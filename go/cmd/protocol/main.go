package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Yankzy/usetoro/internal/agent"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/nats-io/nats.go"
)

func main() {
	// 1. Setup Logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 2. Load Config
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

	// 1. NATS Connection handled by queue now

	// 2. Initialize Queue Client
	// Note: protocol was trying to create its own connection before, now we use queue
	// We should probably reuse the connection options from before or just use defaults
	q, err := queue.NewClient(cfg.NatsURL,
		nats.Name("toro-protocol"),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		return fmt.Errorf("queue client init error: %w", err)
	}
	defer q.Close()

	// 3. Initialize Agent
	// Note: We might want database connection here too eventually, similar to Gate
	ag := agent.NewAgent(logger, q.JetStream(), q)

	// 3. Start Agent
	// Run in a goroutine because Start blocks
	agentErrors := make(chan error, 1)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		agentErrors <- ag.Start(ctx)
	}()

	// 4. Graceful Shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-agentErrors:
		return fmt.Errorf("agent error: %w", err)
	case sig := <-shutdown:
		logger.Info("Shutdown signal received", "signal", sig)
		cancel() // Signal agent to stop

		// Wait for agent to cleanup if needed, or just exit since NATS close is deferred
		time.Sleep(1 * time.Second)
	}

	return nil
}
