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
		if needsStreamUpdate(&info.Config, cfg) {
			updateCfg := info.Config
			updateCfg.Subjects = cfg.Subjects
			updateCfg.MaxAge = cfg.MaxAge
			updateCfg.AllowMsgTTL = cfg.AllowMsgTTL
			updateCfg.AllowRollup = cfg.AllowRollup
			updateCfg.DenyDelete = cfg.DenyDelete
			updateCfg.DenyPurge = cfg.DenyPurge
			updateCfg.AllowDirect = cfg.AllowDirect

			_, err = c.js.UpdateStream(&updateCfg)
			if err != nil {
				return fmt.Errorf("failed to update stream %q: %w", cfg.Name, err)
			}
		}
		return nil
	}

	if err != nats.ErrStreamNotFound {
		return fmt.Errorf("failed to check stream status: %w", err)
	}

	var addErr error
	for i := 0; i < 5; i++ {
		_, addErr = c.js.AddStream(cfg)
		if addErr == nil {
			return nil
		}
		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("failed to add stream after retries: %w", addErr)
}

// EnsureStreamExists creates a stream if it does not exist, but will not reconcile/update its subjects.
// This is useful for streams whose subjects are managed dynamically at runtime (e.g. by another component).
func (c *Client) EnsureStreamExists(cfg *nats.StreamConfig) error {
	_, err := c.js.StreamInfo(cfg.Name)
	if err == nil {
		return nil
	}
	if err != nats.ErrStreamNotFound {
		return fmt.Errorf("failed to check stream status: %w", err)
	}

	var addErr error
	for i := 0; i < 5; i++ {
		_, addErr = c.js.AddStream(cfg)
		if addErr == nil {
			return nil
		}
		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("failed to add stream after retries: %w", addErr)
}

// CleanupOrphanedStreams deletes any stream in the NATS server that is not listed in validStreamNames.
// This prevents legacy/renamed streams from causing 'subject overlap' errors on startup.
func (c *Client) CleanupOrphanedStreams(validStreamNames []string) error {
	validMap := make(map[string]bool)
	for _, name := range validStreamNames {
		validMap[name] = true
	}

	for streamName := range c.js.StreamNames() {
		if !validMap[streamName] {
			if err := c.js.DeleteStream(streamName); err != nil {
				return fmt.Errorf("failed to delete orphaned stream %q: %w", streamName, err)
			}
		}
	}
	return nil
}

// WaitForStreamsReady aggressively blocks until the provided streams report as healthy
// within the JetStream Raft cluster. This prevents local `stream not found` consumer crashes
// that occur immediately after an `AddStream` if the nodes haven't finished electing.
func (c *Client) WaitForStreamsReady(streamNames []string) error {
	for _, streamName := range streamNames {
		ready := false
		for i := 0; i < 15; i++ { // Allow up to 30s for clustering to stabilize locally
			info, err := c.js.StreamInfo(streamName)
			if err == nil && info != nil && info.Config.Name == streamName {
				ready = true
				break
			}
			time.Sleep(2 * time.Second)
		}
		if !ready {
			return fmt.Errorf("stream %q failed to stabilize its cluster leader in time", streamName)
		}
	}
	return nil
}

// subjectsExactMatch checks if two arrays of subjects contain the exact same elements (regardless of order).
func subjectsExactMatch(existing, required []string) bool {
	if len(existing) != len(required) {
		return false
	}

	existingMap := make(map[string]bool)
	for _, req := range existing {
		existingMap[req] = true
	}

	for _, req := range required {
		if !existingMap[req] {
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

// needsStreamUpdate returns true if the existing stream configuration differs from the required configuration
// for any updateable fields.
func needsStreamUpdate(existing, required *nats.StreamConfig) bool {
	return !subjectsExactMatch(existing.Subjects, required.Subjects) ||
		existing.MaxAge != required.MaxAge ||
		existing.AllowMsgTTL != required.AllowMsgTTL ||
		existing.AllowRollup != required.AllowRollup ||
		existing.DenyDelete != required.DenyDelete ||
		existing.DenyPurge != required.DenyPurge ||
		existing.AllowDirect != required.AllowDirect
}

