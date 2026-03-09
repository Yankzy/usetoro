package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/connectors"
	"github.com/Yankzy/usetoro/internal/erp"
	"github.com/nats-io/nats.go"
)

// ERPEventWorker subscribes to the NATS JetStream subject and processes ERP sync/update events.
type ERPEventWorker struct {
	logger  *slog.Logger
	cfg     *config.Config
	nc      *nats.Conn
	js      nats.JetStreamContext
	factory erp.ProviderFactory

	// Function pointer to the CDC upsert logic to avoid circular dependency
	cdcUpsertFn func(ctx context.Context, tenantID, realmID, entityType, entityID, operation string) error
}

func NewERPEventWorker(
	logger *slog.Logger,
	cfg *config.Config,
	nc *nats.Conn,
	factory erp.ProviderFactory,
	cdcUpsertFn func(ctx context.Context, tenantID, realmID, entityType, entityID, operation string) error,
) (*ERPEventWorker, error) {

	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	return &ERPEventWorker{
		logger:      logger,
		cfg:         cfg,
		nc:          nc,
		js:          js,
		factory:     factory,
		cdcUpsertFn: cdcUpsertFn,
	}, nil
}

// Start begins processing events from the configured NATS subject.
func (w *ERPEventWorker) Start(ctx context.Context) error {
	subject := w.cfg.NatsERPEventSubject

	// We use a Queue Subscribe to ensure multiple instances of Toro share the load
	// and don't double-process the same event.
	sub, err := w.js.QueueSubscribe(subject, "toro-erp-event-workers", func(msg *nats.Msg) {
		w.processMessage(ctx, msg)
	}, nats.ManualAck())

	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", subject, err)
	}

	w.logger.Info("🎧 ERP Event Worker started", "subject", subject)

	<-ctx.Done()
	w.logger.Info("🛑 ERP Event Worker shutting down")
	return sub.Unsubscribe()
}

func (w *ERPEventWorker) processMessage(ctx context.Context, msg *nats.Msg) {
	var event connectors.ERPEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		w.logger.Error("Failed to parse ERPEvent JSON", "error", err, "data", string(msg.Data))
		msg.Term() // Unrecoverable format error
		return
	}

	w.logger.Info("Processing ERP Event", "type", event.Type, "tenant_id", event.TenantID, "realm_id", event.RealmID)

	var err error
	switch event.Type {
	case connectors.EventCDCSync:
		err = w.handleCDCSync(ctx, event)
	case connectors.EventRecategorizeTransaction:
		err = w.handleRecategorizeTransaction(ctx, event)
	default:
		w.logger.Warn("Unknown ERPEvent type", "type", event.Type)
		msg.Term()
		return
	}

	if err != nil {
		w.logger.Error("Failed to process ERP Event", "type", event.Type, "error", err)
		// NAK the message so JetStream redelivers it after a delay
		msg.Nak()
	} else {
		// Event fully handled, ACK
		msg.Ack()
	}
}

func (w *ERPEventWorker) handleCDCSync(ctx context.Context, event connectors.ERPEvent) error {
	var payload connectors.CDCSyncPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to parse CDCSyncPayload: %w", err)
	}

	w.logger.Info("Handling CDC Sync Event", "entity_type", payload.EntityType, "entity_id", payload.EntityID)

	// Call the generic ingest/upsert function provided during initialization
	if w.cdcUpsertFn != nil {
		return w.cdcUpsertFn(ctx, event.TenantID, event.RealmID, payload.EntityType, payload.EntityID, payload.Operation)
	}

	return fmt.Errorf("CDCUpsertFn is not configured in worker")
}

func (w *ERPEventWorker) handleRecategorizeTransaction(ctx context.Context, event connectors.ERPEvent) error {
	var payload connectors.RecategorizePayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("failed to parse RecategorizePayload: %w", err)
	}

	w.logger.Info("Handling Recategorize Transaction Event", "erp_entity_id", payload.ERPEntityID, "new_account", payload.NewAccountID)

	// 1. Resolve Provider based on tenant or realm
	var provider *erp.Provider
	var err error

	if event.RealmID != "" {
		provider, err = w.factory.GetProviderForRealm(ctx, "quickbooks_online", event.RealmID)
	} else {
		provider, err = w.factory.GetProviderForTenant(ctx, event.TenantID)
	}

	if err != nil {
		return fmt.Errorf("failed to resolve ERP provider: %w", err)
	}

	if provider.UpdateExpenseCategory == nil {
		return fmt.Errorf("resolved provider does not support UpdateExpenseCategory")
	}

	// 2. Execute Recategorization Push
	err = provider.UpdateExpenseCategory(ctx, payload.ERPEntityID, payload.EntityType, payload.NewAccountID, payload.NewVendorID)
	if err != nil {
		return fmt.Errorf("failed to push category update to ERP: %w", err)
	}

	w.logger.Info("✅ Successfully recategorized transaction in ERP", "erp_entity_id", payload.ERPEntityID)
	return nil
}
