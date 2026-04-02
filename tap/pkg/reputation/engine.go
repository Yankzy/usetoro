package reputation

import (
	"context"
)

// Engine is responsible for continuously calculating and retrieving dynamic 
// reliability scores for agents based on outcomes and non-linear decay.
type Engine interface {
	// SubmitOutcome takes an evaluation from a finished/disputed task and
	// adjusts the agent's multi-dimensional reputation score accordingly.
	SubmitOutcome(ctx context.Context, eval *Evaluation) error

	// GetScore retrieves the current computed reputation for an agent in a specific capability.
	GetScore(ctx context.Context, did, capability string) (*ReputationScore, error)

	// GetSystemWidePercentile returns the relative ranking (from 0 to 1) 
	// for an agent within a specific capability domain.
	GetSystemWidePercentile(ctx context.Context, did, capability string) (float64, error)
}
