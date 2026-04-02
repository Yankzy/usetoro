package incentive

import (
	"context"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// Engine is a system that shapes agent behavior by adjusting rewards, costs,
// and probabilities of execution.
type Engine interface {
	// AllocateLiquidity allocates pool resources for a given task, shaping its pricing bounds.
	AllocateLiquidity(ctx context.Context, task *core.TaskDefinition) error

	// CalculatePenalty assesses standard slashing parameters or financial penalties
	// in the event of an established failure or malicious act.
	CalculatePenalty(ctx context.Context, contract *core.Contract) (int64, error)
}
