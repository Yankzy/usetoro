package connectors

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/nats-io/nats.go"
)

// Worker subscribes to NATS and executes connector jobs.
type Worker struct {
	logger  *slog.Logger
	q       *queue.Client
	manager *Manager
}

func NewWorker(logger *slog.Logger, q *queue.Client, manager *Manager) *Worker {
	return &Worker{
		logger:  logger,
		q:       q,
		manager: manager,
	}
}

func (w *Worker) Start(ctx context.Context) error {
	w.logger.Info("⚙️ Sync Worker starting...")

	// 1. Ensure Stream Exists
	err := w.q.EnsureStream(&nats.StreamConfig{
		Name:        "SYNC",
		Subjects:    []string{"cmd.sync.*", "qbo_webhook"},
		MaxAge:      24 * time.Hour,
		DenyDelete:  true,
		DenyPurge:   true,
		AllowRollup: false,
		AllowDirect: true,
	})
	if err != nil {
		return fmt.Errorf("failed to ensure stream: %w", err)
	}

	// 2. Listen for sync commands
	// Subject: cmd.sync.fetch
	js := w.q.JetStream()
	sub, err := js.Subscribe("cmd.sync.fetch", func(msg *nats.Msg) {
		w.processFetch(msg)
	}, nats.Durable("toro-sync-worker"), nats.ManualAck())

	if err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	// 3. Listen for QBO webhooks
	// Subject: qbo_webhook
	qboSub, err := js.Subscribe("qbo_webhook", func(msg *nats.Msg) {
		w.processQBOWebhook(msg)
	}, nats.Durable("toro-qbo-webhook-consumer"), nats.ManualAck())

	if err != nil {
		sub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to qbo_webhook: %w", err)
	}

	w.logger.Info("⚙️ Listening on cmd.sync.fetch and qbo_webhook")
	<-ctx.Done()

	// Cleanup subscriptions
	sub.Unsubscribe()
	qboSub.Unsubscribe()
	return nil
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
