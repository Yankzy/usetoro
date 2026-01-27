package connectors

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"
)

// Worker subscribes to NATS and executes connector jobs.
type Worker struct {
	logger  *slog.Logger
	js      nats.JetStreamContext
	manager *Manager
}

func NewWorker(logger *slog.Logger, js nats.JetStreamContext, manager *Manager) *Worker {
	return &Worker{
		logger:  logger,
		js:      js,
		manager: manager,
	}
}

func (w *Worker) Start(ctx context.Context) error {
	w.logger.Info("⚙️ Sync Worker starting...")

	// Listen for sync commands
	// Subject: cmd.sync.fetch
	sub, err := w.js.Subscribe("cmd.sync.fetch", func(msg *nats.Msg) {
		w.processFetch(msg)
	}, nats.Durable("toro-sync-worker"), nats.ManualAck())

	if err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	w.logger.Info("⚙️ Listening on cmd.sync.fetch")
	<-ctx.Done()
	return sub.Unsubscribe()
}

func (w *Worker) processFetch(msg *nats.Msg) {
	// Simple stub for payload parsing
	// In real app, unmarshal JSON
	provider := msg.Header.Get("Provider")
	tenantID := msg.Header.Get("Tenant-ID")

	w.logger.Info("⚙️ Received Sync Command", "subject", msg.Subject)

	err := w.manager.FetchData(context.Background(), provider, tenantID)
	if err != nil {
		w.logger.Error("Sync failed", "error", err)
		msg.Nak()
		return
	}

	msg.Ack()
}
