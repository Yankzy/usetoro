package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

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

	// 1. Ensure Stream Exists (Config-driven)
	syncConfig, ok := w.manager.cfg.NATS.Services["sync"]
	if !ok {
		return fmt.Errorf("sync service configuration not found")
	}

	// 1a. Create Service Stream (e.g., SYNC)
	serviceStreamConfig := &nats.StreamConfig{
		Name:        syncConfig.StreamName,
		Subjects:    syncConfig.JetStream.Subjects,
		MaxAge:      syncConfig.JetStream.MaxAge,
		Replicas:    syncConfig.JetStream.Replicas,
		DenyDelete:  syncConfig.JetStream.DenyDelete,
		DenyPurge:   syncConfig.JetStream.DenyPurge,
		AllowRollup: syncConfig.JetStream.AllowRollup,
		AllowDirect: syncConfig.JetStream.AllowDirect,
	}

	// Iterate components
	for _, comp := range syncConfig.Components {
		if comp.StreamName != "" {
			// Case A: Component has its own stream (e.g. QBO_EVENTS)
			// Create a separate stream for this component
			compStreamConfig := &nats.StreamConfig{
				Name:        comp.StreamName,
				Subjects:    comp.JetStream.Subjects,
				MaxAge:      comp.JetStream.MaxAge,
				Replicas:    comp.JetStream.Replicas,
				DenyDelete:  comp.JetStream.DenyDelete,
				DenyPurge:   comp.JetStream.DenyPurge,
				AllowRollup: comp.JetStream.AllowRollup,
				AllowDirect: comp.JetStream.AllowDirect,
			}
			err := w.q.EnsureStream(compStreamConfig)
			if err != nil {
				return fmt.Errorf("failed to ensure component stream %s: %w", comp.StreamName, err)
			}
			w.logger.Info("✅ Ensured component stream", "stream", comp.StreamName)
		} else {
			// Case B: Component shares service stream
			// Append subjects to the main service stream
			serviceStreamConfig.Subjects = append(serviceStreamConfig.Subjects, comp.JetStream.Subjects...)
		}
	}

	// 1b. Create/Update Service Stream (with appended subjects if any)
	err := w.q.EnsureStream(serviceStreamConfig)
	if err != nil {
		w.logger.Warn("failed to ensure service stream using config", "error", err)
		return fmt.Errorf("failed to ensure service stream: %w", err)
	}
	w.logger.Info("✅ Ensured service stream", "stream", serviceStreamConfig.Name)

	// 2. Subscribe to Streams
	js := w.q.JetStream()
	var subs []*nats.Subscription

	// A. Sync Service Subjects (Config-driven)
	for _, subject := range syncConfig.JetStream.Subjects {
		w.logger.Info("Subscribe to sync subject", "subject", subject)

		durableName := "toro-sync-worker"
		sub, err := js.Subscribe(subject, func(msg *nats.Msg) {
			w.processFetch(msg)
		}, nats.Durable(durableName), nats.ManualAck())

		if err != nil {
			// Check for consumer mismatch (e.g. subject mismatch or configuration change)
			if strings.Contains(err.Error(), "consumer already exists") || strings.Contains(err.Error(), "subject does not match") || strings.Contains(err.Error(), "name already in use") {
				w.logger.Warn("⚠️ Consumer mismatch, recreating consumer...", "subject", subject, "durable", durableName, "error", err)

				// Delete the problematic consumer
				if delErr := js.DeleteConsumer(serviceStreamConfig.Name, durableName); delErr != nil {
					w.logger.Error("Failed to delete conflicting consumer", "error", delErr)
					return fmt.Errorf("failed to delete consumer %s: %w", durableName, delErr)
				}

				// Retry subscription
				sub, err = js.Subscribe(subject, func(msg *nats.Msg) {
					w.processFetch(msg)
				}, nats.Durable(durableName), nats.ManualAck())
				if err != nil {
					return fmt.Errorf("failed to resubscribe to %s: %w", subject, err)
				}
				w.logger.Info("✅ Recreated consumer and subscribed", "subject", subject)
			} else {
				return fmt.Errorf("failed to subscribe to %s: %w", subject, err)
			}
		}
		subs = append(subs, sub)
	}

	// B. Component Subjects (Config-driven)
	for compName, compConfig := range syncConfig.Components {
		for _, subject := range compConfig.JetStream.Subjects {
			w.logger.Info("Subscribe to component subject", "component", compName, "subject", subject)

			// Map component to handler
			// TODO: Dynamic mapping if needed. For now, specific check.
			if compName == "qbo" {
				sub, err := js.Subscribe(subject, func(msg *nats.Msg) {
					w.processQBOEvent(msg)
				}, nats.Durable(fmt.Sprintf("toro-%s-webhook-consumer", compName)), nats.ManualAck())
				if err != nil {
					return fmt.Errorf("failed to subscribe to %s: %w", subject, err)
				}
				subs = append(subs, sub)
			}
		}
	}

	w.logger.Info("⚙️ specific subscriptions started")
	<-ctx.Done()

	// Cleanup subscriptions
	for _, sub := range subs {
		sub.Unsubscribe()
	}
	return nil
}

func (w *Worker) processQBOEvent(msg *nats.Msg) {
	if msg.Subject == "qbo.events.dlq" {
		w.logger.Warn("⚠️ Received message from DLQ, acknowledging and skipping to avoid loop", "subject", msg.Subject)
		msg.Ack()
		return
	}

	if msg.Subject == "qbo.events.connected" {
		w.processQBOConnected(msg)
		return
	}

	w.processQBOWebhook(msg)
}

func (w *Worker) processQBOConnected(msg *nats.Msg) {
	w.logger.Info("⚙️ Received QBO Connected Event", "subject", msg.Subject)

	type connectedPayload struct {
		RealmID  string `json:"realm_id"`
		EntityID string `json:"entity_id"`
		Status   string `json:"status"`
	}

	type qboConnectedSyncer interface {
		SyncCompanyInfo(ctx context.Context, tenantID, realmID string) error
		SyncFullChartOfAccounts(ctx context.Context, tenantID, realmID string) (int, error)
		SyncFullCustomers(ctx context.Context, tenantID, realmID string) (int, error)
		SyncFullVendors(ctx context.Context, tenantID, realmID string) (int, error)
		SyncFullPurchases(ctx context.Context, tenantID, realmID string) (int, error)
		SyncFullDeposits(ctx context.Context, tenantID, realmID string) (int, error)
	}

	var payload connectedPayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		w.logger.Error("Failed to parse QBO connected payload", "error", err)
		msg.Nak()
		return
	}

	if payload.RealmID == "" {
		w.logger.Error("Missing realm_id in QBO connected payload")
		msg.Nak()
		return
	}

	connector, ok := w.manager.connectors["qbo"]
	if !ok {
		w.logger.Error("QBO connector not found for connected event")
		msg.Nak()
		return
	}

	qboConn, ok := connector.(qboConnectedSyncer)
	if !ok {
		w.logger.Error("Connector does not support QBO full sync after connect")
		msg.Nak()
		return
	}

	bgCtx := context.Background()

	handleSyncError := func(step string, err error) {
		w.logger.Error(fmt.Sprintf("%s sync failed after QBO connect", step), "error", err, "realm_id", payload.RealmID)

		// 1. Permanent Auth Failure - Terminate immediately
		if errors.Is(err, ErrQBOAuthRevoked) {
			w.logger.Warn("🚫 QBO Auth revoked, dropping connected event", "realm_id", payload.RealmID)
			msg.Term()
			return
		}

		// 2. Resource Not Found - Terminate immediately
		if strings.Contains(err.Error(), "no rows in result set") {
			w.logger.Warn("Tokens not found, dropping connected event", "realm_id", payload.RealmID)
			msg.Term()
			return
		}

		// 3. Handle DLQ (Dead Letter Queue) for persistent failures (e.g. Circuit Breaker open)
		metadata, metaErr := msg.Metadata()
		if metaErr == nil && metadata.NumDelivered > 3 {
			w.logger.Error("🚨 QBO sync failed persistently. Moving to DLQ.", 
				"realm_id", payload.RealmID, 
				"step", step, 
				"attempts", metadata.NumDelivered,
				"error", err,
			)
			
			// Move to DLQ subject
			dlqSubject := "qbo.events.dlq"
			if pubErr := w.q.Publish(dlqSubject, msg.Data); pubErr != nil {
				w.logger.Error("Failed to publish to QBO DLQ", "error", pubErr)
			}
			
			// Terminate the message so it doesn't retry anymore
			msg.Term()
			return
		}

		// 4. Otherwise, Nak for standard retry
		msg.Nak()
	}

	if err := qboConn.SyncCompanyInfo(bgCtx, payload.EntityID, payload.RealmID); err != nil {
		handleSyncError("Company info", err)
		return
	}

	if _, err := qboConn.SyncFullChartOfAccounts(bgCtx, payload.EntityID, payload.RealmID); err != nil {
		handleSyncError("Full CoA", err)
		return
	}

	if _, err := qboConn.SyncFullCustomers(bgCtx, payload.EntityID, payload.RealmID); err != nil {
		handleSyncError("Full Customers", err)
		return
	}

	if _, err := qboConn.SyncFullVendors(bgCtx, payload.EntityID, payload.RealmID); err != nil {
		handleSyncError("Full Vendors", err)
		return
	}

	if _, err := qboConn.SyncFullPurchases(bgCtx, payload.EntityID, payload.RealmID); err != nil {
		handleSyncError("Full Purchases", err)
		return
	}

	if _, err := qboConn.SyncFullDeposits(bgCtx, payload.EntityID, payload.RealmID); err != nil {
		handleSyncError("Full Deposits", err)
		return
	}

	// Trigger rule engine bootstrap after all syncs are done
	if qboObj, ok := connector.(*QBOConnector); ok {
		if err := qboObj.PublishRuleBootstrapTask(bgCtx, payload.RealmID); err != nil {
			w.logger.Error("Failed to trigger rule engine bootstrap after connected sync", "error", err, "realm_id", payload.RealmID)
		}
	}

	msg.Ack()
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
