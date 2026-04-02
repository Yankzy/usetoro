package core

import (
	"encoding/json"
	"time"
)

// --- 1. The Task Primitive (The Work) ---

// MicrionMultiplier defines the fractional conversion unit. (e.g. 1 USD = 1,000,000 uC)
const MicrionMultiplier int64 = 1_000_000

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
	Reward   int64  `json:"reward"`   // Amount in micrions
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
	ContractDraft      ContractStatus = "DRAFT"
	ContractProposed   ContractStatus = "PROPOSED"
	ContractValidated  ContractStatus = "VALIDATED"
	ContractEscrowed   ContractStatus = "ESCROWED"
	ContractLocked     ContractStatus = "LOCKED"      // Binding
	ContractInProgress ContractStatus = "IN_PROGRESS" // Being Worked On
	ContractSettled    ContractStatus = "SETTLED"     // Paid
	ContractDisputed   ContractStatus = "DISPUTED"
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

// --- 4. The Identity Primitive (The Actor) ---

// Identity represents a comprehensive, verifiable digital identity for an agent,
// inspired by the W3C DID specification.
type Identity struct {
	// The DID URI, the unique, persistent identifier for the agent.
	// e.g., "did:toro:1a2b3c-v2"
	ID string `json:"id"`

	// The DID of the entity (e.g., an organization or user) that controls this agent's identity.
	// This is crucial for establishing trust, ownership, and accountability.
	Controller string `json:"controller"`

	// A list of cryptographic public keys associated with the DID.
	// Allows for key rotation and specifying different keys for different purposes
	// (e.g., one for authentication, another for signing contracts).
	VerificationMethods []VerificationMethod `json:"verificationMethod"`

	// A list of cryptographically verifiable claims (e.g., skills, certifications, reputation scores)
	// issued by trusted third parties. This is far more trustworthy than a self-declared list of domains.
	VerifiableCredentials []VerifiableCredential `json:"verifiableCredential,omitempty"`

	// A list of service endpoints for interaction. This tells other agents
	// how to communicate with this one (e.g., where to send messages or tasks).
	Services []ServiceEndpoint `json:"service,omitempty"`

	// CapabilityVector measures the agent's competency in specific domains (e.g., {"logistics": 0.95}).
	CapabilityVector map[string]float64 `json:"capabilityVector,omitempty"`

	// Metadata for lifecycle management.
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	Version int64     `json:"version"` // Monotonically increasing version number
	Status  string    `json:"status"`  // e.g., "active", "revoked", "deprecated"
}

// VerificationMethod defines a public key and its purpose.
type VerificationMethod struct {
	ID         string `json:"id"`              // e.g., "did:toro:1a2b3c-v2#keys-1"
	Type       string `json:"type"`            // e.g., "Ed25519VerificationKey2020"
	Controller string `json:"controller"`      // The DID that controls this key
	PublicKey  string `json:"publicKeyBase58"` // The public key encoded in Base58
}

// ServiceEndpoint describes how to interact with the agent.
type ServiceEndpoint struct {
	ID              string `json:"id"`              // e.g., "did:toro:1a2b3c-v2#c-aip"
	Type            string `json:"type"`            // e.g., "cAIP-v1"
	ServiceEndpoint string `json:"serviceEndpoint"` // The URL or address for the service
}

// VerifiableCredential is a tamper-evident claim made by an issuer about a subject.
type VerifiableCredential struct {
	Context           []string        `json:"@context"` // W3C context (e.g., "https://www.w3.org/2018/credentials/v1")
	ID                string          `json:"id"`       // Unique ID for the credential
	Type              []string        `json:"type"`     // e.g., ["VerifiableCredential", "ToroSkillCredential"]
	Issuer            string          `json:"issuer"`   // DID of the issuer (e.g., "did:toro:org-quickbooks")
	IssuanceDate      time.Time       `json:"issuanceDate"`
	CredentialSubject json.RawMessage `json:"credentialSubject"` // The actual claim data (e.g., {"skill": "invoicing", "level": "expert"})
	Proof             CredentialProof `json:"proof"`             // The digital signature from the issuer
}

// CredentialProof is the cryptographic signature that makes a credential verifiable.
type CredentialProof struct {
	Type               string    `json:"type"` // e.g., "Ed25519Signature2020"
	Created            time.Time `json:"created"`
	ProofPurpose       string    `json:"proofPurpose"`       // e.g., "assertionMethod"
	VerificationMethod string    `json:"verificationMethod"` // The key used for signing
	SignatureValue     string    `json:"signatureValue"`     // The base64-encoded signature
}
