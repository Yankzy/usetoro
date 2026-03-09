package queue

import (
	"fmt"
	"strings"
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

// Conn returns the underlying NATS connection
func (c *Client) Conn() *nats.Conn {
	return c.nc
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
	var info *nats.StreamInfo
	var err error

	// Retry loop for JetStream cluster leader election during startup
	for i := 0; i < 5; i++ {
		info, err = c.js.StreamInfo(cfg.Name)
		if err == nil || err == nats.ErrStreamNotFound {
			break
		}
		time.Sleep(2 * time.Second)
	}

	if err == nil {
		// Stream exists, update if needed
		// Check if subjects need to be updated
		if !containsAllSubjects(info.Config.Subjects, cfg.Subjects) {
			updateCfg := info.Config
			// merge subjects
			subjectMap := make(map[string]bool)
			for _, s := range updateCfg.Subjects {
				subjectMap[s] = true
			}
			for _, s := range cfg.Subjects {
				if !subjectMap[s] {
					updateCfg.Subjects = append(updateCfg.Subjects, s)
				}
			}

			_, err = c.js.UpdateStream(&updateCfg)
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
// It supports NATS wildcards (* and >) to determine if existing subjects cover the required ones.
func containsAllSubjects(existing, required []string) bool {
	for _, req := range required {
		covered := false
		for _, ex := range existing {
			if subjectIsCovered(req, ex) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

// subjectIsCovered returns true if the required subject is subsumed by the existing NATS pattern.
func subjectIsCovered(req, existing string) bool {
	if req == existing {
		return true
	}

	reqTokens := strings.Split(req, ".")
	exTokens := strings.Split(existing, ".")

	for i, exToken := range exTokens {
		// Existing subject ends with wide wildcard, covering everything from here
		if exToken == ">" {
			return true
		}

		// Existing subject requires more tokens than required subject provides
		if i >= len(reqTokens) {
			return false
		}

		// Direct match or single-level wildcard
		if exToken != "*" && exToken != reqTokens[i] {
			return false
		}
	}

	// Make sure they have the exact same number of tokens unless > matched early
	return len(reqTokens) == len(exTokens)
}
