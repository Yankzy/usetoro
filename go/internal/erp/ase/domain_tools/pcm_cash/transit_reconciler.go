package pcm_cash

import (
	"fmt"
	"math"
	"time"
)

const TransitClearingAccount = "511500" // Virements de fonds

type TransitLegType string

const (
	TransitLegOutgoing TransitLegType = "OUTGOING" // Credit Source Bank (5141), Debit 5115
	TransitLegIncoming TransitLegType = "INCOMING" // Debit Target Bank/Cash (5141/5161), Credit 5115
)

// TransitTransferLeg represents one side of an inter-account fund movement.
type TransitTransferLeg struct {
	ID            string         `json:"id"`
	LegType       TransitLegType `json:"leg_type"`
	SourceAccount string         `json:"source_account"`
	TargetAccount string         `json:"target_account"`
	Amount        float64        `json:"amount"`
	Timestamp     time.Time      `json:"timestamp"`
	RefNum        string         `json:"ref_num"`
}

// TransitNettingResult details the reconciliation of matching 5115 transfer legs.
type TransitNettingResult struct {
	TransitAccount    string  `json:"transit_account"`
	OutgoingTotal     float64 `json:"outgoing_total"`
	IncomingTotal     float64 `json:"incoming_total"`
	NetBalance        float64 `json:"net_balance"`
	IsCleared         bool    `json:"is_cleared"`
	MatchedLegsCount  int     `json:"matched_legs_count"`
	UnmatchedLegsCount int    `json:"unmatched_legs_count"`
}

// ReconcileTransitAccount evaluates incoming and outgoing legs in 5115, verifying auto-netting to 0 MAD.
func ReconcileTransitAccount(outgoingLegs []TransitTransferLeg, incomingLegs []TransitTransferLeg) (*TransitNettingResult, error) {
	var outSum, inSum float64
	for _, leg := range outgoingLegs {
		outSum += leg.Amount
	}
	for _, leg := range incomingLegs {
		inSum += leg.Amount
	}

	net := math.Round(math.Abs(outSum-inSum)*100) / 100
	isCleared := net == 0.0

	res := &TransitNettingResult{
		TransitAccount:     TransitClearingAccount,
		OutgoingTotal:      outSum,
		IncomingTotal:      inSum,
		NetBalance:         net,
		IsCleared:          isCleared,
		MatchedLegsCount:   min(len(outgoingLegs), len(incomingLegs)),
		UnmatchedLegsCount: absInt(len(outgoingLegs) - len(incomingLegs)),
	}

	if !isCleared {
		return res, fmt.Errorf("HOLD: UNMATCHED_TRANSIT_PAIR: Transit account 5115 net balance is %.2f MAD (Expected: 0.00 MAD)", net)
	}

	return res, nil
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

