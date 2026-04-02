package incentive

import (
	"context"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// AuctionType specifies the matching mechanism for an interaction proposal.
type AuctionType string

const (
	AuctionFirstPrice  AuctionType = "FIRST_PRICE_AUCTION"
	AuctionSecondPrice AuctionType = "SECOND_PRICE_AUCTION"
	AuctionAdaptive    AuctionType = "ADAPTIVE_AUCTION"
)

// AuctionManager manages the bid/proposal flow shaping interactions.
type AuctionManager interface {
	// ValidateBid asserts a proposed bid matches the current dynamic pricing bounds.
	ValidateBid(ctx context.Context, task *core.TaskDefinition, bidAmount int64) (bool, error)

	// SettleAuction resolves an auction and assigns the winner identity and escrow value.
	SettleAuction(ctx context.Context, taskID string) (winnerDID string, finalPrice int64, err error)
}
