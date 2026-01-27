package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Yankzy/usetoro/internal/api"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/ingest"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/dgraph-io/ristretto"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
)

func main() {
	// 1. Setup Structured Logging (JSON)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 2. Load and Validate Configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Error("Configuration Loading Failed", "error", err)
		os.Exit(1)
	}

	// 3. Start the Application
	if err := run(cfg, logger); err != nil {
		logger.Error("Fatal startup error", "error", err)
		os.Exit(1)
	}
}

func run(cfg config.Config, logger *slog.Logger) error {
	ctx := context.Background()

	// =========================================================================
	// INFRASTRUCTURE INITIALIZATION
	// =========================================================================

	// 1. PostgreSQL (Metadata Store)
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
		logger.Error("db ping failed",
			"error", err,
			"host", dbConfig.ConnConfig.Host,
			"port", dbConfig.ConnConfig.Port,
			"database", dbConfig.ConnConfig.Database,
		)
		return fmt.Errorf("db ping failed: %w", err)
	}
	logger.Info("✅ Connected to PostgreSQL")

	// 2. NATS JetStream (Durability Layer)
	nc, err := nats.Connect(cfg.NatsURL,
		nats.Name("toro-ingress"),
		nats.MaxReconnects(10),
		nats.ReconnectWait(2*time.Second),
		nats.ReconnectJitter(500*time.Millisecond, 2*time.Second),
	)
	if err != nil {
		return fmt.Errorf("nats connect error: %w", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		return fmt.Errorf("jetstream init error: %w", err)
	}
	logger.Info("✅ Connected to NATS JetStream")

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

	// =========================================================================
	// APPLICATION WIRING
	// =========================================================================

	st := store.NewStore(dbPool, cache)
	pub := ingest.NewPublisher(nc, js)
	// DI: Create Server
	srv := api.NewServer(cfg, logger, st, pub)

	// =========================================================================
	// STARTUP & GRACEFUL SHUTDOWN
	// =========================================================================

	serverErrors := srv.Start()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	// We wait for either a server error or a shutdown signal
	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)

	case sig := <-shutdown:
		logger.Info("Shutdown signal received", "signal", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			srv.Close()
			return fmt.Errorf("could not stop server gracefully: %w", err)
		}
	}

	return nil
}
