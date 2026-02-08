package transport

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// InitStreams ensures the necessary JetStream streams exist.
// This makes the system idempotent.
func InitStreams(js nats.JetStreamContext) error {
	// 1. Tasks Stream (Work Queue)
	// Retains messages until they are acknowledged (Work Queue Policy)
	_, err := js.AddStream(&nats.StreamConfig{
		Name:      "TAP_TASKS",
		Subjects:  []string{"tasks.>"},
		Retention: nats.WorkQueuePolicy, // Key: Messages are removed when acked
		Storage:   nats.FileStorage,
	})
	if err != nil {
		return fmt.Errorf("failed to create tasks stream: %w", err)
	}

	// 2. Events Stream (Audit Log)
	// Retains history for 30 days (Limits Policy)
	_, err = js.AddStream(&nats.StreamConfig{
		Name:      "TAP_EVENTS",
		Subjects:  []string{"events.>"},
		Retention: nats.LimitsPolicy,
		MaxAge:    30 * 24 * time.Hour,
		Storage:   nats.FileStorage,
	})
	if err != nil {
		return fmt.Errorf("failed to create events stream: %w", err)
	}

	// 3. Contracts Stream (State Updates)
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     "TAP_CONTRACTS",
		Subjects: []string{"contracts.>"},
		Storage:  nats.FileStorage,
	})
	if err != nil {
		return fmt.Errorf("failed to create contracts stream: %w", err)
	}

	return nil
}
