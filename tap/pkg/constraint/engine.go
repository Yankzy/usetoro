package constraint

import (
	"context"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// Engine defines a pre-execution deterministic rule system.
type Engine interface {
	// Evaluate acts as a gatekeeper. It must return true (Allow) for a contract to proceed.
	Evaluate(ctx context.Context, contract *core.Contract) (bool, error)
}
