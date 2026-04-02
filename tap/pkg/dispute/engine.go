package dispute

import (
	"context"
)

// ResolutionDecision holds the outcome of a disputed contract.
type ResolutionDecision struct {
	WinnerDID   string  `json:"winner_did"`
	LoserDID    string  `json:"loser_did"`
	Penalty     int64   `json:"penalty"`
	Justification string `json:"justification"`
}

// Engine manages the recursive arbitration and resolution of conflicting claims.
type Engine interface {
	// RaiseDispute initiates the process, transitioning the contract to Disputed.
	RaiseDispute(ctx context.Context, contractID string, challengerDID string, reason string) error

	// Resolve evaluates evidence and outputs a decision shaping future trust via the reputation engine.
	Resolve(ctx context.Context, contractID string) (*ResolutionDecision, error)
}
