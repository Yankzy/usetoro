package wshandler

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/nats-io/nats.go"
)

// StripeEventConsumer handles NATS subscription for Stripe events
type StripeEventConsumer struct {
	client *queue.Client
	hub    *Hub
	logger *slog.Logger
	cfg    *config.Config
	sub    *nats.Subscription
	ctx    context.Context
	cancel context.CancelFunc
}

// NewStripeEventConsumer creates a new Stripe event consumer
func NewStripeEventConsumer(client *queue.Client, hub *Hub, logger *slog.Logger, cfg *config.Config) *StripeEventConsumer {
	ctx, cancel := context.WithCancel(context.Background())
	return &StripeEventConsumer{
		client: client,
		hub:    hub,
		logger: logger,
		cfg:    cfg,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start begins consuming stripe events from NATS
func (c *StripeEventConsumer) Start() error {
	subject := "stripe_fc_connected"

	// Subscribe to the simple NATS topic
	sub, err := c.client.Conn().Subscribe(subject, c.handleStripeEvent)
	if err != nil {
		return err
	}

	c.sub = sub
	c.logger.Info("Stripe event consumer started", "subject", subject)
	return nil
}

// Stop gracefully stops the consumer
func (c *StripeEventConsumer) Stop() error {
	c.cancel()
	if c.sub != nil {
		return c.sub.Unsubscribe()
	}
	return nil
}

// handleStripeEvent processes incoming stripe events and broadcasts to the correct room.
func (c *StripeEventConsumer) handleStripeEvent(msg *nats.Msg) {
	var payload map[string]interface{}
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		c.logger.Error("Failed to unmarshal stripe event", "error", err)
		return
	}

	entityID, ok := payload["entity_id"].(string)
	if !ok || entityID == "" {
		c.logger.Warn("Received stripe event without entity_id, ignoring", "msg", string(msg.Data))
		return
	}

	c.logger.Info("📡 WS consumer: received stripe event", "entity_id", entityID, "session_id", payload["session_id"])

	wsMsg := Message{
		Type: MessageTypeStripeFCConnected,
		Data: map[string]interface{}{
			"session_id": payload["session_id"],
		},
	}

	wsMsgBytes, err := json.Marshal(wsMsg)
	if err != nil {
		c.logger.Error("Failed to marshal stripe WS message", "error", err)
		return
	}

	// Targeted broadcast to the specific room (entityID)
	c.hub.BroadcastToRoom(entityID, wsMsgBytes)
	c.logger.Info("📡 WS consumer: broadcast queued for stripe event", "room", entityID)
}
