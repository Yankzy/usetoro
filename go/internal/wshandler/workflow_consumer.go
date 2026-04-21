package wshandler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/queue"
)

// WorkflowEventConsumer handles NATS subscription for Workflow status events
type WorkflowEventConsumer struct {
	client  *queue.Client
	hub     *Hub
	logger  *slog.Logger
	cfg     *config.Config
	sub     *nats.Subscription
	ctx     context.Context
	cancel  context.CancelFunc
	queries *database.Queries
}

// NewWorkflowEventConsumer creates a new Workflow event consumer
func NewWorkflowEventConsumer(client *queue.Client, hub *Hub, logger *slog.Logger, cfg *config.Config, queries *database.Queries) *WorkflowEventConsumer {
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkflowEventConsumer{
		client:  client,
		hub:     hub,
		logger:  logger,
		cfg:     cfg,
		ctx:     ctx,
		cancel:  cancel,
		queries: queries,
	}
}

// Start begins consuming workflow events from NATS
func (c *WorkflowEventConsumer) Start() error {
	// Subscribe to workflow status events (started, updated, completed)
	subject := "workflow.events.>"

	// Using the same JetStream pattern as QBO
	sub, err := c.client.JetStream().Subscribe(
		subject,
		c.handleWorkflowStatusEvent,
		nats.BindStream("WORKFLOWS"), // Explicitly bind exactly to WORKFLOWS stream
		nats.DeliverNew(),
		nats.AckExplicit(),
	)

	if err != nil {
		return fmt.Errorf("failed to subscribe to %s on WORKFLOWS: %w", subject, err)
	}

	c.sub = sub
	c.logger.Info("Workflow event consumer started", "subject", subject)
	return nil
}

// Stop gracefully stops the consumer
func (c *WorkflowEventConsumer) Stop() error {
	c.cancel()
	if c.sub != nil {
		return c.sub.Drain()
	}
	return nil
}

// handleWorkflowStatusEvent processes incoming workflow status events and broadcasts to the correct room.
func (c *WorkflowEventConsumer) handleWorkflowStatusEvent(msg *nats.Msg) {
	var payload map[string]interface{}
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		c.logger.Error("Failed to unmarshal workflow event", "error", err)
		msg.Nak()
		return
	}

	// Extract entity_id for targeted room broadcast
	entityID, ok := payload["entity_id"].(string)
	if !ok || entityID == "" {
		c.logger.Warn("Received workflow event without entity_id, ignoring to prevent leak", "msg", string(msg.Data))
		msg.Ack()
		return
	}

	c.logger.Debug("Received workflow status event", "entity_id", entityID, "status", payload["status"])

	// Create and broadcast WS message
	wsMsg, err := NewWorkflowStatusMessage(payload)
	if err != nil {
		c.logger.Error("Failed to create workflow WS message", "error", err)
		msg.Nak()
		return
	}

	sessionID, _ := payload["session_id"].(string)
	roomID := entityID
	if rid, ok := payload["realm_id"].(string); ok && rid != "" {
		roomID = rid
	} else if sessionID != "" {
		roomID = sessionID
	}
	// Targeted broadcast to the specific room (realm preferred, then session, then entity)
	c.hub.BroadcastToRoom(roomID, wsMsg)

	if status, ok := payload["status"].(string); ok && status == "ambiguous" {
		if c.queries != nil {
			if sessionID == "" {
				c.logger.Warn("Ambiguity event missing session_id; cannot update session row", "entity_id", entityID)
			} else if sid, err := uuid.Parse(sessionID); err == nil {
				reason := ""
				if r, ok := payload["suspension_reason"].(string); ok {
					reason = r
				}
				err := c.queries.MarkCleanupSessionAmbiguous(context.Background(), database.MarkCleanupSessionAmbiguousParams{
					ID:              pgtype.UUID{Bytes: sid, Valid: true},
					IsAmbiguous:     true,
					AmbiguityReason: pgtype.Text{String: reason, Valid: reason != ""},
				})
				if err != nil {
					c.logger.Error("Failed to mark cleanup session ambiguous", "error", err, "session", sessionID)
				}
			} else {
				c.logger.Error("Invalid session ID in ambiguity event", "session_id", sessionID, "error", err)
			}
		}
	}

	if err := msg.Ack(); err != nil {
		c.logger.Error("Failed to acknowledge workflow NATS message", "error", err)
	}
}
