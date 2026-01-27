// Package ingest handles event ingestion with circuit breaker protection
package ingest

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/sony/gobreaker"
	"github.com/stripe/stripe-go/v76"
)

// Publisher handles the ingestion of events into the system.
type Publisher struct {
	nc      *nats.Conn
	js      nats.JetStreamContext
	breaker *gobreaker.CircuitBreaker
}

// NewPublisher creates a new Publisher with circuit breaker protection.
func NewPublisher(nc *nats.Conn, js nats.JetStreamContext) *Publisher {
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
		nc:      nc,
		js:      js,
		breaker: breaker,
	}
}

// Ping checks the connection to NATS.
func (p *Publisher) Ping(ctx context.Context) error {
	if p.nc.Status() != nats.CONNECTED {
		return fmt.Errorf("nats not connected, status: %v", p.nc.Status())
	}
	return nil
}

// PublishStripeEvent publishes a raw Stripe webhook event to the NATS JetStream.
// It uses a circuit breaker to prevent cascading failures.
func (p *Publisher) PublishStripeEvent(ctx context.Context, connID, toroEventID string, event stripe.Event, body []byte) error {
	_, err := p.breaker.Execute(func() (interface{}, error) {
		msg := nats.NewMsg("raw.ingest.stripe")
		msg.Data = body
		msg.Header.Set("Toro-Conn-ID", connID)
		msg.Header.Set("Toro-Event-ID", toroEventID)
		msg.Header.Set("Stripe-Event-ID", event.ID)
		msg.Header.Set("Stripe-Event-Type", string(event.Type))
		msg.Header.Set("Timestamp", fmt.Sprintf("%d", time.Now().Unix()))

		// Add request ID from context if available
		if requestID, ok := ctx.Value("request_id").(string); ok {
			msg.Header.Set("Request-ID", requestID)
		}

		_, publishErr := p.js.PublishMsg(msg, nats.Context(ctx))
		return nil, publishErr
	})

	if err == gobreaker.ErrOpenState {
		return fmt.Errorf("circuit breaker open: NATS is unhealthy")
	}

	return err
}
