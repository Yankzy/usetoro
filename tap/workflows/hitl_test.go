package workflows

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

// ── Test helpers ─────────────────────────────────────────────────────────────

// stubBus is a minimal core.EventBus implementation for unit tests.
type stubBus struct {
	publishFn func(subject string, data []byte) error
}

func (s *stubBus) Publish(subject string, data []byte) error {
	if s.publishFn != nil {
		return s.publishFn(subject, data)
	}
	return nil
}

func (s *stubBus) PublishCore(subject string, data []byte) error { return s.Publish(subject, data) }

func (s *stubBus) RequestWithContext(_ context.Context, _ string, _ []byte) (*nats.Msg, error) {
	return nil, nil
}

func (s *stubBus) QueueSubscribe(_, _ string, _ nats.MsgHandler, _ ...nats.SubOpt) (*nats.Subscription, error) {
	return nil, nil
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestNormalizeSkipsHITLSteps verifies that hitl.* steps are exempt from
// complexity and depends_on normalization just like sub_workflow steps.
func TestNormalizeSkipsHITLSteps(t *testing.T) {
	def := WorkflowDef{
		Name: "test-hitl-normalize",
		Steps: []WorkflowStep{
			{ID: "step_a", ActivityType: "agents.foo"},
			{ID: "human_gate", ActivityType: "hitl.review"}, // HITL — must not get depends_on injected
			{ID: "step_c", ActivityType: "workers.bar"},
		},
	}

	result, mutated := normalizeWorkflowDef(def)
	if !mutated {
		t.Fatal("expected normalizeWorkflowDef to report mutation")
	}

	// step_a has no depends_on and is first — should stay empty
	if len(result.Steps[0].DependsOn) != 0 {
		t.Errorf("step_a should have no depends_on, got %v", result.Steps[0].DependsOn)
	}

	// human_gate is hitl.* — normalizer must NOT inject depends_on
	gate := result.Steps[1]
	if len(gate.DependsOn) != 0 {
		t.Errorf("hitl.review step should not have auto-injected depends_on, got %v", gate.DependsOn)
	}
	// And it must not have a complexity set
	if gate.Complexity != 0 {
		t.Errorf("hitl.review step should have zero complexity, got %v", gate.Complexity)
	}
}

// TestHITLPrefixConstant sanity-checks that the constant matches the convention.
func TestHITLPrefixConstant(t *testing.T) {
	if !strings.HasPrefix("hitl.review", HITLPrefixActivity) {
		t.Errorf("hitl.review should match HITLPrefixActivity %q", HITLPrefixActivity)
	}
	if strings.HasPrefix("agents.foo", HITLPrefixActivity) {
		t.Error("agents.foo should not match HITLPrefixActivity")
	}
}

// TestSuspendForHITL_MutatesState verifies that suspendForHITL correctly
// mutates the InstanceState (suspension flags, active step) and produces a
// valid HITLPendingEvent without needing a real NATS bus.
func TestSuspendForHITL_MutatesState(t *testing.T) {
	// Build a stub bus that captures publishes.
	var capturedSubject string
	var capturedData []byte
	bus := &stubBus{
		publishFn: func(subject string, data []byte) error {
			capturedSubject = subject
			capturedData = data
			return nil
		},
	}

	o := &Orchestrator{
		bus:    bus,
		logger: newTestLogger(),
	}

	step := WorkflowStep{
		ID:           "human_review",
		ActivityType: "hitl.review",
		Timeout:      "3600s",
		Config: map[string]interface{}{
			"scope":         "session",
			"prompt":        "Please review",
			"allow_edits":   true,
			"reject_action": "abort",
		},
	}

	entityID := pgtype.UUID{Valid: false}
	state := &InstanceState{
		InstancePath:   []string{"instance-123"},
		ActiveSteps:    make(map[string]bool),
		CompletedSteps: make(map[string]bool),
		Variables:      make(map[string]json.RawMessage),
	}

	err := o.suspendForHITL(step, state, entityID)
	if err != nil {
		t.Fatalf("suspendForHITL returned error: %v", err)
	}

	// State assertions.
	if !state.Suspended {
		t.Error("state.Suspended should be true after suspendForHITL")
	}
	if state.SuspensionStep != "human_review" {
		t.Errorf("SuspensionStep = %q, want %q", state.SuspensionStep, "human_review")
	}
	if state.SuspensionKind != SuspensionKindHITL {
		t.Errorf("SuspensionKind = %q, want %q", state.SuspensionKind, SuspensionKindHITL)
	}
	if !state.ActiveSteps["human_review"] {
		t.Error("human_review should be in ActiveSteps")
	}

	// Event assertions.
	if capturedSubject != HITLPendingSubject {
		t.Errorf("published to %q, want %q", capturedSubject, HITLPendingSubject)
	}
	var evt HITLPendingEvent
	if err := json.Unmarshal(capturedData, &evt); err != nil {
		t.Fatalf("could not unmarshal HITLPendingEvent: %v", err)
	}
	if evt.Status != "hitl_pending" {
		t.Errorf("evt.Status = %q, want \"hitl_pending\"", evt.Status)
	}
	if evt.StepID != "human_review" {
		t.Errorf("evt.StepID = %q, want \"human_review\"", evt.StepID)
	}
	if evt.Scope != "session" {
		t.Errorf("evt.Scope = %q, want \"session\"", evt.Scope)
	}
	if !evt.AllowEdits {
		t.Error("evt.AllowEdits should be true")
	}
	if evt.ExpiresAt == "" {
		t.Error("evt.ExpiresAt should be set when step has a timeout")
	}
	if evt.InstanceID != "instance-123" {
		t.Errorf("evt.InstanceID = %q, want \"instance-123\"", evt.InstanceID)
	}
}

// TestReadySteps_HitlBlockedUntilDependenciesMet ensures that a hitl step is
// not surfaced as ready until its depends_on steps are completed.
func TestReadySteps_HitlBlockedUntilDependenciesMet(t *testing.T) {
	def := WorkflowDef{
		Steps: []WorkflowStep{
			{ID: "select_account", ActivityType: "agents.accounting.select_account"},
			{ID: "human_review", ActivityType: "hitl.review", DependsOn: []string{"select_account"}},
			{ID: "sync_to_qbo", ActivityType: "workers.qbo_sync", DependsOn: []string{"human_review"}},
		},
	}

	// Nothing completed yet — only select_account should be ready.
	state := InstanceState{
		ActiveSteps:    make(map[string]bool),
		CompletedSteps: make(map[string]bool),
		Variables:      make(map[string]json.RawMessage),
	}
	ready := readySteps(def, state)
	if len(ready) != 1 || ready[0].ID != "select_account" {
		t.Errorf("expected only select_account ready, got %v", stepIDsFromList(ready))
	}

	// Mark select_account complete — now human_review should be ready.
	state.CompletedSteps["select_account"] = true
	ready = readySteps(def, state)
	if len(ready) != 1 || ready[0].ID != "human_review" {
		t.Errorf("expected only human_review ready, got %v", stepIDsFromList(ready))
	}

	// HITL step is active (suspended) — sync_to_qbo must NOT be ready.
	state.ActiveSteps["human_review"] = true
	ready = readySteps(def, state)
	if len(ready) != 0 {
		t.Errorf("expected no ready steps while HITL is active, got %v", stepIDsFromList(ready))
	}
}

// TestConfigHelpers validates the string/bool config extractors.
func TestConfigHelpers(t *testing.T) {
	cfg := map[string]interface{}{
		"scope":       "session",
		"allow_edits": true,
		"count":       42,
	}
	if got := stringFromConfig(cfg, "scope"); got != "session" {
		t.Errorf("stringFromConfig scope = %q, want \"session\"", got)
	}
	if got := stringFromConfig(cfg, "missing"); got != "" {
		t.Errorf("stringFromConfig missing = %q, want \"\"", got)
	}
	if got := boolFromConfig(cfg, "allow_edits"); !got {
		t.Error("boolFromConfig allow_edits should be true")
	}
	if got := boolFromConfig(cfg, "count"); got {
		t.Error("boolFromConfig count (int) should return false")
	}
	if got := stringFromConfig(nil, "x"); got != "" {
		t.Error("stringFromConfig with nil config should return \"\"")
	}
}
