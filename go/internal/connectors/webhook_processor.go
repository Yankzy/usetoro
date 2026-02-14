package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nats-io/nats.go"
)

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

		err := w.syncEntity(context.Background(), event.IntuitAccountID, entityType, event.IntuitEntityID, operation)
		if err != nil {
			w.logger.Error("Failed to sync entity",
				"realm_id", event.IntuitAccountID,
				"entity_type", entityType,
				"entity_id", event.IntuitEntityID,
				"error", err,
			)
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

	// Call FetchEntity to:
	// 1. Get QBO tokens from database
	// 2. Initialize QBO client with auto-refresh
	// 3. Fetch entity data from QBO API
	// 4. Upsert to shadow DB (or soft delete)
	return qboConn.FetchEntity(ctx, realmID, entityType, entityID, operation)
}
