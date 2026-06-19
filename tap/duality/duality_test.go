package duality

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/redux"
)

// --- Mocks for Agentic Duality ---

type mockProposer struct {
	proposalFunc func(faults []redux.DomainFault) ([]json.RawMessage, error)
}

func (m *mockProposer) Propose(ctx context.Context, baseState []byte, faults []redux.DomainFault) ([]json.RawMessage, error) {
	return m.proposalFunc(faults)
}

type mockVerifier struct {
	verifyFunc func(patches []json.RawMessage) ([]redux.DomainFault, error)
}

func (m *mockVerifier) Verify(ctx context.Context, baseState []byte, proposedPatches []json.RawMessage) ([]redux.DomainFault, error) {
	return m.verifyFunc(proposedPatches)
}

// TestAgenticDualitySuccess verifies standard execution when proposer generates valid patches.
func TestAgenticDualitySuccess(t *testing.T) {
	ctx := context.Background()
	baseState := []byte(`{}`)

	p := &mockProposer{
		proposalFunc: func(faults []redux.DomainFault) ([]json.RawMessage, error) {
			return []json.RawMessage{[]byte(`{"op":"add","path":"/status","value":"APPROVED"}`)}, nil
		},
	}

	v := &mockVerifier{
		verifyFunc: func(patches []json.RawMessage) ([]redux.DomainFault, error) {
			return nil, nil // No faults
		},
	}

	engine := NewDyadEngine(p, v, 3)
	patches, err := engine.Execute(ctx, baseState)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(patches) != 1 {
		t.Errorf("expected 1 patch, got %d", len(patches))
	}
	if string(patches[0]) != `{"op":"add","path":"/status","value":"APPROVED"}` {
		t.Errorf("unexpected patch content: %s", string(patches[0]))
	}
}

// TestAgenticDualitySelfCorrection verifies the retry-feedback self-correction flow.
func TestAgenticDualitySelfCorrection(t *testing.T) {
	ctx := context.Background()
	baseState := []byte(`{}`)
	proposeCalls := 0

	p := &mockProposer{
		proposalFunc: func(faults []redux.DomainFault) ([]json.RawMessage, error) {
			proposeCalls++
			if len(faults) > 0 {
				// Self-correct based on verification faults
				return []json.RawMessage{[]byte(`{"op":"add","path":"/amount","value":10.0}`)}, nil
			}
			// Initial proposal with invalid type (string instead of float)
			return []json.RawMessage{[]byte(`{"op":"add","path":"/amount","value":"ten"}`)}, nil
		},
	}

	v := &mockVerifier{
		verifyFunc: func(patches []json.RawMessage) ([]redux.DomainFault, error) {
			var m map[string]interface{}
			// Simple schema/type check validation mock
			payload := patches[0]
			_ = json.Unmarshal(payload, &m)
			val := string(payload)
			if val == `{"op":"add","path":"/amount","value":"ten"}` {
				return []redux.DomainFault{
					{
						EventID: "evt_1",
						Error:   "field /amount must be a float, got string",
					},
				}, nil
			}
			return nil, nil // Corrected
		},
	}

	engine := NewDyadEngine(p, v, 3)
	patches, err := engine.Execute(ctx, baseState)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if proposeCalls != 2 {
		t.Errorf("expected 2 proposal generation calls, got %d", proposeCalls)
	}
	if string(patches[0]) != `{"op":"add","path":"/amount","value":10.0}` {
		t.Errorf("unexpected patch: %s", string(patches[0]))
	}
}

// TestAgenticDualityFailure verifies the circuit breaker trips when limit is reached.
func TestAgenticDualityFailure(t *testing.T) {
	ctx := context.Background()
	baseState := []byte(`{}`)

	p := &mockProposer{
		proposalFunc: func(faults []redux.DomainFault) ([]json.RawMessage, error) {
			return []json.RawMessage{[]byte(`{"op":"add","path":"/amount","value":"invalid"}`)}, nil
		},
	}

	v := &mockVerifier{
		verifyFunc: func(patches []json.RawMessage) ([]redux.DomainFault, error) {
			return []redux.DomainFault{
				{
					EventID: "evt_1",
					Error:   "permanent validation error",
				},
			}, nil
		},
	}

	engine := NewDyadEngine(p, v, 3)
	_, err := engine.Execute(ctx, baseState)
	if err == nil {
		t.Fatal("expected engine failure but got none")
	}
}

// --- Tests for Topological Duality ---

func TestTopologicalDuality(t *testing.T) {
	ctx := context.Background()
	gdag := NewGuardrailDAG()

	gdag.RegisterConstraint("step_stripe_transfer", GuardrailConstraint{
		MaxFinancialLimit: 1000.0,
		MaxTokenBudget:    5000,
	})

	// 1. Safe execution within limits
	safePayload := []byte(`{"amount": 500.0, "token_usage": 3000}`)
	err := gdag.ConcurrentGate(ctx, "step_stripe_transfer", safePayload)
	if err != nil {
		t.Errorf("expected safe execution but got error: %v", err)
	}

	// 2. Violation: Exceeds financial limit
	badFinancialPayload := []byte(`{"amount": 1500.0, "token_usage": 3000}`)
	err = gdag.ConcurrentGate(ctx, "step_stripe_transfer", badFinancialPayload)
	if err == nil {
		t.Error("expected error due to financial limit breach, got nil")
	}

	// 3. Violation: Exceeds token budget
	badTokenPayload := []byte(`{"amount": 500.0, "token_usage": 6000}`)
	err = gdag.ConcurrentGate(ctx, "step_stripe_transfer", badTokenPayload)
	if err == nil {
		t.Error("expected error due to token budget breach, got nil")
	}

	// 4. Verification that unconfigured steps pass seamlessly
	err = gdag.ConcurrentGate(ctx, "unknown_step", []byte(`{"amount": 99999.0}`))
	if err != nil {
		t.Errorf("unconfigured step should pass, got: %v", err)
	}
}

// --- Mocks & Tests for State Duality ---

type mockStateProvider struct {
	factFunc         func(intent Intent) (*Fact, error)
	compensateFunc   func(intent Intent) error
	compensationRuns []Intent
}

func (m *mockStateProvider) QueryExternalFact(ctx context.Context, intent Intent) (*Fact, error) {
	return m.factFunc(intent)
}

func (m *mockStateProvider) ExecuteCompensation(ctx context.Context, intent Intent) error {
	m.compensationRuns = append(m.compensationRuns, intent)
	return m.compensateFunc(intent)
}

func TestStateDualityReconciliation(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	// Intent: proposed balance addition of 100
	intent := Intent{
		ID:         "int_001",
		WorkflowID: "wf_001",
		StepID:     "step_1",
		ActorDID:   "did:toro:actor",
		Target:     "/balance",
		Proposed:   json.RawMessage(`100`),
		Timestamp:  time.Now(),
	}

	// Case 1: Reconciled Successfully (Fact matches Intent value)
	providerSuccess := &mockStateProvider{
		factFunc: func(intent Intent) (*Fact, error) {
			return &Fact{
				ID:            "fact_001",
				IntentID:      intent.ID,
				ConfirmedBy:   "stripe",
				ActualValue:   json.RawMessage(`100`),
				TransactionID: "tx_999",
				Timestamp:     time.Now(),
			}, nil
		},
		compensateFunc: func(intent Intent) error {
			return nil
		},
	}

	log1 := &MirrorLog{
		WorkflowID: "wf_001",
		SequenceID: 1,
		Intent:     intent,
		Status:     StatePending,
	}

	engine := NewReconciliationEngine(logger, providerSuccess)
	err := engine.ReconcileLogs(ctx, []*MirrorLog{log1})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	if log1.Status != StateReconciled {
		t.Errorf("expected status to be Reconciled, got %s", log1.Status)
	}
	if len(providerSuccess.compensationRuns) > 0 {
		t.Error("expected no compensation execution on success")
	}

	// Case 2: State Drift Detected -> Saga Rollback Succeeded
	providerDrift := &mockStateProvider{
		factFunc: func(intent Intent) (*Fact, error) {
			return &Fact{
				ID:            "fact_002",
				IntentID:      intent.ID,
				ConfirmedBy:   "stripe",
				ActualValue:   json.RawMessage(`50`), // actual value differs from proposed 100
				TransactionID: "tx_998",
				Timestamp:     time.Now(),
			}, nil
		},
		compensateFunc: func(intent Intent) error {
			return nil // compensation succeeds
		},
	}

	log2 := &MirrorLog{
		WorkflowID: "wf_001",
		SequenceID: 2,
		Intent:     intent,
		Status:     StatePending,
	}

	engineDrift := NewReconciliationEngine(logger, providerDrift)
	err = engineDrift.ReconcileLogs(ctx, []*MirrorLog{log2})
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	if log2.Status != StateCompensated {
		t.Errorf("expected status to be Compensated after drift, got %s", log2.Status)
	}
	if len(providerDrift.compensationRuns) != 1 {
		t.Errorf("expected 1 compensation run, got %d", len(providerDrift.compensationRuns))
	}
	if providerDrift.compensationRuns[0].ID != intent.ID {
		t.Errorf("expected compensation intent ID to match, got %s", providerDrift.compensationRuns[0].ID)
	}
}

// TestStateDualityCompensateFailure verifies engine error escalation when rollback fails.
func TestStateDualityCompensateFailure(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	intent := Intent{
		ID:        "int_002",
		Proposed:  json.RawMessage(`100`),
		Timestamp: time.Now(),
	}

	providerFail := &mockStateProvider{
		factFunc: func(intent Intent) (*Fact, error) {
			return &Fact{
				ActualValue: json.RawMessage(`0`), // Drift
			}, nil
		},
		compensateFunc: func(intent Intent) error {
			return errors.New("external compensation service down")
		},
	}

	log := &MirrorLog{
		Intent: intent,
		Status: StatePending,
	}

	engine := NewReconciliationEngine(logger, providerFail)
	err := engine.ReconcileLogs(ctx, []*MirrorLog{log})
	if err == nil {
		t.Fatal("expected error due to compensation failure, got nil")
	}

	if !errors.Is(err, err) || err.Error() == "" {
		t.Errorf("unexpected error message: %v", err)
	}
}
