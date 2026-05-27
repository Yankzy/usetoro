package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/infra/vector"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/dgraph-io/ristretto"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
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
			AllowMsgTTL: erpCfg.JetStream.AllowMsgTTL,
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
			AllowMsgTTL: ledgerCfg.JetStream.AllowMsgTTL,
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
	_, err = store.NewStore(dbPool, cache, cfg.EncryptionKey)
	if err != nil {
		return fmt.Errorf("store init error: %w", err)
	}

	// 5. Initialize AI Infrastructure (Optional)
	if os.Getenv("PINECONE_API_KEY") != "" && os.Getenv("OPENAI_API_KEY") != "" {
		pc, pcErr := vector.NewPineconeClient(
			os.Getenv("PINECONE_API_KEY"),
			cfg.PineconeIndex,
			cfg.EmbeddingDimensions,
		)
		if pcErr != nil {
			logger.Warn("Failed to initialize Pinecone client", "error", pcErr)
		} else {
			emb, embErr := vector.NewEmbedder(
				os.Getenv("OPENAI_API_KEY"),
				cfg.EmbeddingModel,
				cfg.EmbeddingDimensions,
			)
			if embErr != nil {
				logger.Warn("Failed to initialize OpenAI embedder", "error", embErr)
			} else {
				logger.Info("✅ AI infrastructure initialized (for local testing)")
				_ = pc
				_ = emb
			}
		}
	}

	logger.Info("⏭️  Sync Background Workers migrated to Protocol Binary")

	// 8. Graceful Shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-shutdown:
		logger.Info("Shutdown signal received", "signal", sig)
	}

	return nil
}
