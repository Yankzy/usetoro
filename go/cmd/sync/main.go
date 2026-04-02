package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	"github.com/dgraph-io/ristretto"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
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
	ctx := context.Background()

	// 1. NATS Connection
	q, err := queue.NewClient(cfg.NATS.URL,
		nats.Name("toro-sync"),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		return fmt.Errorf("queue client init error: %w", err)
	}
	defer q.Close()
	logger.Info("✅ Connected to NATS JetStream")

	// Ensure required streams exist synchronously
	erpCfg, erpOk := cfg.NATS.Services["erp"]
	if erpOk {
		if err := q.EnsureStream(&nats.StreamConfig{
			Name:        erpCfg.StreamName,
			Subjects:    erpCfg.JetStream.Subjects,
			MaxAge:      erpCfg.JetStream.MaxAge,
			Replicas:    erpCfg.JetStream.Replicas,
			DenyDelete:  erpCfg.JetStream.DenyDelete,
			DenyPurge:   erpCfg.JetStream.DenyPurge,
			AllowRollup: erpCfg.JetStream.AllowRollup,
			AllowDirect: erpCfg.JetStream.AllowDirect,
		}); err != nil {
			logger.Warn("Failed to ensure ERP stream", "error", err)
		} else {
			logger.Info("✅ Ensured TORO_ERP_EVENTS stream synchronously")
		}
	} else {
		logger.Warn("erp nats config not found")
	}

	if ledgerCfg, ok := cfg.NATS.Services["ledger"]; ok {
		if err := q.EnsureStream(&nats.StreamConfig{
			Name:        ledgerCfg.StreamName,
			Subjects:    ledgerCfg.JetStream.Subjects,
			MaxAge:      ledgerCfg.JetStream.MaxAge,
			Replicas:    ledgerCfg.JetStream.Replicas,
			DenyDelete:  ledgerCfg.JetStream.DenyDelete,
			DenyPurge:   ledgerCfg.JetStream.DenyPurge,
			AllowRollup: ledgerCfg.JetStream.AllowRollup,
			AllowDirect: ledgerCfg.JetStream.AllowDirect,
		}); err != nil {
			logger.Warn("Failed to ensure LEDGER stream", "error", err)
		} else {
			logger.Info("✅ Ensured LEDGER stream synchronously")
		}
	} else {
		logger.Warn("ledger nats config not found")
	}

	// 2. PostgreSQL (Metadata Store)
	dbConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db config error: %w", err)
	}
	dbConfig.MaxConns = int32(cfg.DBMaxConns)
	dbConfig.MinConns = int32(cfg.DBMinConns)

	dbPool, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		return fmt.Errorf("db connection error: %w", err)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		return fmt.Errorf("db ping failed: %w", err)
	}
	logger.Info("✅ Connected to PostgreSQL")

	// 3. Ristretto Cache (L1 Cache)
	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 1e7,
		MaxCost:     100 << 20, // 100 * 1MiB
		BufferItems: 64,
	})
	if err != nil {
		return fmt.Errorf("cache init error: %w", err)
	}
	logger.Info("✅ Initialized Cache")

	// 4. Store
	st, err := store.NewStore(dbPool, cache, cfg.EncryptionKey)
	if err != nil {
		return fmt.Errorf("store init error: %w", err)
	}

	// 5. Initialize AI Infrastructure (Optional)
	var vectorWorker *workers.VectorSyncWorker
	var pc *vector.PineconeClient
	var emb *vector.Embedder
	if os.Getenv("PINECONE_API_KEY") != "" && os.Getenv("OPENAI_API_KEY") != "" {
		var pcErr error
		pc, pcErr = vector.NewPineconeClient(
			os.Getenv("PINECONE_API_KEY"),
			cfg.PineconeIndex,
			cfg.EmbeddingDimensions,
		)
		if pcErr != nil {
			logger.Warn("Failed to initialize Pinecone client", "error", pcErr)
		} else {
			var embErr error
			emb, embErr = vector.NewEmbedder(
				os.Getenv("OPENAI_API_KEY"),
				cfg.EmbeddingModel,
				cfg.EmbeddingDimensions,
			)
			if embErr != nil {
				logger.Warn("Failed to initialize OpenAI embedder", "error", embErr)
			} else {
				vectorWorker, err = workers.NewVectorSyncWorker(logger, st, pc, emb, q.Conn())
				if err != nil {
					logger.Warn("Failed to init VectorSyncWorker", "error", err)
					vectorWorker = nil
				}
				logger.Info("✅ AI infrastructure initialized")
			}
		}
	}

	// 6. Initialize Logic
	mgr := connectors.NewManager(logger, cfg, st)
	worker := connectors.NewWorker(logger, q, mgr)

	// 6a. Initialize Accounting Services (requires AI infra + QBO connector)
	// These are available for on-demand use by GraphQL resolvers or NATS handlers.
	qboConn := mgr.GetConnector("qbo").(*connectors.QBOConnector)

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

	var txService *accounting.TransactionService
	var attachService *accounting.AttachableService
	var coaMapper *ai.CoAMapper
	var entityResolver *ai.EntityResolver
	ruleEngineService := accounting.NewRuleEngineService(logger, st.Queries, cache)

	if vectorWorker != nil {
		coaMapper = ai.NewCoAMapper(pc, emb, cfg.AIThreshold)
		entityResolver = ai.NewEntityResolver(st, pc, emb, cfg.AIThreshold)
		txService = accounting.NewTransactionService(logger, st.Queries, entityResolver, coaMapper, providerFactory, ruleEngineService, q.Conn(), erpCfg.StreamName)
		attachService = accounting.NewAttachableService(logger, providerFactory)
		logger.Info("✅ Accounting services initialized (AI-assisted)")
	} else {
		// No AI infra: services still usable with explicit AccountHint/VendorHint
		txService = accounting.NewTransactionService(logger, st.Queries, nil, nil, providerFactory, ruleEngineService, q.Conn(), erpCfg.StreamName)
		attachService = accounting.NewAttachableService(logger, providerFactory)
		logger.Info("✅ Accounting services initialized (manual hints only)")
	}
	_ = txService     // available for future GraphQL resolver / NATS handler wiring
	_ = attachService // available for future GraphQL resolver / NATS handler wiring

	// 6. Initialize CDC Worker (if enabled)
	var cdcWorker *connectors.CDCWorker
	if cfg.CDCEnabled {
		qboConn := mgr.GetConnector("qbo").(*connectors.QBOConnector)
		cdcWorker = connectors.NewCDCWorker(logger, qboConn, st, cfg.CDCSyncInterval)
		logger.Info("✅ CDC Worker initialized", "interval", cfg.CDCSyncInterval, "enabled", true)
	} else {
		logger.Info("⏭️  CDC Worker disabled", "enabled", false)
	}

	// 7. Start Workers
	workerManager := workers.NewManager(logger, q.Conn())

	erpEventWorker, err := workers.NewERPEventWorker(
		logger,
		cfg,
		q.Conn(),
		providerFactory,
		func(workerCtx context.Context, tenantID, realmID, entityType, entityID, operation string) error {
			return qboConn.FetchEntity(workerCtx, realmID, entityType, entityID, operation)
		},
	)
	if err != nil {
		return fmt.Errorf("failed to init erp event worker: %w", err)
	}
	workerManager.Register(erpEventWorker)

	if vectorWorker != nil {
		workerManager.Register(vectorWorker)
	}

	attachWorker, err := workers.NewAttachableWorker(logger, q.Conn(), attachService)
	if err != nil {
		return fmt.Errorf("failed to init attachable worker: %w", err)
	}
	workerManager.Register(attachWorker)

	txWorker, err := workers.NewTransactionWorker(logger, q.Conn(), dbPool, st.Queries, entityResolver, coaMapper, ruleEngineService, providerFactory)
	if err != nil {
		return fmt.Errorf("failed to init transaction worker: %w", err)
	}
	workerManager.Register(txWorker)

	cleanupWorker, err := workers.NewCleanupWorker(st.Queries, entityResolver, coaMapper, q.Conn(), logger)
	if err != nil {
		return fmt.Errorf("failed to init cleanup worker: %w", err)
	}
	workerManager.Register(cleanupWorker)

	fignodeLLM, _ := ai.NewLLMClient(os.Getenv("OPENAI_API_KEY"), "")
	
	enrichmentWorker, err := workers.NewEnrichmentWorker(st.Queries, q.Conn(), logger, fignodeLLM)
	if err != nil {
		return fmt.Errorf("failed to init enrichment worker: %w", err)
	}
	workerManager.Register(enrichmentWorker)

	fignodePublisherWorker, err := workers.NewFignodePublisherWorker(st.Queries, q.Conn(), logger, fignodeLLM)
	if err != nil {
		return fmt.Errorf("failed to init fignode publisher worker: %w", err)
	}
	workerManager.Register(fignodePublisherWorker)

	workerErrors := make(chan error, 6) // Increased buffer
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start webhook worker (legacy/current nats subscriber)
	go func() {
		workerErrors <- worker.Start(ctx)
	}()

	// Start CDC worker if enabled
	if cdcWorker != nil {
		go func() {
			workerErrors <- cdcWorker.Start(ctx)
		}()
	}

	// Start Managed Workers
	go func() {
		workerErrors <- workerManager.StartAll(ctx)
	}()

	// 8. Graceful Shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-workerErrors:
		return fmt.Errorf("worker error: %w", err)
	case sig := <-shutdown:
		logger.Info("Shutdown signal received", "signal", sig)
		cancel()
		time.Sleep(1 * time.Second)
	}

	return nil
}
