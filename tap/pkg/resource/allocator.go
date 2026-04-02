package resource

import (
	"context"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// Allocator manages compute, bandwidth, and task liquidity flow to prevent spam
// and prioritize high-reputation cluster tasks.
type Allocator interface {
	// RequestAllocation reserves system capability for an interaction.
	// Returns true if the system load allows the specified interaction size.
	RequestAllocation(ctx context.Context, task *core.TaskDefinition) (bool, error)

	// ReleaseAllocation frees the locked resource bandwidth post-interaction.
	ReleaseAllocation(ctx context.Context, taskID string) error
}
