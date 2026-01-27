package agent

import (
	"context"
	"log/slog"

	"github.com/nats-io/nats.go"
)

type Agent struct {
	logger *slog.Logger
	js     nats.JetStreamContext
}

func NewAgent(logger *slog.Logger, js nats.JetStreamContext) *Agent {
	return &Agent{
		logger: logger,
		js:     js,
	}
}

// Start begins listening for events
func (a *Agent) Start(ctx context.Context) error {
	a.logger.Info("🧠 Agent starting...")

	// Subscribe to the raw ingest subject
	sub, err := a.js.Subscribe("raw.ingest.stripe", func(msg *nats.Msg) {
		a.processMessage(msg)
	}, nats.Durable("toro-protocol-consumer"), nats.ManualAck())

	if err != nil {
		return err
	}

	a.logger.Info("🧠 Agent listening on raw.ingest.stripe")

	<-ctx.Done()
	return sub.Unsubscribe()
}

func (a *Agent) processMessage(msg *nats.Msg) {
	meta, err := msg.Metadata()
	if err != nil {
		a.logger.Error("Failed to get metadata", "error", err)
		msg.Nak()
		return
	}

	a.logger.Info("🧠 Received Event",
		"seq", meta.Sequence.Stream,
		"subject", msg.Subject,
	)

	// TODO: Call Python Sidecar via gRPC here
	// TODO: Call OpenAI here

	msg.Ack()
}
