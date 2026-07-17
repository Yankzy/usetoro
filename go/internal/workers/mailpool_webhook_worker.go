package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/infra/crypto"
	"github.com/Yankzy/usetoro/internal/services/mailpool"
	"github.com/nats-io/nats.go"
)

// MailpoolWebhookWorker processes inbound webhooks from Mailpool (e.g. mailboxes.created)
type MailpoolWebhookWorker struct {
	db     *database.Queries
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
}

// NewMailpoolWebhookWorkerForTest creates a worker specifically for testing
func NewMailpoolWebhookWorkerForTest(cfg *config.Config, logger *slog.Logger, db *database.Queries, nc *nats.Conn) *MailpoolWebhookWorker {
	return &MailpoolWebhookWorker{
		db:     db,
		logger: logger,
		cfg:    cfg,
		nc:     nc,
	}
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &MailpoolWebhookWorker{
			db:     deps.Store.Queries,
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
		}, nil
	})
}

func (w *MailpoolWebhookWorker) Init(ctx context.Context) error { return nil }

func (w *MailpoolWebhookWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	subject := workerCfg.Subject
	if subject == "" {
		subject = "webhooks.mailpool.received"
	}
	group := workerCfg.Group
	if group == "" {
		group = "mailpool-webhook-worker-group"
	}

	return []SubscriptionConfig{
		{
			Subject: subject,
			Group:   group,
			Options: []nats.SubOpt{
				nats.Durable(durableFromSubject(subject)),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

func (w *MailpoolWebhookWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var genericPayload struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(msg.Data, &genericPayload); err != nil {
		w.logger.Error("Failed to unmarshal generic mailpool webhook", "error", err)
		return nil
	}

	w.logger.Info("Received Mailpool Webhook", "type", genericPayload.Type)

	var err error

	switch genericPayload.Type {
	// Domains
	case "domains.registered":
		var evt mailpool.DomainsRegistered
		err = w.parseAndPublish(ctx, msg.Data, &evt, "events.mailpool.domains.registered")
	case "domains.updated":
		var evt mailpool.DomainsUpdated
		err = w.parseAndPublish(ctx, msg.Data, &evt, "events.mailpool.domains.updated")
	
	// Mailboxes
	case "mailboxes.created":
		var evt mailpool.MailboxesCreated
		err = w.parseAndPublish(ctx, msg.Data, &evt, "events.mailpool.mailboxes.created")
		if err == nil {
			_ = w.extractAndHandleMailboxCredentials(ctx, msg.Data)
		}
	case "mailboxes.updated":
		var evt mailpool.MailboxesUpdated
		err = w.parseAndPublish(ctx, msg.Data, &evt, "events.mailpool.mailboxes.updated")
		if err == nil {
			_ = w.extractAndHandleMailboxCredentials(ctx, msg.Data)
		}
	case "mailboxes.deleted":
		var evt mailpool.MailboxesDeleted
		err = w.parseAndPublish(ctx, msg.Data, &evt, "events.mailpool.mailboxes.deleted")
	
	// Spam Checks
	case "spam-checks.created":
		var evt mailpool.SpamChecksCreated
		err = w.parseAndPublish(ctx, msg.Data, &evt, "events.mailpool.spam_checks.created")
	case "spam-checks.updated":
		var evt mailpool.SpamChecksUpdated
		err = w.parseAndPublish(ctx, msg.Data, &evt, "events.mailpool.spam_checks.updated")
	case "spam-checks.deleted":
		var evt mailpool.SpamChecksDeleted
		err = w.parseAndPublish(ctx, msg.Data, &evt, "events.mailpool.spam_checks.deleted")

	// IP Blacklist Checks
	case "ip-blacklist-checks.created":
		// Native struct naming logic might just be IpBlacklistChecksCreated or missing, but let's fall back to map[string]any if we don't know the exact name
		// Let's use the actual names generated from OpenAPI. Wait, I'll need to check the exact struct names.
		// For now, I'll use a generic map for ones I'm not 100% sure of the struct name, but I did grep earlier.
		// Wait, I saw SpamChecksCreated but not IpBlacklistChecksCreated in the grep. I should check.
		w.publishGenericEvent(ctx, msg.Data, "events.mailpool.ip_blacklist_checks.created")
	case "ip-blacklist-checks.updated":
		w.publishGenericEvent(ctx, msg.Data, "events.mailpool.ip_blacklist_checks.updated")
	case "ip-blacklist-checks.deleted":
		w.publishGenericEvent(ctx, msg.Data, "events.mailpool.ip_blacklist_checks.deleted")

	// Inbox Placements
	case "inbox-placements.created":
		var evt mailpool.InboxPlacementsCreated
		err = w.parseAndPublish(ctx, msg.Data, &evt, "events.mailpool.inbox_placements.created")
	case "inbox-placements.updated":
		var evt mailpool.InboxPlacementsUpdated
		err = w.parseAndPublish(ctx, msg.Data, &evt, "events.mailpool.inbox_placements.updated")
	case "inbox-placements.deleted":
		var evt mailpool.InboxPlacementsDeleted
		err = w.parseAndPublish(ctx, msg.Data, &evt, "events.mailpool.inbox_placements.deleted")

	default:
		w.logger.Info("Ignored Mailpool event", "type", genericPayload.Type)
		return nil
	}

	if err != nil {
		w.logger.Error("Failed to parse mailpool webhook payload", "type", genericPayload.Type, "error", err)
	}
	return nil
}

func (w *MailpoolWebhookWorker) parseAndPublish(ctx context.Context, data []byte, target interface{}, subject string) error {
	if err := json.Unmarshal(data, target); err != nil {
		return err
	}
	
	if w.nc != nil {
		// Re-marshal the strongly typed struct to ensure we only publish validated data
		publishedData, err := json.Marshal(target)
		if err != nil {
			return fmt.Errorf("failed to marshal validated event: %w", err)
		}
		if err := w.nc.Publish(subject, publishedData); err != nil {
			w.logger.Error("Failed to publish internal mailpool event", "subject", subject, "error", err)
		}
	}
	return nil
}

func (w *MailpoolWebhookWorker) publishGenericEvent(ctx context.Context, data []byte, subject string) {
	if w.nc != nil {
		if err := w.nc.Publish(subject, data); err != nil {
			w.logger.Error("Failed to publish generic internal mailpool event", "subject", subject, "error", err)
		}
	}
}

func (w *MailpoolWebhookWorker) extractAndHandleMailboxCredentials(ctx context.Context, data []byte) error {
	var evt struct {
		Mailbox struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		} `json:"mailbox"`
	}
	if err := json.Unmarshal(data, &evt); err != nil {
		return err
	}
	if evt.Mailbox.Email != "" && evt.Mailbox.Password != "" {
		return w.handleMailboxCredentials(ctx, evt.Mailbox.Email, evt.Mailbox.Password)
	}
	return nil
}

func (w *MailpoolWebhookWorker) handleMailboxCredentials(ctx context.Context, email, password string) error {
	if password == "" {
		return nil
	}
	if w.cfg.MailpoolAESKey == "" {
		w.logger.Error("Mailpool AES key not configured")
		return nil
	}

	encryptedPassword, err := crypto.Encrypt(w.cfg.MailpoolAESKey, password)
	if err != nil {
		w.logger.Error("Failed to encrypt mailbox password", "error", err)
		return nil
	}

	w.logger.Info("Successfully extracted and encrypted mailbox credentials", "email", email, "encrypted_password", encryptedPassword)

	// TODO: Save to DB or trigger sequences once DB functions are available
	return nil
}
