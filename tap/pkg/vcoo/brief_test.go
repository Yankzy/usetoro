package vcoo

import (
	"context"
	"strings"
	"testing"
)

type MockLLM struct{}

func (m *MockLLM) GenerateText(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	return "MOCK COMMAND BRIEF TEXT", nil
}

func TestGenerateDailyBrief(t *testing.T) {
	tasks := []Task{
		{ID: "ENGINE-SANDBOX-WASM", Weight: 0.40, Status: "ACTIVE"},
	}
	targets := []Target{
		{ID: "OUTBOUND_CALLS", Metric: 250, Unit: "Dials"},
		{ID: "VIDEO_PROOFS", Metric: 10, Unit: "Drops"},
		{ID: "X_DAILY_POSTS", Metric: 35, Unit: "Posts"},
		{ID: "ONBOARDING_RUNS", Metric: 5, Unit: "Calls"},
	}

	ctx := context.Background()

	// 1. Test standard deterministic fallback formatting (with nil LLM)
	brief, err := GenerateDailyBrief(ctx, nil, "founder@yourplatform.com", -0.12, true, 1.5, []string{"WASM memory page bounds"}, tasks, targets)
	if err != nil {
		t.Fatalf("failed to generate brief: %v", err)
	}

	if !strings.Contains(brief, "To: founder@yourplatform.com") {
		t.Errorf("brief missing founder email: %s", brief)
	}
	if !strings.Contains(brief, "Current Weekly Velocity Delta: -0.12") {
		t.Errorf("brief missing delta: %s", brief)
	}
	if !strings.Contains(brief, "Restriction Protocol: ACTIVE") {
		t.Errorf("brief missing restriction: %s", brief)
	}
	if !strings.Contains(brief, "Outbound VoIP Dials: 75 calls") {
		t.Errorf("brief missing scaled outbound calls (50 * 1.5): %s", brief)
	}

	// 2. Test LLM generator
	llm := &MockLLM{}
	llmBrief, err := GenerateDailyBrief(ctx, llm, "founder@yourplatform.com", -0.12, true, 1.5, []string{"WASM memory page bounds"}, tasks, targets)
	if err != nil {
		t.Fatalf("failed to generate brief with LLM: %v", err)
	}
	if llmBrief != "MOCK COMMAND BRIEF TEXT" {
		t.Errorf("expected MOCK COMMAND BRIEF TEXT, got: %s", llmBrief)
	}
}
