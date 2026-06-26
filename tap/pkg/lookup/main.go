package lookup

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// AlmanacQuery defines the search criteria
type AlmanacQuery struct {
	CallerDID      string                 `json:"caller_did,omitempty"`      // Added for billing the Micrion Toll
	CapabilityType string                 `json:"capability_type,omitempty"` // e.g., "logistics.trucking"
	DID            string                 `json:"did,omitempty"`             // Direct lookup by DID
	MetaFilter     map[string]interface{} `json:"meta_filter,omitempty"`     // e.g., {"location": "New York"}
	MinReputation  int                    `json:"min_rep,omitempty"`         // e.g., 3
}

// AlmanacEntry represents a discovered agent
type AlmanacEntry struct {
	DID          string                   `json:"did"`
	Endpoints    []string                 `json:"endpoints"` // NATS Subjects
	Capabilities []RegistrationCapability `json:"capabilities"`
}

// RegistrationCapability defines a capability in the registration payload
type RegistrationCapability struct {
	Type string                 `json:"type"`
	Meta map[string]interface{} `json:"meta,omitempty"`
}

// RegistrationPayload matches the Schema for registering an agent
type RegistrationPayload struct {
	DID            string                   `json:"did"`
	Endpoints      []string                 `json:"endpoints"`
	Capabilities   []RegistrationCapability `json:"capabilities"`
	Expiry         time.Time                `json:"expiry"`
	Signature      string                   `json:"signature"`       // Ed25519 signature of (DID + Endpoints + Caps)
	CertificatePEM string                   `json:"certificate_pem"` // PEM encoded certificate

}

// Client handles discovery operations
type Client struct {
	nc        *nats.Conn
	callerDID string
}

func New(nc *nats.Conn, callerDID string) *Client {
	return &Client{nc: nc, callerDID: callerDID}
}

// FindAgents performs a synchronous NATS Request to find agents.
func (c *Client) FindAgents(capability string, timeout time.Duration) ([]AlmanacEntry, error) {
	query := AlmanacQuery{
		CallerDID:      c.callerDID,
		CapabilityType: capability,
	}

	reqData, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("marshal error: %w", err)
	}

	// Send to "almanac.query" and wait for response
	msg, err := c.requestIgnorePubAck(core.SubjectAlmanacQuery, reqData, timeout)
	if err != nil {
		return nil, fmt.Errorf("almanac request failed: %w", err)
	}

	var results []AlmanacEntry
	if err := json.Unmarshal(msg.Data, &results); err != nil {
		return nil, fmt.Errorf("unmarshal error: %w", err)
	}

	return results, nil
}

// Resolve looks up agents by multiple criteria
func (c *Client) Resolve(query AlmanacQuery, timeout time.Duration) (*AlmanacEntry, error) {
	query.CallerDID = c.callerDID
	reqData, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("marshal error: %w", err)
	}

	msg, err := c.requestIgnorePubAck(core.SubjectAlmanacQuery, reqData, timeout)
	if err != nil {
		return nil, fmt.Errorf("almanac request failed: %w", err)
	}

	var entries []AlmanacEntry
	if err := json.Unmarshal(msg.Data, &entries); err != nil {
		return nil, fmt.Errorf("unmarshal error: %w", err)
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("agent not found: %s", query.DID)
	}

	return &entries[0], nil
}

// ResolveByDID looks up a specific agent by DID
func (c *Client) ResolveByDID(did string, timeout time.Duration) (*AlmanacEntry, error) {
	query := AlmanacQuery{
		CallerDID: c.callerDID,
		DID:       did,
	}
	reqData, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("marshal error: %w", err)
	}

	msg, err := c.requestIgnorePubAck(core.SubjectAlmanacQuery, reqData, timeout)
	if err != nil {
		return nil, fmt.Errorf("almanac request failed: %w", err)
	}

	var entries []AlmanacEntry
	if err := json.Unmarshal(msg.Data, &entries); err != nil {
		return nil, fmt.Errorf("unmarshal error: %w", err)
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("agent not found: %s", query.DID)
	}

	return &entries[0], nil
}

// ResolveByCapabilityType looks up agents by capability type
func (c *Client) ResolveByCapabilityType(capability string, timeout time.Duration) ([]AlmanacEntry, error) {
	query := AlmanacQuery{
		CallerDID:      c.callerDID,
		CapabilityType: capability,
	}
	reqData, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("marshal error: %w", err)
	}

	msg, err := c.requestIgnorePubAck(core.SubjectAlmanacQuery, reqData, timeout)
	if err != nil {
		return nil, fmt.Errorf("almanac request failed: %w", err)
	}

	var results []AlmanacEntry
	if err := json.Unmarshal(msg.Data, &results); err != nil {
		return nil, fmt.Errorf("unmarshal error: %w", err)
	}

	return results, nil
}

// requestIgnorePubAck performs a synchronous NATS Request but ignores JetStream PubAcks.
func (c *Client) requestIgnorePubAck(subj string, data []byte, timeout time.Duration) (*nats.Msg, error) {
	inbox := nats.NewInbox()
	sub, err := c.nc.SubscribeSync(inbox)
	if err != nil {
		return nil, fmt.Errorf("subscribe failed: %w", err)
	}
	defer sub.Unsubscribe()

	if err := c.nc.PublishRequest(subj, inbox, data); err != nil {
		return nil, fmt.Errorf("publish request failed: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		msg, err := sub.NextMsg(time.Until(deadline))
		if err != nil {
			break
		}

		// Try unmarshaling to see if it's a valid JetStream PubAck.
		var generic map[string]interface{}
		if err := json.Unmarshal(msg.Data, &generic); err == nil {
			if _, ok := generic["stream"]; ok {
				if _, ok := generic["seq"]; ok {
					// It's a PubAck, skip it
					continue
				}
			}
		}

		// Not a PubAck, return the message
		return msg, nil
	}

	return nil, nats.ErrTimeout
}
