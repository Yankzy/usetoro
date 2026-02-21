package wshandler

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/nats-io/nats.go"
)

// QBOEventConsumer handles NATS subscription for QBO events
type QBOEventConsumer struct {
	client *queue.Client
	hub    *Hub
	logger *slog.Logger
	cfg    *config.Config
	sub    *nats.Subscription
	ctx    context.Context
	cancel context.CancelFunc
}

// NewQBOEventConsumer creates a new QBO event consumer
func NewQBOEventConsumer(client *queue.Client, hub *Hub, logger *slog.Logger, cfg *config.Config) *QBOEventConsumer {
	ctx, cancel := context.WithCancel(context.Background())
	return &QBOEventConsumer{
		client: client,
		hub:    hub,
		logger: logger,
		cfg:    cfg,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start begins consuming QBO connection events from NATS
func (c *QBOEventConsumer) Start() error {
	// Subscribe to QBO connected events (Config-driven)
	// Subscribe to QBO connected events (Config-driven)
	// We use the 'ws' service configuration
	wsConfig, ok := c.cfg.NATS.Services["ws"]
	subject := "qbo.events.connected" // Default fallback
	if ok && len(wsConfig.JetStream.Subjects) > 0 {
		subject = wsConfig.JetStream.Subjects[0]
	}

	sub, err := c.client.JetStream().Subscribe(
		subject,
		c.handleQBOConnectedEvent,
		nats.DeliverNew(),
		nats.AckExplicit(),
		nats.MaxDeliver(3),
	)

	if err != nil {
		return err
	}

	c.sub = sub
	c.logger.Info("QBO event consumer started", "subject", subject)
	return nil
}

// Stop gracefully stops the consumer
func (c *QBOEventConsumer) Stop() error {
	c.cancel()

	if c.sub != nil {
		if err := c.sub.Drain(); err != nil {
			c.logger.Error("Failed to drain subscription", "error", err)
			return err
		}
	}

	c.logger.Info("QBO event consumer stopped")
	return nil
}

// handleQBOConnectedEvent processes incoming QBO connection events
func (c *QBOEventConsumer) handleQBOConnectedEvent(msg *nats.Msg) {
	// Parse the NATS message
	var payload map[string]interface{}
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		c.logger.Error("Failed to unmarshal QBO event", "error", err)
		msg.Nak()
		return
	}

	// Extract realm_id
	realmID, ok := payload["realm_id"].(string)
	if !ok || realmID == "" {
		c.logger.Error("Missing or invalid realm_id in QBO event", "payload", payload)
		msg.Nak()
		return
	}

	c.logger.Info("Received QBO connected event",
		"realm_id", realmID,
		"type", payload["type"],
		"status", payload["status"],
	)

	// Create WebSocket message
	wsMsg, err := NewQBOConnectedMessage(realmID)
	if err != nil {
		c.logger.Error("Failed to create WebSocket message", "error", err)
		msg.Nak()
		return
	}

	// Broadcast to all connected WebSocket clients
	c.hub.Broadcast(wsMsg)
	c.logger.Info("Broadcast QBO connected to WebSocket clients",
		"realm_id", realmID,
		"clients", c.hub.ClientCount(),
	)

	// Acknowledge the NATS message
	if err := msg.Ack(); err != nil {
		c.logger.Error("Failed to acknowledge NATS message", "error", err)
	}
}
