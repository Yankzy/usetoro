package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/transport"
	"github.com/nats-io/nats.go"
)

type Agent struct {
	logger *slog.Logger
	js     nats.JetStreamContext
	nc     *nats.Conn
}

func NewAgent(logger *slog.Logger, js nats.JetStreamContext, nc *nats.Conn) *Agent {
	return &Agent{
		logger: logger,
		js:     js,
		nc:     nc,
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

	// Call request to python worker
	resp, err := transport.Request(a.nc, "skill.ocr", msg.Data, 5*time.Second) // 5s timeout
	if err != nil {
		a.logger.Error("Failed to call skill.ocr", "error", err)
		// We might want to nak or term depending on error, for now log
		return
	}

	a.logger.Info("Received response from skill.ocr", "response_len", len(resp))
	msg.Ack()
}
