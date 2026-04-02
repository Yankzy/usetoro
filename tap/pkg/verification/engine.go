package verification

import (
	"context"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// ValidityScore measures the confidence of the verification process (probabilistic).
type ValidityScore struct {
	IsValid    bool    `json:"is_valid"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning,omitempty"`
}

// Engine evaluates whether a task outcome satisfies its declarative intent.
type Engine interface {
	// Verify compares a submitted proof against the contract terms.
	// It is capable of deterministic hash checks or probabilistic model-based validation.
	Verify(ctx context.Context, contract *core.Contract, proof *core.Proof) (*ValidityScore, error)
}
