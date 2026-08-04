package ase

import (
	"math"
	"testing"
)

func TestCalculateEntropy_EmptyCandidates(t *testing.T) {
	entropy := CalculateEntropy(nil)
	if entropy != 1.0 {
		t.Errorf("expected entropy 1.0 for nil candidates, got %f", entropy)
	}

	entropy = CalculateEntropy([]ProbabilityCandidate{})
	if entropy != 1.0 {
		t.Errorf("expected entropy 1.0 for empty candidates, got %f", entropy)
	}
}

func TestCalculateEntropy_SingleCandidate(t *testing.T) {
	candidates := []ProbabilityCandidate{
		{Value: "EXPENSE", Confidence: 0.95},
	}
	entropy := CalculateEntropy(candidates)
	if entropy != 0.0 {
		t.Errorf("expected entropy 0.0 for single candidate, got %f", entropy)
	}
}

func TestCalculateEntropy_UniformDistribution(t *testing.T) {
	// Two candidates with equal confidence → maximum entropy (1.0).
	candidates := []ProbabilityCandidate{
		{Value: "EXPENSE", Confidence: 0.5},
		{Value: "REVENUE", Confidence: 0.5},
	}
	entropy := CalculateEntropy(candidates)
	// H = -0.5*log2(0.5) - 0.5*log2(0.5) = 1.0
	if math.Abs(entropy-1.0) > 0.001 {
		t.Errorf("expected entropy ~1.0 for uniform distribution, got %f", entropy)
	}
}

func TestCalculateEntropy_HighConfidenceDominance(t *testing.T) {
	// One highly confident candidate, others low → low entropy.
	candidates := []ProbabilityCandidate{
		{Value: "EXPENSE", Confidence: 0.98},
		{Value: "ASSET", Confidence: 0.01},
		{Value: "LIABILITY", Confidence: 0.01},
	}
	entropy := CalculateEntropy(candidates)
	if entropy > 0.2 {
		t.Errorf("expected low entropy (< 0.2) when one candidate dominates, got %f", entropy)
	}
}

func TestCalculateEntropy_ThreeWayTie(t *testing.T) {
	// Three candidates with equal confidence.
	candidates := []ProbabilityCandidate{
		{Value: "EXPENSE", Confidence: 1.0},
		{Value: "ASSET", Confidence: 1.0},
		{Value: "LIABILITY", Confidence: 1.0},
	}
	entropy := CalculateEntropy(candidates)
	// H = -3 * (1/3 * log2(1/3)) / log2(3) = 1.0
	if math.Abs(entropy-1.0) > 0.0001 {
		t.Errorf("expected entropy ~1.0 for three way tie, got %f", entropy)
	}
}

func TestCalculateEntropy_ZeroConfidence(t *testing.T) {
	// All zero confidences → normalized = 1.0 max entropy.
	candidates := []ProbabilityCandidate{
		{Value: "EXPENSE", Confidence: 0.0},
		{Value: "ASSET", Confidence: 0.0},
	}
	entropy := CalculateEntropy(candidates)
	if entropy != 1.0 {
		t.Errorf("expected entropy 1.0 for zero-confidence candidates, got %f", entropy)
	}
}

func TestCalculateEntropy_AsymmetricDistribution(t *testing.T) {
	// 70/30 distribution → moderate entropy.
	candidates := []ProbabilityCandidate{
		{Value: "EXPENSE", Confidence: 0.7},
		{Value: "REVENUE", Confidence: 0.3},
	}
	entropy := CalculateEntropy(candidates)
	// H = -0.7*log2(0.7) - 0.3*log2(0.3) ≈ 0.881
	expected := 0.881
	if math.Abs(entropy-expected) > 0.01 {
		t.Errorf("expected entropy ~%f for 70/30 split, got %f", expected, entropy)
	}
}

func TestNewASENode_InitialState(t *testing.T) {
	node := NewASENode("tenant-1", "", "default", map[string]any{"raw_description": "Staples purchase", "cash_direction": "OUTFLOW", "raw_amount": "-54.23"})

	if node.CurrentState != StateUninitialized {
		t.Errorf("expected initial state UNINITIALIZED, got %s", node.CurrentState)
	}
	if node.CurrentEntropy != 4.0 {
		t.Errorf("expected initial entropy 4.0, got %f", node.CurrentEntropy)
	}
	if node.LifetimeProbes != 0 {
		t.Errorf("expected 0 probes, got %d", node.LifetimeProbes)
	}
	if node.NodeID == "" {
		t.Error("expected non-empty NodeID")
	}
}

func TestASENode_TopCandidate(t *testing.T) {
	node := NewASENode("t1", "", "default", map[string]any{"raw_description": "desc", "cash_direction": "OUTFLOW", "raw_amount": "-10"})
	if top := node.TopCandidate("test_property_1"); top != nil {
		t.Error("expected nil top candidate for new node")
	}

	node.SetPropertyCandidates("test_property_1", []ProbabilityCandidate{
		{Value: "EXPENSE", Confidence: 0.6},
		{Value: "ASSET", Confidence: 0.95},
	})
	top := node.TopCandidate("test_property_1")
	if top == nil || top.Value != "ASSET" {
		t.Errorf("expected top candidate to be ASSET with 0.95, got %v", top)
	}
}

func TestASENode_IsConfident(t *testing.T) {
	node := NewASENode("t1", "", "default", map[string]any{"raw_description": "desc", "cash_direction": "OUTFLOW", "raw_amount": "-10"})

	// No candidates → not confident.
	if node.IsConfident() {
		t.Error("expected not confident with no candidates")
	}

	// Below threshold.
	node.SetPropertyCandidates("prop_1", []ProbabilityCandidate{{Value: "val_a", Confidence: 1.0}})
	node.SetPropertyCandidates("prop_2", []ProbabilityCandidate{{Value: "val_b", Confidence: 1.0}})
	node.SetPropertyCandidates("prop_3", []ProbabilityCandidate{{Value: "val_c", Confidence: 1.0}})
	node.SetPropertyCandidates("prop_4", []ProbabilityCandidate{
		{Value: "val_d1", Confidence: 0.7},
		{Value: "val_d2", Confidence: 0.3},
	})
	if node.IsConfident() {
		t.Errorf("expected not confident at final score (unified: %f)", node.UnifiedConfidence)
	}

	// At threshold (perfect confidence).
	node.SetPropertyCandidates("prop_4", []ProbabilityCandidate{{Value: "val_d1", Confidence: 1.0}})
	if !node.IsConfident() {
		t.Errorf("expected confident at 0.98 final score (unified: %f)", node.UnifiedConfidence)
	}

	// Above threshold.
	node.SetPropertyCandidates("prop_4", []ProbabilityCandidate{{Value: "val_d1", Confidence: 0.99}})
	if !node.IsConfident() {
		t.Errorf("expected confident at 0.99 final score (unified: %f)", node.UnifiedConfidence)
	}
}

func TestASENode_SetCandidates_IncrementsProbes(t *testing.T) {
	node := NewASENode("t1", "", "default", map[string]any{"raw_description": "desc", "cash_direction": "OUTFLOW", "raw_amount": "-10"})
	if node.LifetimeProbes != 0 {
		t.Errorf("expected 0 probes, got %d", node.LifetimeProbes)
	}

	node.SetPropertyCandidates("prop_1", []ProbabilityCandidate{
		{Value: "val_a", Confidence: 0.9},
	})
	if node.LifetimeProbes != 1 {
		t.Errorf("expected 1 probe after first set, got %d", node.LifetimeProbes)
	}

	node.SetPropertyCandidates("prop_2", []ProbabilityCandidate{
		{Value: "val_b", Confidence: 0.8},
	})
	if node.LifetimeProbes != 2 {
		t.Errorf("expected 2 probes after second set, got %d", node.LifetimeProbes)
	}
}

func TestASENode_StateTransitions(t *testing.T) {
	node := NewASENode("t1", "", "default", map[string]any{"raw_description": "desc", "cash_direction": "OUTFLOW", "raw_amount": "-10"})

	states := make([]string, 0)
	node.SetOnStateChange(func(n *AutonomousSemanticEngineNode, oldState, newState NodeState) {
		states = append(states, string(newState))
	})

	node.transition(StateTriaging)
	node.transition(StateThinking)

	if len(states) != 2 {
		t.Fatalf("expected 2 state transitions, got %d", len(states))
	}
	if states[0] != string(StateTriaging) || states[1] != string(StateThinking) {
		t.Errorf("unexpected state sequence: %v", states)
	}
	node.Mu.RLock()
	if node.CurrentState != StateThinking {
		t.Errorf("expected current state THINKING, got %s", node.CurrentState)
	}
	node.Mu.RUnlock()
}
