package resolver

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// AlmanacQuery defines the search criteria
type AlmanacQuery struct {
	CapabilityType string                 `json:"capability_type"` // e.g., "logistics.trucking"
	MetaFilter     map[string]interface{} `json:"meta_filter,omitempty"`
	MinReputation  int                    `json:"min_rep,omitempty"`
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
	DID          string                   `json:"did"`
	Endpoints    []string                 `json:"endpoints"`
	Capabilities []RegistrationCapability `json:"capabilities"`
	Expiry       time.Time                `json:"expiry"`
	Signature    string                   `json:"signature"` // Ed25519 signature of (DID + Endpoints + Caps)
}

// Client handles discovery operations
type Client struct {
	nc *nats.Conn
}

func New(nc *nats.Conn) *Client {
	return &Client{nc: nc}
}

// FindAgents performs a synchronous NATS Request to find agents.
func (c *Client) FindAgents(capability string, timeout time.Duration) ([]AlmanacEntry, error) {
	query := AlmanacQuery{CapabilityType: capability}

	reqData, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("marshal error: %w", err)
	}

	// Send to "almanac.query" and wait for response
	msg, err := c.nc.Request("almanac.query", reqData, timeout)
	if err != nil {
		return nil, fmt.Errorf("almanac request failed: %w", err)
	}

	var results []AlmanacEntry
	if err := json.Unmarshal(msg.Data, &results); err != nil {
		return nil, fmt.Errorf("unmarshal error: %w", err)
	}

	return results, nil
}
