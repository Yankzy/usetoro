package incentive

import (
	"context"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// EscrowManager holds risk profiles and validates lock/release conditions.
type EscrowManager interface {
	// Lock holds the allocated task reward and any risk-slashing collateral from the agent.
	Lock(ctx context.Context, contract *core.Contract, amount int64) error

	// Release transfers escrowed funds upon a successful Verification/Settle.
	Release(ctx context.Context, contractID string) error

	// Slash burns or transfers escrowed collateral corresponding to defined SLA violations.
	Slash(ctx context.Context, contractID string, penaltyAmount int64) error
}
