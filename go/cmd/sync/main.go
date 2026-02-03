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
	"github.com/Yankzy/usetoro/internal/queue"
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

func run(cfg config.Config, logger *slog.Logger) error {
	ctx := context.Background()

	// 1. NATS Connection
	q, err := queue.NewClient(cfg.NatsURL,
		nats.Name("toro-sync"),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		return fmt.Errorf("queue client init error: %w", err)
	}
	defer q.Close()

	logger.Info("✅ Connected to NATS JetStream")

	// 2. Initialize Logic
	mgr := connectors.NewManager(logger, cfg)
	worker := connectors.NewWorker(logger, q, mgr)

	// 3. Start Worker
	workerErrors := make(chan error, 1)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		workerErrors <- worker.Start(ctx)
	}()

	// 4. Graceful Shutdown
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
