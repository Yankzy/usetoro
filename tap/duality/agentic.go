package duality

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Yankzy/usetoro/tap/pkg/redux"
)

// Proposer generates the non-deterministic payload (e.g. LLM inputs/outputs).
type Proposer interface {
	Propose(ctx context.Context, baseState []byte, faults []redux.DomainFault) ([]json.RawMessage, error)
}

// Verifier handles strict type-assertion, schema validation, and logic checking.
type Verifier interface {
	Verify(ctx context.Context, baseState []byte, proposedPatches []json.RawMessage) ([]redux.DomainFault, error)
}

// DyadEngine coordinates the twin-engine proposer-verifier execution.
type DyadEngine struct {
	Proposer   Proposer
	Verifier   Verifier
	MaxRetries int
}

// NewDyadEngine creates a new Proposer-Verifier twin-engine.
func NewDyadEngine(p Proposer, v Verifier, maxRetries int) *DyadEngine {
	if maxRetries <= 0 {
		maxRetries = 3
	}
	return &DyadEngine{
		Proposer:   p,
		Verifier:   v,
		MaxRetries: maxRetries,
	}
}

// Execute runs the proposer-verifier loop, feeding back verification faults to the proposer
// so it can self-correct, up to MaxRetries.
func (de *DyadEngine) Execute(ctx context.Context, baseState []byte) ([]json.RawMessage, error) {
	var faults []redux.DomainFault

	for attempt := 0; attempt < de.MaxRetries; attempt++ {
		// 1. Propose non-deterministic patches
		patches, err := de.Proposer.Propose(ctx, baseState, faults)
		if err != nil {
			return nil, fmt.Errorf("proposal generator failed (attempt %d): %w", attempt+1, err)
		}

		// 2. Perform adversarial validation
		verificationFaults, err := de.Verifier.Verify(ctx, baseState, patches)
		if err != nil {
			return nil, fmt.Errorf("verifier logic error (attempt %d): %w", attempt+1, err)
		}

		if len(verificationFaults) > 0 {
			faults = verificationFaults
			continue // retry loop with feedback faults
		}

		// Success
		return patches, nil
	}

	return nil, fmt.Errorf("dyad engine failed after %d retries: %v", de.MaxRetries, faults)
}
