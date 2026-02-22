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
	"github.com/Yankzy/usetoro/internal/infrastructure/vector"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/internal/store"
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
	var vectorWorker *ai.VectorSyncWorker
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
				vectorWorker = ai.NewVectorSyncWorker(logger, st, pc, emb, 1*time.Hour)
				logger.Info("✅ AI infrastructure initialized")
			}
		}
	}

	// 6. Initialize Logic
	mgr := connectors.NewManager(logger, cfg, st, vectorWorker)
	worker := connectors.NewWorker(logger, q, mgr)

	// 6a. Initialize Accounting Services (requires AI infra + QBO connector)
	// These are available for on-demand use by GraphQL resolvers or NATS handlers.
	qboConn := mgr.GetConnector("qbo").(*connectors.QBOConnector)
	qboClientFn := accounting.QBOClientFn(qboConn.ClientForRealm)
	var txService *accounting.TransactionService
	var attachService *accounting.AttachableService
	ruleEngineService := accounting.NewRuleEngineService(logger, st.Queries, cache)

	if vectorWorker != nil {
		coaMapper := ai.NewCoAMapper(pc, emb, cfg.AIThreshold)
		entityResolver := ai.NewEntityResolver(st, pc, emb, cfg.AIThreshold)
		txService = accounting.NewTransactionService(logger, st.Queries, entityResolver, coaMapper, qboClientFn, ruleEngineService)
		attachService = accounting.NewAttachableService(logger, qboClientFn)
		logger.Info("✅ Accounting services initialized (AI-assisted)")
	} else {
		// No AI infra: services still usable with explicit AccountHint/VendorHint
		txService = accounting.NewTransactionService(logger, st.Queries, nil, nil, qboClientFn, ruleEngineService)
		attachService = accounting.NewAttachableService(logger, qboClientFn)
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
	workerErrors := make(chan error, 3) // Increased buffer for Vector worker
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start webhook worker
	go func() {
		workerErrors <- worker.Start(ctx)
	}()

	// Start CDC worker if enabled
	if cdcWorker != nil {
		go func() {
			workerErrors <- cdcWorker.Start(ctx)
		}()
	}

	// Start Vector sync worker if enabled
	if vectorWorker != nil {
		go func() {
			vectorWorker.Start(ctx)
		}()
	}

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
