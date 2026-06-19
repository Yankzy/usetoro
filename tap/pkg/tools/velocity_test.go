package tools

import (
	"math"
	"testing"
)

func TestCalculateVelocityDelta(t *testing.T) {
	targets := map[string]float64{
		"OUTBOUND_CALLS":  250.0,
		"VIDEO_PROOFS":    10.0,
		"X_DAILY_POSTS":   35.0,
		"ONBOARDING_RUNS": 5.0,
	} // Weekly target sum: 300

	tests := []struct {
		name    string
		actuals map[string]int
		horizon string
		want    float64
	}{
		{
			name: "exact match weekly targets",
			actuals: map[string]int{
				"outbound_voip_dials":   250,
				"video_proofs_dropped":  10,
				"x_posts_executed":      35,
				"scheduled_onboardings": 5,
			},
			horizon: "weekly",
			want:    0.0,
		},
		{
			name: "outperforming weekly",
			actuals: map[string]int{
				"outbound_voip_dials":   300,
				"video_proofs_dropped":  12,
				"x_posts_executed":      40,
				"scheduled_onboardings": 8,
			},
			horizon: "week",
			want:    0.2, // (360 / 300) - 1
		},
		{
			name: "exact daily targets",
			actuals: map[string]int{
				"outbound_voip_dials":   250,
				"video_proofs_dropped":  10,
				"x_posts_executed":      35,
				"scheduled_onboardings": 5,
			},
			horizon: "daily",
			// Target scaled by 1/7 = 300 / 7 = 42.857
			// Actual sum = 300
			// Delta = (300 / 42.857) - 1 = 6.0
			want: 6.0,
		},
		{
			name: "exact daily targets with daily actuals",
			actuals: map[string]int{
				"outbound_voip_dials":   35, // 250 / 7 = 35.7
				"video_proofs_dropped":  1,  // 10 / 7 = 1.4
				"x_posts_executed":      5,  // 35 / 7 = 5
				"scheduled_onboardings": 1,
			}, // Daily actual sum: 42
			horizon: "daily",
			// (42 / (300 / 7.0)) - 1 = (42 / 42.857) - 1 = -0.02
			want: -0.02,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateVelocityDelta(tt.actuals, targets, tt.horizon)
			if math.Abs(got-tt.want) > 1e-4 {
				t.Errorf("CalculateVelocityDelta() = %v, want %v", got, tt.want)
			}
		})
	}
}
