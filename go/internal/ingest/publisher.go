// Package ingest handles event ingestion with circuit breaker protection
package ingest

import (
	"context"
	"fmt"
	"time"

	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/nats-io/nats.go"
	"github.com/sony/gobreaker"
)

// Publisher handles the ingestion of events into the system.
type Publisher struct {
	q       *queue.Client
	breaker *gobreaker.CircuitBreaker
}

// NewPublisher creates a new Publisher with circuit breaker protection.
func NewPublisher(q *queue.Client) *Publisher {
	breaker := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "nats-publisher",
		MaxRequests: 3,
		Interval:    10 * time.Second,
		Timeout:     60 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
			return counts.Requests >= 3 && failureRatio >= 0.6
		},
	})

	return &Publisher{
		q:       q,
		breaker: breaker,
	}
}

// Ping checks the connection to NATS.
func (p *Publisher) Ping(ctx context.Context) error {
	if p.q.Status() != nats.CONNECTED {
		return fmt.Errorf("nats not connected, status: %v", p.q.Status())
	}
	return nil
}

// PublishWebhookEvent publishes a webhook event to the NATS JetStream.
// It uses a circuit breaker to prevent cascading failures.
// The subject is dynamically constructed based on provider:
// - QBO: "qbo_webhook" (dedicated subject for sync service)
// - Others: "raw.ingest.{provider}"
func (p *Publisher) PublishWebhookEvent(ctx context.Context, provider, connID, toroEventID, providerEventID, providerEventType string, body []byte) error {
	_, err := p.breaker.Execute(func() (interface{}, error) {
		// Route QBO webhooks to dedicated subject for sync service
		var subject string
		if provider == "qbo" {
			subject = "qbo_webhook"
		} else {
			subject = fmt.Sprintf("raw.ingest.%s", provider)
		}

		msg := nats.NewMsg(subject)
		msg.Data = body
		msg.Header.Set("Toro-Conn-ID", connID)
		msg.Header.Set("Toro-Event-ID", toroEventID)
		msg.Header.Set("Provider", provider)
		msg.Header.Set("Provider-Event-ID", providerEventID)
		msg.Header.Set("Provider-Event-Type", providerEventType)
		msg.Header.Set("Timestamp", fmt.Sprintf("%d", time.Now().Unix()))

		// Add request ID from context if available
		if requestID, ok := ctx.Value("request_id").(string); ok {
			msg.Header.Set("Request-ID", requestID)
		}

		_, publishErr := p.q.PublishMsg(msg, nats.Context(ctx))
		return nil, publishErr
	})

	if err == gobreaker.ErrOpenState {
		return fmt.Errorf("circuit breaker open: NATS is unhealthy")
	}

	return err
}

// PublishStripeEvent is a backward-compatible wrapper around PublishWebhookEvent.
// Deprecated: Use PublishWebhookEvent instead.
func (p *Publisher) PublishStripeEvent(ctx context.Context, connID, toroEventID, eventID, eventType string, body []byte) error {
	return p.PublishWebhookEvent(ctx, "stripe", connID, toroEventID, eventID, eventType, body)
}

// PublishQBOEvent publishes a QuickBooks Online event to NATS.
// This is used to notify WebSocket clients about QBO authentication success.
func (p *Publisher) PublishQBOEvent(ctx context.Context, eventType, realmID string, data []byte) error {
	_, err := p.breaker.Execute(func() (interface{}, error) {
		subject := fmt.Sprintf("qbo.events.%s", eventType)
		msg := nats.NewMsg(subject)
		msg.Data = data
		msg.Header.Set("Realm-ID", realmID)
		msg.Header.Set("Event-Type", eventType)
		msg.Header.Set("Timestamp", fmt.Sprintf("%d", time.Now().Unix()))

		// Add request ID from context if available
		if requestID, ok := ctx.Value("request_id").(string); ok {
			msg.Header.Set("Request-ID", requestID)
		}

		_, publishErr := p.q.PublishMsg(msg, nats.Context(ctx))
		return nil, publishErr
	})

	if err == gobreaker.ErrOpenState {
		return fmt.Errorf("circuit breaker open: NATS is unhealthy")
	}

	return err
}
