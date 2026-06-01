// Package ase implements the Autonomous Semantic Engine — an event-driven,
// concurrent agentic architecture where individual transactions are micro-agents
// that manage their own classification state and entropy.
package ase

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// NodeState represents the lifecycle phase of a transaction micro-agent.
type NodeState string

const (
	StateUninitialized    NodeState = "UNINITIALIZED"
	StateTriaging         NodeState = "TRIAGING"
	StateThinking         NodeState = "THINKING"
	StateActivating       NodeState = "ACTIVATING"
	StateClassified       NodeState = "CLASSIFIED"
	StateHoldAmbiguous    NodeState = "HOLD_AMBIGUOUS"
	StateHoldMissingCtx   NodeState = "HOLD_MISSING_CONTEXT"
	StateReadyForSync     NodeState = "READY_FOR_SYNC"
	StateCollapsed        NodeState = "COLLAPSED"
)

// Property Keys for the multi-dimensional Candidates Map
const (
	PropMacroClass   = "macro_class"
	PropAccountType  = "account_type"
	PropCounterparty = "counterparty"
	PropAccountRef   = "resolved_account_id"
)

// ProbabilityCandidate represents a single isolated guess for a specific slot.
type ProbabilityCandidate struct {
	Value      string  `json:"value"`      // e.g., "EXPENSE", "Bank", "Amazon"
	Confidence float64 `json:"confidence"` // Total confidence per property array must equal exactly 1.0
	Reasoning  string  `json:"reasoning,omitempty"`
}

// NodeExecutionStep represents a single decision made by a DAG node.
type NodeExecutionStep struct {
	DAGNodeID    string                 `json:"dag_node_id"`
	Kind         string                 `json:"kind"`
	PropertyKey  string                 `json:"property_key,omitempty"`
	Candidates   []ProbabilityCandidate `json:"candidates,omitempty"`
	SelectedEdge string                 `json:"selected_edge,omitempty"`
	Timestamp    time.Time              `json:"timestamp"`
}

// AutonomousSemanticEngineNode represents an individual transaction micro-agent.
// Each node manages its own classification lifecycle, entropy state, and routing
// through the DAG topology.
type AutonomousSemanticEngineNode struct {
	// Identity
	NodeID    string `json:"node_id"`
	TenantID  string `json:"tenant_id"`
	RealmID   string `json:"realm_id"`

	// Source data
	SourceStatement string  `json:"source_statement"`
	RawDescription  string   `json:"raw_description"`
	CashDirection   string   `json:"cash_direction"` // "INFLOW" or "OUTFLOW"
	RawAmount       string   `json:"raw_amount"`
	ContextUpdates  []string `json:"context_updates,omitempty"`

	// State management
	CurrentState      NodeState          `json:"current_state"`
	CurrentEntropy    float64            `json:"current_entropy"`    // Sum of H(k)
	UnifiedConfidence float64            `json:"unified_confidence"` // C = 1 - (Sum / Total)
	HoldReason        string             `json:"hold_reason,omitempty"`
	PropertyEntropies map[string]float64 `json:"property_entropies"`

	// Candidates maps property keys directly to their isolated array of choices.
	Candidates map[string][]ProbabilityCandidate `json:"candidates"`

	// Full execution trace of the node's journey through the DAG
	ExecutionTrace []NodeExecutionStep `json:"execution_trace"`

	// Lifecycle tracking
	LifetimeProbes int       `json:"lifetime_probes"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	// Internal
	mu       sync.RWMutex
	logger   *slog.Logger
	onStateChange func(node *AutonomousSemanticEngineNode, oldState, newState NodeState)
	stateChan chan struct{}
}

// NewASENode creates a new transaction micro-agent from a staging transaction.
func NewASENode(tenantID, realmID, rawDescription, cashDirection, rawAmount string) *AutonomousSemanticEngineNode {
	now := time.Now().UTC()
	nodeID := uuid.New().String()
	return &AutonomousSemanticEngineNode{
		NodeID:          nodeID,
		TenantID:        tenantID,
		RealmID:         realmID,
		RawDescription:  rawDescription,
		CashDirection:   cashDirection,
		RawAmount:       rawAmount,
		ContextUpdates:  make([]string, 0),
		CurrentState:    StateUninitialized,
		CurrentEntropy:  4.0, // Maximum entropy at birth (4 properties * 1.0)
		UnifiedConfidence: 0.0,
		PropertyEntropies: make(map[string]float64),
		Candidates:      make(map[string][]ProbabilityCandidate),
		ExecutionTrace:  make([]NodeExecutionStep, 0),
		LifetimeProbes:  0,
		CreatedAt:       now,
		UpdatedAt:       now,
		logger:          slog.Default().With("component", "ase.node", "node_id", nodeID),
		stateChan:       make(chan struct{}, 1),
	}
}

// SetLogger assigns a structured logger to this node.
func (n *AutonomousSemanticEngineNode) SetLogger(logger *slog.Logger) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.logger = logger.With("component", "ase.node", "node_id", n.NodeID)
}

// SetOnStateChange registers a callback invoked on every state transition.
func (n *AutonomousSemanticEngineNode) SetOnStateChange(fn func(node *AutonomousSemanticEngineNode, oldState, newState NodeState)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.onStateChange = fn
}

// AppendExecutionStep adds a new step to the node's execution history.
func (n *AutonomousSemanticEngineNode) AppendExecutionStep(step NodeExecutionStep) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ExecutionTrace = append(n.ExecutionTrace, step)
}

// transition updates the node's state and fires the callback.
func (n *AutonomousSemanticEngineNode) transition(newState NodeState) {
	n.mu.Lock()
	oldState := n.CurrentState
	n.CurrentState = newState
	n.UpdatedAt = time.Now().UTC()
	cb := n.onStateChange
	n.mu.Unlock()

	if n.logger != nil {
		n.logger.Info("ase node state transition",
			"old", string(oldState),
			"new", string(newState),
			"entropy", n.GetEntropy(),
		)
	}

	if cb != nil {
		cb(n, oldState, newState)
	}

	select {
	case n.stateChan <- struct{}{}:
	default:
	}
}

// GetState returns the current node state.
func (n *AutonomousSemanticEngineNode) GetState() NodeState {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.CurrentState
}

// GetEntropy returns the current Shannon entropy.
func (n *AutonomousSemanticEngineNode) GetEntropy() float64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.CurrentEntropy
}

// SetPropertyCandidates replaces the classification candidates for a specific property
// stage and recalculates the unified confidence score using the Shannon entropy formula:
// C = 1 - (Sum(H(k)) / Total Properties).
func (n *AutonomousSemanticEngineNode) SetPropertyCandidates(propertyKey string, candidates []ProbabilityCandidate) {
	n.mu.Lock()
	n.Candidates[propertyKey] = candidates
	n.PropertyEntropies[propertyKey] = CalculateEntropy(candidates)

	var sumEntropy float64
	for _, h := range n.PropertyEntropies {
		sumEntropy += h
	}

	// Adapt the denominator to the actual number of properties evaluated.
	// A path may evaluate more than 4 (e.g., cash_direction_router + holding_gates).
	totalProperties := len(n.PropertyEntropies)
	if totalProperties < 4 {
		totalProperties = 4
		// Assume maximum entropy (1.0) for any unseen canonical properties.
		sumEntropy += float64(4 - len(n.PropertyEntropies))
	}

	n.CurrentEntropy = sumEntropy
	n.UnifiedConfidence = 1.0 - (sumEntropy / float64(totalProperties))
	n.LifetimeProbes++
	n.mu.Unlock()
	
	n.UpdatedAt = time.Now().UTC()
}

// TopCandidate returns the candidate with the highest confidence score for a specific property.
// Returns nil if there are no candidates for the property.
func (n *AutonomousSemanticEngineNode) TopCandidate(propertyKey string) *ProbabilityCandidate {
	n.mu.RLock()
	defer n.mu.RUnlock()
	candidates, ok := n.Candidates[propertyKey]
	if !ok || len(candidates) == 0 {
		return nil
	}
	best := &candidates[0]
	for i := 1; i < len(candidates); i++ {
		if candidates[i].Confidence > best.Confidence {
			best = &candidates[i]
		}
	}
	return best
}

// IsConfident returns true if the unified confidence score meets or exceeds
// the State Collapse Guardrail threshold of 0.98.
func (n *AutonomousSemanticEngineNode) IsConfident() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.UnifiedConfidence >= 0.98
}

// GetConfidence returns the unified confidence score in a thread-safe manner.
func (n *AutonomousSemanticEngineNode) GetConfidence() float64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.UnifiedConfidence
}

// GetProbes returns the lifetime probe count in a thread-safe manner.
func (n *AutonomousSemanticEngineNode) GetProbes() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.LifetimeProbes
}

// GetHoldReason returns the current hold reason in a thread-safe manner.
func (n *AutonomousSemanticEngineNode) GetHoldReason() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.HoldReason
}

// Run executes the node's lifecycle loop from the very beginning.
func (n *AutonomousSemanticEngineNode) Run(ctx context.Context, dag *DAG, store *StateStore) error {
	return n.Resume(ctx, dag, store, "")
}

// Resume executes the node's lifecycle loop starting from the given DAG node ID.
// If startNodeID is empty, it routes to the DAG's entry node based on its properties.
func (n *AutonomousSemanticEngineNode) Resume(ctx context.Context, dag *DAG, store *StateStore, startNodeID string) error {
	n.InitInternalState()

	// First save initial state
	if store != nil {
		store.CacheActiveAgent(ctx, n)
	}
	n.transition(StateTriaging)

	var entryNode *DAGNode
	if startNodeID != "" {
		entryNode = dag.GetNode(startNodeID)
		if entryNode == nil {
			n.mu.Lock()
			n.HoldReason = "target node " + startNodeID + " not found in DAG during Resume"
			n.mu.Unlock()
			n.transition(StateHoldMissingCtx)
			if store != nil {
				store.PersistHoldReason(ctx, n)
			}
			return nil
		}
	} else {
		// Triage: route to the correct DAG entry node based on cash direction.
		entryNode = dag.Route(n)
		if entryNode == nil {
			n.mu.Lock()
			n.HoldReason = "no DAG entry node found for cash direction " + n.CashDirection
			n.mu.Unlock()
			n.transition(StateHoldMissingCtx)
			if store != nil {
				store.PersistHoldReason(ctx, n)
			}
			return nil
		}
	}

	// Register with the DAG entry node for Think batching.
	entryNode.Accept(n)

	// Wait for the node to reach a terminal state or context cancellation.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-n.stateChan:
		}

		state := n.GetState()

		// Persist state changes
		if store != nil {
			switch state {
			case StateReadyForSync:
				store.PersistReadyForSync(ctx, n)
			case StateHoldAmbiguous, StateHoldMissingCtx:
				store.PersistHoldReason(ctx, n)
			default:
				store.PersistNode(ctx, n)
			}
			
			switch state {
			case StateCollapsed, StateReadyForSync, StateHoldAmbiguous, StateHoldMissingCtx:
				store.RemoveCachedAgent(ctx, n.NodeID)
			default:
				store.CacheActiveAgent(ctx, n)
			}
		}

		switch state {
		case StateCollapsed, StateReadyForSync, StateHoldAmbiguous, StateHoldMissingCtx:
			return nil
		case StateClassified:
			// Check guardrail: must have confidence >= 0.98 to become READY_FOR_SYNC.
			if n.IsConfident() {
				n.transition(StateReadyForSync)
			} else {
				n.mu.Lock()
				n.HoldReason = "top candidate confidence below 0.98 sync guardrail"
				n.mu.Unlock()
				n.transition(StateHoldAmbiguous)
			}
		default:
			// Any custom HOLD_* state (e.g. HOLD_AMORTIZATION_LOOKUP) is terminal.
			if strings.HasPrefix(string(state), "HOLD_") {
				return nil
			}
		}
	}
}

// CalculateEntropy computes the Shannon entropy of a set of classification
// candidates based on their confidence scores.
//
// H = -Σ(p_i * log₂(p_i))
//
// Confidence scores are normalized to sum to 1.0. If no candidates are provided,
// returns 1.0 (maximum entropy / complete uncertainty).
// If only one candidate exists, returns 0.0 (perfect certainty).
func CalculateEntropy(candidates []ProbabilityCandidate) float64 {
	if len(candidates) == 0 {
		return 1.0
	}
	if len(candidates) == 1 {
		return 0.0
	}

	// Sum all confidence scores.
	var total float64
	for _, c := range candidates {
		total += c.Confidence
	}
	if total == 0 {
		return 1.0
	}

	// Normalize and compute Shannon entropy.
	var entropy float64
	for _, c := range candidates {
		p := c.Confidence / total
		if p > 0 {
			entropy -= p * math.Log2(p)
		}
	}

	// Scale entropy based on the number of options (max entropy is log2(N))
	// This ensures that 5 options properly scales between 0 and 1 instead of overflowing.
	maxEntropy := math.Log2(float64(len(candidates)))
	if maxEntropy > 0 {
		entropy = entropy / maxEntropy
	}

	// Clamp to [0, 1] as a final safety measure.
	if entropy > 1.0 {
		entropy = 1.0
	}
	if entropy < 0.0 {
		entropy = 0.0
	}

	return entropy
}

// InitInternalState initializes unexported fields (like channels and loggers)
// that may be nil after the node is deserialized from a datastore.
func (n *AutonomousSemanticEngineNode) InitInternalState() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stateChan == nil {
		n.stateChan = make(chan struct{}, 1)
	}
	if n.logger == nil {
		n.logger = slog.Default().With("component", "ase.node", "node_id", n.NodeID)
	}
}
