package tools

import (
	"strings"
)

type VCOOStateStruct struct {
	VelocityDelta      float64 `json:"velocity_delta"`
	RestrictionActive  bool    `json:"restriction_active"`
	Multiplier         float64 `json:"multiplier"`
	LastBriefDate      string  `json:"last_brief_date"`
	LastVelocityUpdate string  `json:"last_velocity_update"`
}

// CalculateVelocityDelta computes the velocity index delta based on baseline targets, actuals, and time horizon.
// It scales baseline targets (which are weekly targets) depending on the requested horizon.
// Formula: \Delta VI = (\sum A_{actual, i} / \sum T_{scaled_target, i}) - 1
func CalculateVelocityDelta(actuals map[string]int, targets map[string]float64, horizon string) float64 {
	var actualSum float64
	var targetSum float64

	mapping := map[string]string{
		"OUTBOUND_CALLS":  "outbound_voip_dials",
		"VIDEO_PROOFS":    "video_proofs_dropped",
		"X_DAILY_POSTS":   "x_posts_executed",
		"ONBOARDING_RUNS": "scheduled_onboardings",
	}

	for targetID, targetVal := range targets {
		actualKey, exists := mapping[targetID]
		if !exists {
			actualKey = strings.ToLower(targetID)
		}
		actualVal := actuals[actualKey]
		actualSum += float64(actualVal)
		targetSum += targetVal
	}

	if targetSum == 0 {
		return 0.0
	}

	// Calculate target scaling factor relative to weekly baseline (1.0)
	var factor float64 = 1.0
	switch strings.ToLower(strings.TrimSpace(horizon)) {
	case "hour":
		factor = 1.0 / (7.0 * 24.0)
	case "daily", "day":
		factor = 1.0 / 7.0
	case "weekly", "week":
		factor = 1.0
	case "quarterly", "quarter":
		factor = 13.0
	case "yearly", "year":
		factor = 52.0
	}

	scaledTargetSum := targetSum * factor
	if scaledTargetSum == 0 {
		return 0.0
	}

	return (actualSum / scaledTargetSum) - 1.0
}
