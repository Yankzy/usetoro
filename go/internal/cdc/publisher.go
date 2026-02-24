package cdc

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type Publisher struct {
	js jetstream.JetStream
}

func NewPublisher(js jetstream.JetStream) *Publisher {
	return &Publisher{
		js: js,
	}
}

// PublishSync publishes the Toro Event synchronously and guarantees Exactly-Once Semantics via Nats-Msg-Id.
func (p *Publisher) PublishSync(ctx context.Context, event *Event) error {
	// Promote event_source from the row data into the top-level Source field so
	// consumers can call event.IsInternal() without digging into Data.
	if src, ok := event.Data["event_source"].(string); ok {
		event.Source = src
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event payload: %w", err)
	}

	// Subject structure: ledger.<table_name>.<action>
	subject := fmt.Sprintf("ledger.%s.%s", event.Table, strings.ToLower(event.Action))

	msg := &nats.Msg{
		Subject: subject,
		Data:    payload,
		Header: nats.Header{
			// Nats-Msg-Id is NATS built-in deduplication ID. Providing the exact Postgres LSN prevents
			// duplicates during network partitions or crashes.
			nats.MsgIdHdr: []string{event.EventID},
		},
	}

	// Synchronous wait for JetStream Acknowledgment
	// (critical to not advance Postgres LSN until this completely returns).
	ack, err := p.js.PublishMsg(ctx, msg)
	if err != nil {
		return fmt.Errorf("nats jetstream synchronous publish failed: %w", err)
	}

	log.Printf("Successfully published to subject: %s, Sequence: %d, EventID(LSN): %s", subject, ack.Sequence, event.EventID)
	return nil
}
