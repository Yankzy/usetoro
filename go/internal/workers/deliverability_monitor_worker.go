package workers

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/nats-io/nats.go"
)

type DeliverabilityMonitorWorker struct {
	db     *database.Queries
	nc     *nats.Conn
	logger *slog.Logger
}

type DeliverabilityWebhookEvent struct {
	EventType string `json:"event_type"` // e.g. spam-checks.updated, ip-blacklist-checks.created
	Email     string `json:"email"`
	Status    string `json:"status"` // e.g. failed, blacklisted
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return NewDeliverabilityMonitorWorker(deps.Store.Queries, deps.Queue, deps.Logger), nil
	})
}

func NewDeliverabilityMonitorWorker(db *database.Queries, nc *nats.Conn, logger *slog.Logger) *DeliverabilityMonitorWorker {
	return &DeliverabilityMonitorWorker{
		db:     db,
		nc:     nc,
		logger: logger.With("worker", "deliverability_monitor"),
	}
}

func (w *DeliverabilityMonitorWorker) Init(ctx context.Context) error {
	return nil
}

func (w *DeliverabilityMonitorWorker) Subscriptions() []SubscriptionConfig {
	return []SubscriptionConfig{
		{
			Subject: "webhooks.mailpool.received",
			Group:   "deliverability-monitor-worker",
		},
	}
}

func (w *DeliverabilityMonitorWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var event DeliverabilityWebhookEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		w.logger.Error("failed to unmarshal deliverability webhook event", "error", err)
		return nil // Drop invalid payload
	}

	w.logger.Info("received deliverability event", "event_type", event.EventType, "email", event.Email, "status", event.Status)

	// If the status indicates a failure, mark the inbox as burned
	if event.Status == "failed" || event.Status == "blacklisted" {
		w.logger.Warn("deliverability failure detected, burning inbox", "email", event.Email)

		// Look up the email account to get its ID and tenant_id
		account, err := w.db.GetEmailAccountByEmail(ctx, event.Email)
		if err != nil {
			w.logger.Error("failed to find email account for deliverability failure", "email", event.Email, "error", err)
			return nil // Drop, we don't know this account
		}

		// Update the status to 'burned'
		err = w.db.UpdateEmailAccountStatus(ctx, database.UpdateEmailAccountStatusParams{
			Status: "burned",
			ID:     account.ID,
		})
		if err != nil {
			w.logger.Error("failed to burn email account", "account_id", account.ID, "error", err)
			return err // Retry via NATS
		}

		w.logger.Warn("inbox burned successfully, automated campaigns paused for this sender", "email", event.Email)
	}

	return nil
}
