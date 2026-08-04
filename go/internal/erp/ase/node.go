// Package ase implements the Autonomous Semantic Engine — an event-driven,
// concurrent agentic architecture where individual payloads are autonomous micro-agents
// that manage their own classification state and entropy.
package ase

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// NodeState represents the lifecycle phase of an autonomous micro-agent.
type NodeState string

const (
	StateUninitialized  NodeState = "UNINITIALIZED"
	StateTriaging       NodeState = "TRIAGING"
	StateThinking       NodeState = "THINKING"
	StateActivating     NodeState = "ACTIVATING"
	StateClassified     NodeState = "CLASSIFIED"
	StateHoldAmbiguous  NodeState = "HOLD_AMBIGUOUS"
	StateHoldMissingCtx NodeState = "HOLD_MISSING_CONTEXT"
	StateReadyForSync   NodeState = "READY_FOR_SYNC"
	StateCollapsed      NodeState = "COLLAPSED"
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
	ResumeNodeID string                 `json:"resume_node_id,omitempty"`
	Kind         string                 `json:"kind"`
	PropertyKey  string                 `json:"property_key,omitempty"`
	Candidates   []ProbabilityCandidate `json:"candidates,omitempty"`
	SelectedEdge string                 `json:"selected_edge,omitempty"`
	Timestamp    time.Time              `json:"timestamp"`
}

// AutonomousSemanticEngineNode represents an individual autonomous micro-agent.
// Each node manages its own classification lifecycle, entropy state, and routing
// through the DAG topology.
type AutonomousSemanticEngineNode struct {
	// Identity
	NodeID   string `json:"node_id"`
	TenantID string `json:"tenant_id"`
	RealmID  string `json:"realm_id"`
	DagName   string `json:"dag_name"`
	PromptKey string `json:"prompt_key,omitempty"`

	// Source data
	Payload        map[string]any `json:"payload"`
	ContextUpdates []string       `json:"context_updates,omitempty"`

	// State management
	CurrentState      NodeState          `json:"current_state"`
	CurrentEntropy       float64            `json:"current_entropy"`    // Sum of H(k)
	UnifiedConfidence    float64            `json:"unified_confidence"` // C = 1 - (Sum / Total)
	HoldReason           string             `json:"hold_reason,omitempty"`
	Layer3SelectedAction string             `json:"layer3_selected_action,omitempty"`
	PropertyEntropies    map[string]float64 `json:"property_entropies"`

	// Candidates maps property keys directly to their isolated array of choices.
	Candidates map[string][]ProbabilityCandidate `json:"candidates"`

	// Full execution trace of the node's journey through the DAG
	ExecutionTrace []NodeExecutionStep `json:"execution_trace"`

	// Lifecycle tracking
	LifetimeProbes int       `json:"lifetime_probes"`
	HumanApproved  bool      `json:"human_approved"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	// Internal
	Mu            sync.RWMutex
	logger        *slog.Logger
	onStateChange func(node *AutonomousSemanticEngineNode, oldState, newState NodeState)
	stateChan     chan struct{}
	Persister     StatePersister
}

// NewASENode creates a new autonomous micro-agent from a staging payload.
func NewASENode(tenantID, realmID, dagName string, payload map[string]any) *AutonomousSemanticEngineNode {
	now := time.Now().UTC()
	nodeID := uuid.New().String()

	cfg := GetConfig(tenantID, realmID, dagName)
	expectedProperties := 4 // Default initial entropy fallback
	if cfg != nil && cfg.HyperParameters.ExpectedProperties > 0 {
		expectedProperties = cfg.HyperParameters.ExpectedProperties
	}
	initialEntropy := float64(expectedProperties) * 1.0

	return &AutonomousSemanticEngineNode{
		NodeID:            nodeID,
		TenantID:          tenantID,
		RealmID:           realmID,
		DagName:           dagName,
		Payload:           payload,
		ContextUpdates:    make([]string, 0),
		CurrentState:      StateUninitialized,
		CurrentEntropy:    initialEntropy, // Maximum entropy at birth
		UnifiedConfidence: 0.0,
		PropertyEntropies: make(map[string]float64),
		Candidates:        make(map[string][]ProbabilityCandidate),
		ExecutionTrace:    make([]NodeExecutionStep, 0),
		LifetimeProbes:    0,
		CreatedAt:         now,
		UpdatedAt:         now,
		logger:            slog.Default().With("component", "ase.node", "node_id", nodeID),
		stateChan:         make(chan struct{}, 1),
		Persister:         nil,
	}
}

// SetLogger assigns a structured logger to this node.
func (n *AutonomousSemanticEngineNode) SetLogger(logger *slog.Logger) {
	n.Mu.Lock()
	defer n.Mu.Unlock()
	n.logger = logger.With("component", "ase.node", "node_id", n.NodeID)
}

// SetOnStateChange registers a callback invoked on every state transition.
func (n *AutonomousSemanticEngineNode) SetOnStateChange(fn func(node *AutonomousSemanticEngineNode, oldState, newState NodeState)) {
	n.Mu.Lock()
	defer n.Mu.Unlock()
	n.onStateChange = fn
}

func (n *AutonomousSemanticEngineNode) AppendExecutionStep(step NodeExecutionStep) {
	n.Mu.Lock()
	if len(n.ExecutionTrace) > 0 {
		if n.ExecutionTrace[len(n.ExecutionTrace)-1].DAGNodeID == step.DAGNodeID {
			n.Mu.Unlock()
			return
		}
	}
	n.ExecutionTrace = append(n.ExecutionTrace, step)
	n.Mu.Unlock()

	if n.Persister != nil {
		n.Persister.PersistNode(context.Background(), n)
	}
}

func (n *AutonomousSemanticEngineNode) UpdateLastExecutionStep(propertyKey string, candidates []ProbabilityCandidate, selectedEdge string) {
	n.Mu.Lock()
	if len(n.ExecutionTrace) > 0 {
		idx := len(n.ExecutionTrace) - 1
		n.ExecutionTrace[idx].PropertyKey = propertyKey
		n.ExecutionTrace[idx].Candidates = candidates
		n.ExecutionTrace[idx].SelectedEdge = selectedEdge
	}
	n.Mu.Unlock()

	if n.Persister != nil {
		n.Persister.PersistNode(context.Background(), n)
	}
}

// AppendContextUpdate safely appends a string to ContextUpdates.
func (n *AutonomousSemanticEngineNode) AppendContextUpdate(update string) {
	n.Mu.Lock()
	defer n.Mu.Unlock()
	n.ContextUpdates = append(n.ContextUpdates, update)
}

// transition updates the node's state and fires the callback.
func (n *AutonomousSemanticEngineNode) transition(newState NodeState) {
	n.Mu.Lock()
	oldState := n.CurrentState
	n.CurrentState = newState
	n.UpdatedAt = time.Now().UTC()
	cb := n.onStateChange
	if n.logger != nil {
		n.logger.Info("ase_node: state transition", "node_id", n.NodeID, "old_state", oldState, "new_state", newState)
	}
	n.Mu.Unlock()

	if cb != nil {
		cb(n, oldState, newState)
	}

	if n.Persister != nil {
		switch newState {
		case StateReadyForSync:
			n.Persister.PersistReadyForSync(context.Background(), n)
		case StateHoldAmbiguous, StateHoldMissingCtx:
			n.Persister.PersistHoldReason(context.Background(), n)
		default:
			n.Persister.PersistNode(context.Background(), n)
		}
	}
}

// GetState returns the current node state.
func (n *AutonomousSemanticEngineNode) GetState() NodeState {
	n.Mu.RLock()
	defer n.Mu.RUnlock()
	return n.CurrentState
}

// GetEntropy returns the current Shannon entropy.
func (n *AutonomousSemanticEngineNode) GetEntropy() float64 {
	n.Mu.RLock()
	defer n.Mu.RUnlock()
	return n.CurrentEntropy
}

// SetPropertyCandidates replaces the classification candidates for a specific property
// stage and recalculates the unified confidence score using the Shannon entropy formula:
// C = 1 - (Sum(H(k)) / Total Properties).
func (n *AutonomousSemanticEngineNode) SetPropertyCandidates(propertyKey string, candidates []ProbabilityCandidate) {
	n.Mu.Lock()
	n.Candidates[propertyKey] = candidates
	n.PropertyEntropies[propertyKey] = CalculateEntropy(candidates)

	expectedProperties := len(n.Candidates)
	if expectedProperties < 1 {
		expectedProperties = 1
	}
	cfg := GetConfig(n.TenantID, n.RealmID, n.DagName)
	if cfg != nil && cfg.HyperParameters.ExpectedProperties > 0 {
		expectedProperties = cfg.HyperParameters.ExpectedProperties
	}

	sumEntropy, unifiedConfidence := CalculateUnifiedConfidence(n.PropertyEntropies, n.Candidates, expectedProperties)

	n.CurrentEntropy = sumEntropy // keep entropy for logging/debugging
	n.UnifiedConfidence = unifiedConfidence
	n.LifetimeProbes++
	n.Mu.Unlock()

	n.UpdatedAt = time.Now().UTC()
}

// TopCandidate returns the candidate with the highest confidence score for a specific property.
// Returns nil if there are no candidates for the property.
func (n *AutonomousSemanticEngineNode) TopCandidate(propertyKey string) *ProbabilityCandidate {
	n.Mu.RLock()
	defer n.Mu.RUnlock()
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
// the State Collapse Guardrail threshold.
func (n *AutonomousSemanticEngineNode) IsConfident() bool {
	n.Mu.RLock()
	defer n.Mu.RUnlock()

	cfg := GetConfig(n.TenantID, n.RealmID, n.DagName)
	threshold := 0.98
	if cfg != nil {
		threshold = cfg.HyperParameters.ConfidenceThreshold
	}

	return n.UnifiedConfidence >= threshold
}

// GetConfidence returns the unified confidence score in a thread-safe manner.
func (n *AutonomousSemanticEngineNode) GetConfidence() float64 {
	n.Mu.RLock()
	defer n.Mu.RUnlock()
	return n.UnifiedConfidence
}

// GetProbes returns the lifetime probe count in a thread-safe manner.
func (n *AutonomousSemanticEngineNode) GetProbes() int {
	n.Mu.RLock()
	defer n.Mu.RUnlock()
	return n.LifetimeProbes
}

// GetHoldReason returns the current hold reason in a thread-safe manner.
func (n *AutonomousSemanticEngineNode) GetHoldReason() string {
	n.Mu.RLock()
	defer n.Mu.RUnlock()
	return n.HoldReason
}

// Run executes the node's lifecycle loop from the very beginning.
func (n *AutonomousSemanticEngineNode) Run(ctx context.Context, dag *DAG, store StatePersister) error {
	return n.Resume(ctx, dag, store, "")
}

// ApproveAndResume manually overrides the auto_advance halt by marking the node as HumanApproved
// and resuming its processing inside the DAG at the provided startNodeID.
func (n *AutonomousSemanticEngineNode) ApproveAndResume(ctx context.Context, dag *DAG, store StatePersister, startNodeID string) error {
	n.Mu.Lock()
	n.HumanApproved = true
	n.HoldReason = ""
	n.Mu.Unlock()
	return n.Resume(ctx, dag, store, startNodeID)
}

// Resume executes the node's lifecycle loop starting from the given DAG node ID.
// If startNodeID is empty, it routes to the DAG's entry node based on its properties.
func (n *AutonomousSemanticEngineNode) Resume(ctx context.Context, dag *DAG, store StatePersister, startNodeID string) error {
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
			n.Mu.Lock()
			n.HoldReason = "target node " + startNodeID + " not found in DAG during Resume"
			n.Mu.Unlock()
			n.transition(StateHoldMissingCtx)
			if store != nil {
				store.PersistHoldReason(ctx, n)
			}
			return nil
		}
	} else {
		// Triage: route to the correct DAG entry node.
		entryNode = dag.Route(n)
		if entryNode == nil {
			n.Mu.Lock()
			n.HoldReason = "no DAG entry node found for initial routing state"
			n.Mu.Unlock()
			n.transition(StateHoldMissingCtx)
			if store != nil {
				store.PersistHoldReason(ctx, n)
			}
			return nil
		}
	}

	// Register with the DAG entry node for Think batching.
	entryNode.Accept(n)

	// Since we are moving to a fully non-blocking asynchronous event-driven model,
	// we do not block here. The agent has been handed off to the DAG's entry node
	// queue. The DAG node's flush loop will handle processing and state transitions,
	// saving to the DB sequentially without racing with a blocking goroutine loop.
	return nil
}



// InitInternalState initializes unexported fields (like channels and loggers)
// that may be nil after the node is deserialized from a datastore.
func (n *AutonomousSemanticEngineNode) InitInternalState() {
	n.Mu.Lock()
	defer n.Mu.Unlock()
	if n.stateChan == nil {
		n.stateChan = make(chan struct{}, 1)
	}
	if n.logger == nil {
		n.logger = slog.Default().With("component", "ase.node", "node_id", n.NodeID)
	}
}
