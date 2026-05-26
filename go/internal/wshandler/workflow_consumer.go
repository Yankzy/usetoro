package wshandler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/jackc/pgx/v5/pgtype"
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

	c.logger.Info("📡 WS consumer: received workflow event", "entity_id", entityID, "status", payload["status"], "session_id", payload["session_id"], "realm_id", payload["realm_id"])

	// Strip the blueprint from real-time events — clients already received it
	// during blastActiveWorkflows on connect. Re-sending it on every state
	// transition floods the WebSocket with redundant KB-sized payloads.
	delete(payload, "blueprint")

	// Determine the WS message type. HITL events carry their own type so the
	// frontend can distinguish review/approval gates from generic status updates.
	wsMsgType := MessageTypeWorkflowStatus
	if status, _ := payload["status"].(string); status == "hitl_pending" {
		if mt, ok := payload["message_type"].(string); ok && mt != "" {
			wsMsgType = MessageType(mt)

			if mt == "bookkeeping_review" {
				if sessionID, ok := payload["session_id"].(string); ok && sessionID != "" {
					var pgSessionID pgtype.UUID
					if err := pgSessionID.Scan(sessionID); err == nil {
						// 1. Update DB to READY_FOR_REVIEW
						_ = c.queries.UpdateSessionTransactionsToReadyForReview(c.ctx, pgSessionID)

						// 2. Publish to proof.accounting.human_review
						envelope, err := core.NewEnvelope(
							uuid.New().String(),
							"did:toro:workflow-consumer",
							"",
							sessionID,
							core.INFORM,
							core.Proof{
								TaskID:    sessionID,
								Type:      core.ProofAPI,
								Timestamp: time.Now().Unix(),
							},
						)
						if err == nil {
							envBytes, _ := json.Marshal(envelope)
							_ = c.client.Conn().Publish("proof.accounting.human_review", envBytes)
						}
					}
				}
			}
		}
	}

	wsMsg, err := NewWorkflowStatusMessage(wsMsgType, payload)
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
	c.logger.Info("📡 WS consumer: broadcast queued", "room", roomID, "msg_type", string(wsMsgType))

	if err := msg.Ack(); err != nil {
		c.logger.Error("Failed to acknowledge workflow NATS message", "error", err)
	}
}
