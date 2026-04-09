package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/connectors"
	"github.com/Yankzy/usetoro/internal/erp"
	"github.com/Yankzy/usetoro/internal/erp/adapters/quickbooks"
	"github.com/Yankzy/usetoro/internal/infra/vector"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/Yankzy/usetoro/internal/workers"
	_ "github.com/Yankzy/usetoro/tap/agents/approval"
	_ "github.com/Yankzy/usetoro/tap/agents/csv_mapping"
	_ "github.com/Yankzy/usetoro/tap/agents/reconcile_expense"
	_ "github.com/Yankzy/usetoro/tap/agents/reconcile_revenue"
	_ "github.com/Yankzy/usetoro/tap/agents/stripe_processor"
	"github.com/Yankzy/usetoro/tap/pkg/daemon"
	"github.com/dgraph-io/ristretto"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"
)

func main() {
	flag.Parse()

	// Structured Logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cfg, _, err := config.Load()
	if err != nil {
		logger.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	if err := run(cfg, logger); err != nil {
		logger.Error("Fatal startup error", "error", err)
		os.Exit(1)
	}
}

func run(cfg *config.Config, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 1. NATS Connection & JetStream Setup
	q, err := queue.NewClient(cfg.NATS.URL,
		nats.Name("toro-protocol"),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		return fmt.Errorf("queue client init error: %w", err)
	}
	defer q.Close()

	js, err := q.Conn().JetStream()
	if err != nil {
		return fmt.Errorf("jetstream error: %w", err)
	}

	// Ensure required streams exist
	for _, srvCfg := range cfg.NATS.Services {
		if srvCfg.StreamName != "" {
			if err := q.EnsureStream(&nats.StreamConfig{
				Name:     srvCfg.StreamName,
				Subjects: srvCfg.JetStream.Subjects,
			}); err != nil {
				logger.Warn("Failed to ensure stream", "stream", srvCfg.StreamName, "error", err)
			}
		}
	}

	// 2. PostgreSQL Connection Pool
	dbConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db config error: %w", err)
	}
	dbPool, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		return fmt.Errorf("db connection error: %w", err)
	}
	defer dbPool.Close()

	// 3. Ristretto Cache
	cache, _ := ristretto.NewCache(&ristretto.Config{
		NumCounters: 1e7,
		MaxCost:     100 << 20,
		BufferItems: 64,
	})

	// 4. Store Service
	st, err := store.NewStore(dbPool, cache, cfg.EncryptionKey)
	if err != nil {
		return fmt.Errorf("store init error: %w", err)
	}

	// 5. Initialize AI Infrastructure
	var pc *vector.PineconeClient
	var emb *vector.Embedder
	var entityResolver *ai.EntityResolver
	var coaMapper *ai.CoAMapper

	if os.Getenv("PINECONE_API_KEY") != "" && os.Getenv("OPENAI_API_KEY") != "" {
		pc, _ = vector.NewPineconeClient(os.Getenv("PINECONE_API_KEY"), cfg.PineconeIndex, cfg.EmbeddingDimensions)
		emb, _ = vector.NewEmbedder(os.Getenv("OPENAI_API_KEY"), cfg.EmbeddingModel, cfg.EmbeddingDimensions)
		entityResolver = ai.NewEntityResolver(st, pc, emb, cfg.AIThreshold)
		coaMapper = ai.NewCoAMapper(pc, emb, cfg.AIThreshold)
	}

	// 6. Initialize Workers & Managers
	qboMgr := connectors.NewManager(logger, cfg, st)
	qboConn := qboMgr.GetConnector("qbo").(*connectors.QBOConnector)

	providerFactory := erp.NewProviderFactory(logger, dbPool)
	erp.ResolveQBOAdapter = func(ctx context.Context, realmID string) (*erp.Provider, error) {
		qboClient, err := qboConn.ClientForRealm(ctx, realmID)
		if err != nil {
			return nil, err
		}
		adapter := quickbooks.NewAdapter(qboClient)
		return &erp.Provider{
			SyncCDC:           adapter.SyncCDC,
			FetchTransactions: adapter.FetchTransactions,
			FetchAccounts:     adapter.FetchAccounts,
			PostExpense:       adapter.PostExpense,
			UploadReceipt:     adapter.UploadReceipt,
		}, nil
	}

	ruleEngineService := accounting.NewRuleEngineService(logger, st.Queries, cache)
	attachService := accounting.NewAttachableService(logger, providerFactory)

	workerManager := workers.NewManager(logger, q.Conn())

	fignodeLLM, _ := ai.NewLLMClient(os.Getenv("OPENAI_API_KEY"), "")

	workerDeps := workers.Dependencies{
		Logger:          logger,
		Config:          cfg,
		Queue:           q.Conn(),
		Store:           st,
		DBPool:          dbPool,
		Pinecone:        pc,
		Embedder:        emb,
		EntityResolver:  entityResolver,
		CoAMapper:       coaMapper,
		AttachService:   attachService,
		ProviderFactory: providerFactory,
		RuleEngine:      ruleEngineService,
		LLMClient:       fignodeLLM,
		FetchEntityFn: func(ctx context.Context, tenantID, realmID, entityType, entityID, op string) error {
			return qboConn.FetchEntity(ctx, realmID, entityType, entityID, op)
		},
	}

	if err := workerManager.LoadFromRegistry(workerDeps); err != nil {
		logger.Error("failed to load database workers from registry", "error", err)
		return err
	}

	// Additional Base Workers (Webhook & CDC)
	baseWorker := connectors.NewWorker(logger, q, qboMgr)

	var cdcWorker *connectors.CDCWorker
	if cfg.CDCEnabled {
		cdcWorker = connectors.NewCDCWorker(logger, qboConn, st, cfg.CDCSyncInterval)
	}

	// 7. Initialize Protocol Daemon with shared infra
	loader := func() (*config.Config, *viper.Viper, error) {
		return config.Load()
	}
	d := daemon.New(logger, loader, ":9090", dbPool, q.Conn(), js, entityResolver)

	// 8. Run everything in errgroup
	g, ctx := errgroup.WithContext(ctx)

	//Managed Workers
	g.Go(func() error {
		return workerManager.StartAll(ctx)
	})

	// Protocol Hive Daemon
	g.Go(func() error {
		return d.Run(ctx)
	})

	// Webhook / Legacy Worker
	g.Go(func() error {
		return baseWorker.Start(ctx)
	})

	// CDC Worker (if enabled)
	if cdcWorker != nil {
		g.Go(func() error {
			return cdcWorker.Start(ctx)
		})
	}

	logger.Info("🚀 Protocol Hive + Background Workers Active")
	return g.Wait()
}
