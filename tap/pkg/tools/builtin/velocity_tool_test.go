package builtin

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestCalculateVelocityTool(t *testing.T) {
	tool := &CalculateVelocityTool{
		Logger: slog.Default(),
	}

	// 1. Invalid targets input
	_, err := tool.Call(context.Background(), map[string]any{
		"actuals": map[string]any{"outbound_voip_dials": 10},
		"horizon": "daily",
	})
	if err == nil || !strings.Contains(err.Error(), "missing or invalid 'targets'") {
		t.Errorf("expected targets error, got %v", err)
	}

	// 2. Invalid actuals input
	_, err = tool.Call(context.Background(), map[string]any{
		"targets": map[string]any{"OUTBOUND_CALLS": 250},
		"horizon": "daily",
	})
	if err == nil || !strings.Contains(err.Error(), "missing or invalid 'actuals'") {
		t.Errorf("expected actuals error, got %v", err)
	}

	// 3. Successful calculation (weekly exact match)
	res, err := tool.Call(context.Background(), map[string]any{
		"targets": map[string]any{
			"OUTBOUND_CALLS":  250.0,
			"VIDEO_PROOFS":    10.0,
			"X_DAILY_POSTS":   35.0,
			"ONBOARDING_RUNS": 5.0,
		},
		"actuals": map[string]any{
			"outbound_voip_dials":   250,
			"video_proofs_dropped":  10,
			"x_posts_executed":      35,
			"scheduled_onboardings": 5,
		},
		"horizon": "weekly",
	})
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
	}

	expectedPrefix := "Calculated Velocity Delta: 0.0000"
	if !strings.HasPrefix(res, expectedPrefix) {
		t.Errorf("expected output to start with %q, got %q", expectedPrefix, res)
	}
}
