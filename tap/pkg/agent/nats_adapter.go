package agent

import (
	"context"

	"github.com/nats-io/nats.go"
)

// NatsAdapter implements EventBus using concrete NATS connection and JetStream context.
type NatsAdapter struct {
	nc *nats.Conn
	js nats.JetStreamContext
}

// NewNatsAdapter creates a new adapter.
func NewNatsAdapter(nc *nats.Conn, js nats.JetStreamContext) *NatsAdapter {
	return &NatsAdapter{nc: nc, js: js}
}

func (n *NatsAdapter) Publish(subject string, data []byte) error {
	return n.nc.Publish(subject, data)
}

func (n *NatsAdapter) RequestWithContext(ctx context.Context, subject string, data []byte) (*nats.Msg, error) {
	return n.nc.RequestWithContext(ctx, subject, data)
}

func (n *NatsAdapter) QueueSubscribe(subj, queue string, cb nats.MsgHandler, opts ...nats.SubOpt) (*nats.Subscription, error) {
	// We use JetStream for subscription as per original requirement
	return n.js.QueueSubscribe(subj, queue, cb, opts...)
}
