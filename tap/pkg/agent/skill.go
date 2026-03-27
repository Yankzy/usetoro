package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/identity"
	"github.com/Yankzy/usetoro/tap/pkg/lookup"
	"github.com/nats-io/nats.go"
)

// Skill represents a remote capability we can call
type Skill struct {
	DID    string
	Topic  string // Resolved from DID
	Client *nats.Conn
}

// Use resolves a DID to a Skill struct
// This is the "Import" function
// Use skill by DID
func Use(nc *nats.Conn, callerDID string, targetDID string) (*Skill, error) {
	// 1. Resolve DID to NATS Subject (Discovery) via Almanac
	client := lookup.New(nc, callerDID)
	// 5 second timeout for discovery
	entry, err := client.ResolveByDID(targetDID, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("skill resolution failed: %w", err)
	}

	if len(entry.Endpoints) == 0 {
		return nil, fmt.Errorf("skill %s has no endpoints", targetDID)
	}

	return &Skill{
		DID:    targetDID,
		Topic:  entry.Endpoints[0], // Use the first endpoint
		Client: nc,
	}, nil
}

// Send dispatches a signed Envelope to the skill (Asynchronous Pattern)
// It returns the ConversationID so the caller can track the flow.
func (s *Skill) Send(ctx context.Context, senderDID string, kp *identity.KeyPair, perf core.Performative, payload interface{}) (string, error) {
	// 1. Create Envelope
	// ID is random, ConversationID starts as ID for new conversations
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	cid := msgID

	env, err := core.NewEnvelope(
		msgID,
		senderDID,
		s.DID, // Receiver is the Skill's DID
		cid,
		perf,
		payload,
	)
	if err != nil {
		return "", fmt.Errorf("failed to create envelope: %w", err)
	}

	// 2. Sign Envelope
	env.Signature = kp.Sign(env.Body)

	// 3. Serialize
	data, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("failed to marshal envelope: %w", err)
	}

	// 4. Publish to NATS (Fire and Forget)
	// We use the Topic resolved from Almanac (should be the agent's inbox or listening subject)
	if err := s.Client.Publish(s.Topic, data); err != nil {
		return "", fmt.Errorf("failed to publish envelope: %w", err)
	}

	return cid, nil
}
