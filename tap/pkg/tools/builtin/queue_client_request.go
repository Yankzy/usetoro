package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/jackc/pgx/v5/pgtype"
)

type QueueClientRequestTool struct {
	queries *database.Queries
}

func NewQueueClientRequestTool(queries *database.Queries) tools.Tool {
	return &QueueClientRequestTool{
		queries: queries,
	}
}

func (t *QueueClientRequestTool) Name() string {
	return "QueueClientRequest"
}

func (t *QueueClientRequestTool) Description() string {
	return "Queue a request to contact the client for missing context (e.g., a missing receipt or invoice). This does not send an email immediately; it aggregates the request with other requests for this client and sends a digest email at the end of the bank reconciliation session. Only use this if you have already used the CheckExistingDocuments tool and didn't find the required document."
}

func (t *QueueClientRequestTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"request_type": {
				"type": "string",
				"enum": ["Receipt", "Invoice", "Clarification"],
				"description": "The type of document or information requested."
			},
			"context": {
				"type": "string",
				"description": "A clear, concise, client-friendly explanation of why this document or information is needed for this specific transaction."
			}
		},
		"required": ["request_type", "context"]
	}`)
}

func (t *QueueClientRequestTool) Call(ctx context.Context, input map[string]any) (string, error) {
	requestType, ok := input["request_type"].(string)
	if !ok || requestType == "" {
		return "", fmt.Errorf("request_type is required")
	}

	reqContext, _ := input["context"].(string)

	sessionIDStr, ok := ctx.Value(tools.SessionIDKey{}).(string)
	if !ok || sessionIDStr == "" {
		return "", fmt.Errorf("SessionID not found in context")
	}

	nodeIDStr, ok := ctx.Value(tools.NodeIDKey{}).(string)
	if !ok || nodeIDStr == "" {
		return "", fmt.Errorf("NodeID not found in context (this represents the transaction ID)")
	}

	tenantIDStr, ok := ctx.Value(tools.EntityIDKey{}).(string) // TenantID is usually passed as EntityID in some contexts
	if !ok || tenantIDStr == "" {
		return "", fmt.Errorf("TenantID (EntityID) not found in context")
	}

	if t.queries == nil {
		return "", fmt.Errorf("database queries not initialized in tool")
	}

	var sessionUUID, nodeUUID, tenantUUID pgtype.UUID
	if err := sessionUUID.Scan(sessionIDStr); err != nil {
		return "", fmt.Errorf("invalid SessionID format")
	}
	if err := nodeUUID.Scan(nodeIDStr); err != nil {
		return "", fmt.Errorf("invalid NodeID format")
	}
	if err := tenantUUID.Scan(tenantIDStr); err != nil {
		return "", fmt.Errorf("invalid TenantID format")
	}

	err := t.queries.InsertClientRequestOutbox(ctx, database.InsertClientRequestOutboxParams{
		TransactionID: nodeUUID,
		SessionID:     sessionUUID,
		ClientID:      tenantUUID,
		RequestType:   requestType,
		Context:       pgtype.Text{String: reqContext, Valid: reqContext != ""},
		DagNodeID:     "missing_context_triage", // Ideally dynamic, but this suffices for the Mailroom
		Status:        "QUEUED",
	})

	if err != nil {
		return "", fmt.Errorf("failed to queue client request: %w", err)
	}

	return "Request successfully queued for the client digest email.", nil
}

func init() {
	Register("QueueClientRequest", func(env core.Environment, logger *slog.Logger) tools.Tool {
		return NewQueueClientRequestTool(env.Queries)
	})
}
