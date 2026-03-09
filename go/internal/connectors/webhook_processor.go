package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nats-io/nats.go"
)

// ERPEventType defines the actions routed through NATS
type ERPEventType string

const (
	EventCDCSync                 ERPEventType = "CDC_SYNC"
	EventRecategorizeTransaction ERPEventType = "RECATEGORIZE_TXN"
)

// CDCSyncPayload is used when Type == EventCDCSync
type CDCSyncPayload struct {
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	Operation  string `json:"operation"`
}

// RecategorizePayload is used when Type == EventRecategorizeTransaction
type RecategorizePayload struct {
	ERPEntityID  string `json:"erp_entity_id"`
	EntityType   string `json:"entity_type"`
	NewAccountID string `json:"new_account_id"`
	NewVendorID  string `json:"new_vendor_id"`
}

// ERPEvent represents the unified schema published to the JetStream subject
type ERPEvent struct {
	Type     ERPEventType    `json:"type"`
	TenantID string          `json:"tenant_id"`
	RealmID  string          `json:"realm_id"` // Sometimes available instead of TenantID
	Payload  json.RawMessage `json:"payload"`
}

// QBOCloudEvent represents QuickBooks Online webhooks in CloudEvents format.
// Reference: https://blogs.intuit.com/2025/11/12/upcoming-change-to-webhooks-payload-structure/
// Migration deadline: May 15, 2026
type QBOCloudEvent struct {
	SpecVersion     string                 `json:"specversion"`     // "1.0"
	ID              string                 `json:"id"`              // Event UUID
	Source          string                 `json:"source"`          // "intuit.xxx"
	Type            string                 `json:"type"`            // "qbo.account.created.v1"
	DataContentType string                 `json:"datacontenttype"` // "application/json"
	Time            string                 `json:"time"`            // ISO8601
	IntuitEntityID  string                 `json:"intuitentityid"`  // Entity ID
	IntuitAccountID string                 `json:"intuitaccountid"` // RealmID
	Data            map[string]interface{} `json:"data"`            // Payload (currently empty)
}

// processQBOWebhook handles incoming QBO CloudEvents webhooks.
// Note: One notification can contain multiple events for different companies.
func (w *Worker) processQBOWebhook(msg *nats.Msg) {
	w.logger.Info("⚙️ Received QBO Webhook",
		"subject", msg.Subject,
		"request_id", msg.Header.Get("Request-ID"),
		"provider_event_id", msg.Header.Get("Provider-Event-ID"),
	)

	var events []QBOCloudEvent
	if err := json.Unmarshal(msg.Data, &events); err != nil {
		w.logger.Error("Failed to parse QBO webhook payload", "error", err)
		msg.Nak()
		return
	}

	w.logger.Info("Processing QBO webhook", "event_count", len(events))

	// Process each CloudEvent
	for _, event := range events {
		// Parse entity type and operation from event.Type
		// Format: "qbo.{entity}.{operation}.v1"
		// Example: "qbo.account.created.v1" -> entity="Account", operation="Create"
		entityType, operation := parseCloudEventType(event.Type)

		w.logger.Info("QBO entity changed",
			"realm_id", event.IntuitAccountID,
			"entity_type", entityType,
			"entity_id", event.IntuitEntityID,
			"operation", operation,
			"event_id", event.ID,
			"time", event.Time,
		)

		// 1. Construct the payload
		payload := CDCSyncPayload{
			EntityType: entityType,
			EntityID:   event.IntuitEntityID,
			Operation:  operation,
		}

		payloadBytes, jsonErr := json.Marshal(payload)
		if jsonErr != nil {
			w.logger.Error("Failed to marshal CDCSyncPayload", "error", jsonErr)
			continue
		}

		// 2. Construct the ERP Event
		erpEvent := ERPEvent{
			Type:    EventCDCSync,
			RealmID: event.IntuitAccountID,
			Payload: payloadBytes,
		}

		erpEventBytes, err := json.Marshal(erpEvent)
		if err != nil {
			w.logger.Error("Failed to marshal ERPEvent", "error", err)
			continue
		}

		// 3. Publish to NATS JetStream
		//    The NatsERPEventSubject comes from config, defaulting to toro.erp.events.*
		//    We'll publish specifically to .cdc
		subject := strings.Replace(w.manager.cfg.NatsERPEventSubject, "*", "cdc", 1)
		if !strings.Contains(subject, "cdc") { // Fallback if subject isn't a wildcard
			subject = subject + ".cdc"
		}

		// We use `w.q.JS` (JetStream context) instead of `nc.Publish` directly.
		if _, err := w.q.JetStream().Publish(subject, erpEventBytes); err != nil {
			w.logger.Error("Failed to publish CDC event to NATS",
				"realm_id", event.IntuitAccountID,
				"entity_type", entityType,
				"error", err,
			)
		} else {
			w.logger.Debug("Published CDC event to NATS", "subject", subject)
		}
	}

	msg.Ack()
}

// parseCloudEventType extracts entity type and operation from CloudEvents type field.
// Example: "qbo.account.created.v1" -> ("Account", "Create")
func parseCloudEventType(eventType string) (string, string) {
	// Split "qbo.account.created.v1" -> ["qbo", "account", "created", "v1"]
	parts := strings.Split(eventType, ".")
	if len(parts) < 3 {
		return "Unknown", "Unknown"
	}

	entity := parts[1]    // "account"
	operation := parts[2] // "created"

	// Capitalize first letter: "account" -> "Account"
	if len(entity) > 0 {
		entity = strings.ToUpper(entity[:1]) + entity[1:]
	}

	// Map CloudEvents operations to QBO operations
	switch operation {
	case "created":
		return entity, "Create"
	case "updated":
		return entity, "Update"
	case "deleted":
		return entity, "Delete"
	case "merged":
		return entity, "Merge"
	default:
		// Capitalize first letter of operation too
		if len(operation) > 0 {
			operation = strings.ToUpper(operation[:1]) + operation[1:]
		}
		return entity, operation
	}
}

// syncEntity triggers a sync for a specific QBO entity.
// Calls the QBOConnector to fetch from API and upsert to shadow DB.
// NOTE: Now used by the ERPEventWorker rather than directly from webhook.
func (w *Worker) syncEntity(ctx context.Context, realmID, entityType, entityID, operation string) error {
	// Get the QBO connector from the manager
	connector, ok := w.manager.connectors["qbo"]
	if !ok {
		return fmt.Errorf("QBO connector not found")
	}

	// Type assert to QBOConnector to access FetchEntity method
	qboConn, ok := connector.(*QBOConnector)
	if !ok {
		return fmt.Errorf("connector is not a QBOConnector")
	}

	return qboConn.FetchEntity(ctx, realmID, entityType, entityID, operation)
}
