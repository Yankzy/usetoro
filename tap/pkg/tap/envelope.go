package tap

import (
	"encoding/json"
	"time"
)

// Envelope is the atomic unit of communication in TAP.
// It is strictly flat JSON to ensure high-speed parsing.
type Envelope struct {
	// ID is a UUID v4 ensuring idempotency.
	ID string `json:"id"`

	// Timestamp in RFC3339 format (Creation time).
	Timestamp time.Time `json:"ts"`

	// Sender and Receiver DIDs (did:toro:...).
	SenderDID   string `json:"src"`
	ReceiverDID string `json:"dst,omitempty"`

	// Performative indicates the intent (from verbs.go).
	Performative Performative `json:"perf"`

	// ConversationID links messages into a single thread/negotiation.
	// Critical for matching PROPOSE to CFP.
	ConversationID string `json:"cid,omitempty"`

	// Body is the raw logic/data. We use json.RawMessage to delay parsing
	// until the receiver knows what domain this is (Task, Contract, Proof).
	Body json.RawMessage `json:"body"`

	// Signature is the Ed25519 signature of the canonicalized payload.
	// Verifies: ID + Ts + Src + Dst + Perf + Cid + Body
	Signature string `json:"sig"`
}

// NewEnvelope helper to create a standard packet
func NewEnvelope(id, src, dst, cid string, verb Performative, body interface{}) (*Envelope, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	return &Envelope{
		ID:             id,
		Timestamp:      time.Now().UTC(),
		SenderDID:      src,
		ReceiverDID:    dst,
		Performative:   verb,
		ConversationID: cid,
		Body:           bodyBytes,
	}, nil
}
