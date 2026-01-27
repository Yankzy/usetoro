package ingest

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stripe/stripe-go/v76"
)

// Publisher handles the ingestion of events into the system.
type Publisher struct {
	js nats.JetStreamContext
}

// NewPublisher creates a new Publisher.
func NewPublisher(js nats.JetStreamContext) *Publisher {
	return &Publisher{js: js}
}

// PublishStripeEvent publishes a raw Stripe webhook event to the NATS JetStream.
func (p *Publisher) PublishStripeEvent(ctx context.Context, connID, toroEventID string, event stripe.Event, body []byte) error {
	msg := nats.NewMsg("raw.ingest.stripe")
	msg.Data = body
	msg.Header.Set("Toro-Conn-ID", connID)
	msg.Header.Set("Toro-Event-ID", toroEventID)
	msg.Header.Set("Stripe-Event-ID", event.ID)
	msg.Header.Set("Stripe-Event-Type", string(event.Type))
	msg.Header.Set("Timestamp", fmt.Sprintf("%d", time.Now().Unix()))

	_, err := p.js.PublishMsg(msg, nats.Context(ctx))
	return err
}
