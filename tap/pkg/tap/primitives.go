package tap

import (
	"encoding/json"
	"time"
)

// --- 1. The Task Primitive (The Work) ---

// TaskComplexity defines the skill tier required.
type TaskComplexity int

const (
	ComplexityEntry  TaskComplexity = 1  // Simple
	ComplexityJunior TaskComplexity = 5  // Moderate
	ComplexitySenior TaskComplexity = 10 // Expert
)

// TaskDefinition is the generic "Unit of Work" broadcast to the network.
type TaskDefinition struct {
	ID         string         `json:"id"`
	Domain     string         `json:"domain"`     // e.g., "accounting", "logistics"
	Complexity TaskComplexity `json:"complexity"` // Used for NATS routing permissions

	// The Incentive
	Reward   int64  `json:"reward"`   // Amount in micros
	Currency string `json:"currency"` // e.g., "USD", "TORO"

	// The "Black Box" Payload.
	// If Domain="logistics", this matches the Load struct.
	// If Domain="accounting", this matches the Receipt struct.
	Payload json.RawMessage `json:"payload"`

	ExpiresAt int64 `json:"expires_at"` // Unix Timestamp
}

// --- 2. The Contract Primitive (The Agreement) ---

type ContractStatus string

const (
	ContractDraft    ContractStatus = "DRAFT"
	ContractLocked   ContractStatus = "LOCKED"  // Binding
	ContractSettled  ContractStatus = "SETTLED" // Paid
	ContractDisputed ContractStatus = "DISPUTED"
)

// Contract represents the locked state between two agents.
type Contract struct {
	ID             string `json:"id"`
	ConversationID string `json:"cid"`

	InitiatorDID string `json:"initiator_did"`
	AcceptorDID  string `json:"acceptor_did"`

	// The agreed terms (Snapshot of Task + Proposal)
	Terms     json.RawMessage `json:"terms"`
	TermsHash string          `json:"terms_hash"` // Integrity check

	// Signatures[DID] = Signature
	Signatures map[string]string `json:"signatures"`

	Status    ContractStatus `json:"status"`
	CreatedAt time.Time      `json:"created_at"`
}

// --- 3. The Proof Primitive (The Verification) ---

type ProofType string

const (
	ProofGPS            ProofType = "proof.gps"
	ProofClassification ProofType = "proof.classification" // Manual Swipe result
	ProofAPI            ProofType = "proof.api"            // External system result
)

// Proof is the evidence submitted by the Worker to claim the Reward.
type Proof struct {
	TaskID    string    `json:"task_id"`
	Type      ProofType `json:"type"`
	Timestamp int64     `json:"ts"`

	// Evidence Data
	// GPS: { "lat": ..., "lon": ... }
	// Swipe: { "category": "Meals" }
	Data json.RawMessage `json:"data"`

	Signature string `json:"sig"` // Worker's signature of the Data
}
