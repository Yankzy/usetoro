package queue

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

type Client struct {
	nc *nats.Conn
	js nats.JetStreamContext
}

func NewClient(url string, opts ...nats.Option) (*Client, error) {
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to nats: %w", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to init jetstream: %w", err)
	}

	return &Client{nc: nc, js: js}, nil
}

func (c *Client) Close() {
	if c.nc != nil {
		c.nc.Close()
	}
}

func (c *Client) JetStream() nats.JetStreamContext {
	return c.js
}

func (c *Client) Request(subject string, payload []byte, timeout time.Duration) ([]byte, error) {
	msg, err := c.nc.Request(subject, payload, timeout)
	if err != nil {
		return nil, fmt.Errorf("nats request failed: %w", err)
	}
	return msg.Data, nil
}

// Publish sends a message to JetStream and waits for an acknowledgement (At-Least-Once).
// For banking infrastructure, this is the safe default.
func (c *Client) Publish(subject string, payload []byte) error {
	_, err := c.js.Publish(subject, payload)
	if err != nil {
		return fmt.Errorf("jetstream publish failed: %w", err)
	}
	return nil
}

// PublishCore sends a message via core NATS (Fire-and-Forget / At-Most-Once).
// Use this for high-frequency, non-critical data (e.g. ephemeral metrics, logs).
func (c *Client) PublishCore(subject string, payload []byte) error {
	if err := c.nc.Publish(subject, payload); err != nil {
		return fmt.Errorf("nats core publish failed: %w", err)
	}
	return nil
}

func (c *Client) PublishMsg(msg *nats.Msg, opts ...nats.PubOpt) (*nats.PubAck, error) {
	return c.js.PublishMsg(msg, opts...)
}

func (c *Client) Status() nats.Status {
	return c.nc.Status()
}

// EnsureStream checks if a stream exists and creates/updates it as needed.
func (c *Client) EnsureStream(cfg *nats.StreamConfig) error {
	info, err := c.js.StreamInfo(cfg.Name)
	if err == nil {
		// Stream exists, update if needed
		// Check if subjects need to be updated
		if !containsAllSubjects(info.Config.Subjects, cfg.Subjects) {
			_, err = c.js.UpdateStream(cfg)
			if err != nil {
				return fmt.Errorf("failed to update stream: %w", err)
			}
		}
		return nil
	}

	if err != nats.ErrStreamNotFound {
		return fmt.Errorf("failed to check stream status: %w", err)
	}

	_, err = c.js.AddStream(cfg)
	if err != nil {
		return fmt.Errorf("failed to add stream: %w", err)
	}

	return nil
}

// containsAllSubjects checks if all required subjects are present in the stream config.
func containsAllSubjects(existing, required []string) bool {
	subjectMap := make(map[string]bool)
	for _, s := range existing {
		subjectMap[s] = true
	}
	for _, s := range required {
		if !subjectMap[s] {
			return false
		}
	}
	return true
}
