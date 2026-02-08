package transport

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// JetStream returns the JetStream context for the connection.
func JetStream(nc *nats.Conn) (nats.JetStreamContext, error) {
	return nc.JetStream()
}

// Request sends a request and waits for a reply.
// This is a wrapper around nats.Conn.Request but returns just the data or error.
func Request(nc *nats.Conn, subject string, payload []byte, timeout time.Duration) ([]byte, error) {
	msg, err := nc.Request(subject, payload, timeout)
	if err != nil {
		return nil, fmt.Errorf("nats request failed: %w", err)
	}
	return msg.Data, nil
}
