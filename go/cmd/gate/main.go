package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
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
	cfg := config.Load()

	// GO CONCEPT: Fail Fast
	if cfg.DatabaseURL == "" {
		logger.Error("DATABASE_URL is required")
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
	dbConfig.MaxConns = 25
	dbConfig.MinConns = 5

	dbPool, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		return fmt.Errorf("db connection error: %w", err)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		return fmt.Errorf("db ping failed: %w", err)
	}
	logger.Info("✅ Connected to PostgreSQL")

	// 2. NATS JetStream (Durability Layer)
	nc, err := nats.Connect(cfg.NatsURL,
		nats.Name("toro-ingress"),
		nats.MaxReconnects(-1),
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
	pub := ingest.NewPublisher(js)
	h := api.NewHandler(logger, st, pub)
	mux := api.NewRouter(h)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: mux,
		// Timeouts
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// =========================================================================
	// STARTUP & GRACEFUL SHUTDOWN
	// =========================================================================

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("🐂 Toro Ingress started", "port", cfg.Port)
		serverErrors <- srv.ListenAndServe()
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

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
