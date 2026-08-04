package ase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// DAGNodeKind identifies the type of classification stage a DAG node represents.
type DAGNodeKind string

// DAGNode represents a single node in the classification DAG topology.
// Each node accepts transaction agents, batches them, and routes results
// to child nodes.
type DAGNode struct {
	ID                  string      `json:"id"`
	Kind                DAGNodeKind `json:"kind"`
	Name                string      `json:"name"`
	AutoAdvance         *bool       `json:"auto_advance"`
	PromptKey           string      `json:"prompt_key"`
	EdgeType            string      `json:"edge_type"`
	DynamicEdgeProvider string      `json:"dynamic_edge_provider"`
	HoldStateSignal     string      `json:"hold_state_signal"`
	HoldReasonString    string      `json:"hold_reason_string"`
	Email               *EmailTemplate `json:"email"`

	// Batching
	queue      []*AutonomousSemanticEngineNode
	holding    []*AutonomousSemanticEngineNode
	batchSize  int
	batchFlush time.Duration
	mu         sync.Mutex

	// LLM dispatch function — called during Think phase with a batch of nodes.
	thinkFn ThinkFunc

	// ExecutionParams holds node-level execution configuration (e.g. close_status for terminals).
	// ExecutionParams allows DAG nodes to define arbitrary flags that influence Think phase logic
	ExecutionParams map[string]string `json:"execution_params"`

	// ContextConfig provides instructions for how to hydrate vector memory
	ContextConfig map[string]any `json:"context"`

	// RecoveryPolicy defines the sub-tree used by Layer 3 to optimize HOLD recovery
	RecoveryPolicy *DecisionNode `json:"recovery_policy"`

	// Children are downstream DAG nodes keyed by classification result value.
	// e.g., a MacroClassifierNode may have children keyed by "ASSET", "EXPENSE", etc.
	children map[string]*DAGNode
	// defaultChild is used when no specific child matches the routing key.
	defaultChild *DAGNode
	// resumeChild is used for quarantine/holding gates to indicate where to resume processing.
	resumeChild *DAGNode

	logger  *slog.Logger
	ctx     context.Context
	cancel  context.CancelFunc
	stopped chan struct{}
	started bool

	// Callback when a node completes its lifecycle through this DAG node.
	onNodeComplete func(node *AutonomousSemanticEngineNode, result string)
}

// NodeClassification pairs a property key with its classification candidates.
type NodeClassification struct {
	Property   string
	Candidates []ProbabilityCandidate
}

// ThinkFunc is invoked by a DAG node during its Think phase. It receives a batch
// of ASENode records and must return classification results for each.
// The returned map is keyed by NodeID and includes the property key being classified,
// preventing state overwrites when multiple DAG nodes share the same Kind (e.g. holding_gate).
type ThinkFunc func(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error)

// DAG represents the full classification topology.
type DAG struct {
	EntryNode *DAGNode
	Nodes     map[string]*DAGNode
	logger    *slog.Logger
}

// NewDAG creates a new classification DAG with the given entry node.
func NewDAG(entryNode *DAGNode, nodes map[string]*DAGNode, logger *slog.Logger) *DAG {
	return &DAG{
		EntryNode: entryNode,
		Nodes:     nodes,
		logger:    logger,
	}
}

// Route determines the entry point for a transaction agent into the DAG.
func (d *DAG) Route(node *AutonomousSemanticEngineNode) *DAGNode {
	return d.EntryNode
}

// GetNode safely retrieves a DAG node by its ID.
func (d *DAG) GetNode(id string) *DAGNode {
	if d.Nodes == nil {
		return nil
	}
	return d.Nodes[id]
}

// NewDAGNode creates a new DAG node with batching configuration.
func NewDAGNode(id string, kind DAGNodeKind, name string, batchSize int, batchFlush time.Duration, logger *slog.Logger, cfg DAGNodeConfig) *DAGNode {
	ctx, cancel := context.WithCancel(context.Background())
	if name == "" {
		name = id
	}
	dn := &DAGNode{
		ID:                  id,
		Kind:                kind,
		Name:                name,
		AutoAdvance:         cfg.AutoAdvance,
		PromptKey:           cfg.PromptKey,
		EdgeType:            cfg.EdgeType,
		DynamicEdgeProvider: cfg.DynamicEdgeProvider,
		HoldStateSignal:     cfg.HoldStateSignal,
		HoldReasonString:    cfg.HoldReasonString,
		Email:               cfg.Email,
		ExecutionParams:     cfg.ExecutionParams,
		ContextConfig:       cfg.Context,
		RecoveryPolicy:      cfg.RecoveryPolicy,
		queue:               make([]*AutonomousSemanticEngineNode, 0),
		batchSize:           batchSize,
		batchFlush:          batchFlush,
		children:            make(map[string]*DAGNode),
		logger:              logger.With("dag_node", id, "kind", string(kind)),
		ctx:                 ctx,
		cancel:              cancel,
		stopped:             make(chan struct{}),
	}
	if dn.Kind == "holding_gate" && dn.HoldStateSignal == "" && dn.PromptKey == "" {
		dn.HoldStateSignal = "HOLD_AMBIGUOUS"
	}
	return dn
}

// SetThinkFunc assigns the LLM dispatch function for this DAG node's Think phase.
func (dn *DAGNode) SetThinkFunc(fn ThinkFunc) {
	dn.mu.Lock()
	defer dn.mu.Unlock()
	dn.thinkFn = fn
}

// SetOnNodeComplete registers a callback fired when a node finishes processing
// through this DAG node.
func (dn *DAGNode) SetOnNodeComplete(fn func(node *AutonomousSemanticEngineNode, result string)) {
	dn.mu.Lock()
	defer dn.mu.Unlock()
	dn.onNodeComplete = fn
}

// AddChild registers a child DAG node for a specific routing key (e.g., "asset", "expense").
func (dn *DAGNode) AddChild(key string, child *DAGNode) {
	dn.mu.Lock()
	defer dn.mu.Unlock()
	dn.children[strings.ToLower(key)] = child
}

// SetDefaultChild sets the fallback child node when no specific key matches.
func (dn *DAGNode) SetDefaultChild(child *DAGNode) {
	dn.mu.Lock()
	defer dn.mu.Unlock()
	dn.defaultChild = child
}

// ResumeChild returns the resume child node for quarantine/holding gates.
func (dn *DAGNode) ResumeChild() *DAGNode {
	dn.mu.Lock()
	defer dn.mu.Unlock()
	return dn.resumeChild
}

// Accept enqueues a transaction agent into this DAG node for batched processing.
// Once accepted, the agent waits in the node's queue until a batch flush occurs.
func (dn *DAGNode) Accept(node *AutonomousSemanticEngineNode) {
	dn.mu.Lock()
	dn.queue = append(dn.queue, node)
	// Force flush if we're dealing with the debug terminal.
	shouldFlush := (dn.batchSize > 0 && len(dn.queue) >= dn.batchSize) || dn.ID == "debug_terminal" || dn.Name == "debug_terminal" || dn.Kind == "debug_terminal"
	dn.mu.Unlock()

	if shouldFlush {
		go dn.flush()
	}
}

// Start begins the periodic flush loop for this DAG node.
func (dn *DAGNode) Start() {
	dn.mu.Lock()
	if dn.started {
		dn.mu.Unlock()
		return
	}
	dn.started = true
	dn.mu.Unlock()

	go dn.flushLoop()
}

func (dn *DAGNode) flushLoop() {
	defer close(dn.stopped)
	ticker := time.NewTicker(dn.batchFlush)
	defer ticker.Stop()

	for {
		select {
		case <-dn.ctx.Done():
			// Final flush before stopping.
			dn.flush()
			return
		case <-ticker.C:
			dn.flush()
		}
	}
}

// Stop signals the DAG node to stop flushing and waits for completion.
func (dn *DAGNode) Stop() {
	dn.mu.Lock()
	if !dn.started {
		dn.mu.Unlock()
		return
	}
	dn.started = false
	dn.mu.Unlock()

	dn.cancel()
	<-dn.stopped
}

func (dn *DAGNode) flush() {
	dn.mu.Lock()
	if len(dn.queue) == 0 || (dn.thinkFn == nil && dn.HoldStateSignal == "" && dn.Kind != "initial_router" && dn.Kind != "terminal" && dn.Kind != "debug_terminal") {
		dn.mu.Unlock()
		return
	}

	// Extract up to batchSize items to prevent context window overflow.
	extractSize := len(dn.queue)
	if dn.batchSize > 0 && extractSize > dn.batchSize {
		extractSize = dn.batchSize
	}
	batch := make([]*AutonomousSemanticEngineNode, extractSize)
	copy(batch, dn.queue[:extractSize])
	dn.queue = dn.queue[extractSize:]
	needsAnotherFlush := dn.batchSize > 0 && len(dn.queue) >= dn.batchSize
	dn.mu.Unlock()

	if needsAnotherFlush {
		go dn.flush()
	}

	// 0.5) Append entry into trace
	var resumeNodeID string
	if dn.ResumeChild() != nil {
		resumeNodeID = dn.ResumeChild().ID
	}
	for _, node := range batch {
		node.AppendExecutionStep(NodeExecutionStep{
			DAGNodeID:    dn.ID,
			ResumeNodeID: resumeNodeID,
			Kind:         string(dn.Kind),
			Timestamp:    time.Now().UTC(),
		})
	}

	// 1) Fast-Path Dead-End Interceptor
	// If this node defines a static hold signal, instantly transition and halt.
	if dn.HoldStateSignal != "" {
		for _, node := range batch {
			node.Mu.Lock()
			approved := node.HumanApproved
			node.Mu.Unlock()

			if approved {
				// Clear the flag so it doesn't automatically bypass future nodes
				node.Mu.Lock()
				node.HumanApproved = false
				node.Mu.Unlock()
				node.transition(StateActivating)
				dn.routeToChild(node, "")
			} else {
				node.Mu.Lock()
				node.HoldReason = dn.HoldReasonString
				node.Mu.Unlock()
				node.transition(NodeState(dn.HoldStateSignal))
			}
		}
		return
	}

	// 1b) Fast-Path Terminal Nodes
	// Terminal nodes with no thinkFn carry a close_status execution parameter
	// (e.g. COLLAPSED or CLASSIFIED) to properly finalize the agent.
	if (dn.Kind == "terminal" || dn.Kind == "debug_terminal") && dn.thinkFn == nil {
		if dn.ID == "debug_terminal" || dn.Name == "debug_terminal" || dn.Kind == "debug_terminal" {
			printDebugTerminalState(dn, batch)
		}
		closeStatus := dn.ExecutionParams["close_status"]
		if closeStatus == "" {
			if dn.ID == "debug_terminal" || dn.Name == "debug_terminal" || dn.Kind == "debug_terminal" {
				closeStatus = string(StateClassified)
			} else {
				closeStatus = string(StateCollapsed)
			}
		}
		var wg sync.WaitGroup
		for _, node := range batch {
			wg.Add(1)
			go func(n *AutonomousSemanticEngineNode) {
				defer wg.Done()
				n.Mu.Lock()
				n.HoldReason = ""
				n.Mu.Unlock()
				n.transition(NodeState(closeStatus))
			}(node)
		}
		wg.Wait()
		return
	}


	// 3) AutoAdvance Halt
	// If auto_advance is false, halt the node BEFORE thinking, unless it was human approved.
	autoAdv := true
	if len(batch) > 0 {
		node := batch[0]
		if cfg := GetConfig(node.TenantID, node.RealmID, node.DagName); cfg != nil {
			autoAdv = cfg.HyperParameters.AutoAdvance
		}
	} else {
		if cfg := GetConfig("", "", "default"); cfg != nil {
			autoAdv = cfg.HyperParameters.AutoAdvance
		}
	}
	if dn.AutoAdvance != nil {
		autoAdv = *dn.AutoAdvance
	}

	if !autoAdv {
		var thinkingBatch []*AutonomousSemanticEngineNode
		for _, node := range batch {
			node.Mu.Lock()
			approved := node.HumanApproved
			node.Mu.Unlock()

			if !approved {
				node.Mu.Lock()
				node.HoldReason = fmt.Sprintf("auto_advance disabled at DAG node %s", dn.Name)
				node.Mu.Unlock()
				node.transition(StateHoldMissingCtx)
				dn.holding = append(dn.holding, node)
			} else {
				// Clear the flag so it doesn't automatically bypass future nodes
				node.Mu.Lock()
				node.HumanApproved = false
				node.Mu.Unlock()
				// Remove from holding if it was there
				for i, h := range dn.holding {
					if h.NodeID == node.NodeID {
						dn.holding = append(dn.holding[:i], dn.holding[i+1:]...)
						break
					}
				}
				thinkingBatch = append(thinkingBatch, node)
			}
		}
		batch = thinkingBatch
	}

	if len(batch) == 0 {
		return
	}

	// Transition all nodes to Thinking.
	for _, node := range batch {
		node.Mu.Lock()
		node.PromptKey = dn.PromptKey
		node.Mu.Unlock()
		if dn.logger != nil {
			dn.logger.Info("ase_dag: node entered DAG stage", "node_id", node.NodeID, "dag_node", dn.Name, "kind", string(dn.Kind))
		}
		node.transition(StateThinking)
	}

	// Execute Think phase.
	results, err := dn.thinkFn(dn.ctx, batch)
	if err != nil {
		if dn.logger != nil {
			dn.logger.Error("dag node think phase failed",
				"kind", string(dn.Kind),
				"error", err,
			)
		}
		// On failure, transition nodes to HOLD state with error context.
		for _, node := range batch {
			node.Mu.Lock()
			node.HoldReason = "think phase failed: " + err.Error()
			node.Mu.Unlock()
			dn.applyHoldPolicyOrTransition(node, StateHoldMissingCtx)
		}
		return
	}

	// Distribute results to each node.
	for _, node := range batch {
		classification, ok := results[node.NodeID]
		if !ok || len(classification.Candidates) == 0 {
			node.Mu.Lock()
			node.HoldReason = "no classification candidates returned from think phase"
			node.Mu.Unlock()
			dn.applyHoldPolicyOrTransition(node, StateHoldMissingCtx)
			continue
		}

		// Use the LLM-returned property key (or fall back to the DAG node's kind).
		propertyKey := classification.Property
		if propertyKey == "" {
			propertyKey = string(dn.Kind)
		}

		// Apply candidates and recalculate entropy.
		node.SetPropertyCandidates(propertyKey, classification.Candidates)
		node.transition(StateActivating)

		// Route to child DAG node based on top candidate's classification.
		dn.routeToChild(node, propertyKey)
	}
}

// routeToChild forwards a classified node to the appropriate child DAG node
// based on its top candidate's classification value.
func (dn *DAGNode) routeToChild(node *AutonomousSemanticEngineNode, propertyKey string) {
	top := node.TopCandidate(propertyKey)
	if top == nil {
		dn.applyHoldPolicyOrTransition(node, StateHoldMissingCtx)
		return
	}

	// Take a snapshot of the candidates for the property to store in the trace.
	node.Mu.RLock()
	candidatesCopy := make([]ProbabilityCandidate, len(node.Candidates[propertyKey]))
	copy(candidatesCopy, node.Candidates[propertyKey])
	node.Mu.RUnlock()

	// Determine routing key based on this DAG node's kind (even if we hold, we can record what it *would* have been or just use top.Value).
	routeKey := dn.routingKey(top)

	// Update the execution step with the results of the routing decision.
	node.UpdateLastExecutionStep(propertyKey, candidatesCopy, routeKey)

	searchKey := strings.ToLower(routeKey)

	dn.mu.Lock()
	child, exists := dn.children[searchKey]
	if !exists {
		child = dn.defaultChild
	}
	if child == nil {
		if debugChild, ok := dn.children["debug_terminal"]; ok {
			child = debugChild
		}
	}
	dn.mu.Unlock()

	// Enforce strict top candidate confidence guardrail at each routing step (unless routing to debug_terminal).
	isDebugTarget := (child != nil && (child.ID == "debug_terminal" || child.Name == "debug_terminal" || child.Kind == "debug_terminal")) || strings.EqualFold(routeKey, "debug_terminal")
	if !isDebugTarget {
		threshold := 0.98
		cfg := GetConfig(node.TenantID, node.RealmID, node.DagName)
		if cfg != nil && cfg.HyperParameters.ConfidenceThreshold > 0 {
			threshold = cfg.HyperParameters.ConfidenceThreshold
		}

		if top.Confidence < threshold && !strings.HasPrefix(strings.ToUpper(routeKey), "HOLD_") {
			node.Mu.Lock()
			node.HoldReason = fmt.Sprintf("top candidate '%s' confidence (%v) below %v guardrail during routing at %s. AI Reasoning: %s", top.Value, top.Confidence, threshold, dn.Name, top.Reasoning)
			// We can explicitly update UnifiedConfidence to match the failing node's confidence
			// so the UI clearly shows the drop in confidence.
			node.UnifiedConfidence = top.Confidence
			node.Mu.Unlock()
			dn.applyHoldPolicyOrTransition(node, StateHoldAmbiguous)
			return
		}
	}

	if child != nil && dn.logger != nil {
		dn.logger.Info("ase_dag: node routing to next DAG stage", "node_id", node.NodeID, "from", dn.Name, "to", child.Name, "route_key", routeKey)
	}

	if child == nil {
		// No downstream DAG node — check if we can collapse.
		if dn.Kind == "terminal" || dn.Kind == "debug_terminal" || dn.ID == "debug_terminal" || dn.Name == "debug_terminal" {
			if dn.logger != nil {
				dn.logger.Info("ase_dag: node reached terminal stage", "node_id", node.NodeID, "dag_node", dn.Name)
			}
			// Terminal classification stage: ready for final check.
			if node.IsConfident() || dn.ID == "debug_terminal" || dn.Name == "debug_terminal" || dn.Kind == "debug_terminal" {
				closeStatus := dn.ExecutionParams["close_status"]
				if closeStatus != "" {
					node.transition(NodeState(closeStatus))
				} else {
					node.transition(StateClassified)
				}
			} else {
				node.Mu.Lock()
				cfg := GetConfig(node.TenantID, node.RealmID, node.DagName)
				if cfg != nil {
					node.HoldReason = fmt.Sprintf("Unified Confidence Score below %v structural threshold.", cfg.HyperParameters.ConfidenceThreshold)
				} else {
					node.HoldReason = "Unified Confidence Score below 0.98 structural threshold."
				}
				node.Mu.Unlock()
				dn.applyHoldPolicyOrTransition(node, StateHoldMissingCtx)
			}
		} else {
			node.Mu.Lock()
			node.HoldReason = "no downstream DAG node for routing key: " + routeKey
			node.Mu.Unlock()
			dn.applyHoldPolicyOrTransition(node, StateHoldMissingCtx)
		}
		return
	}

	// Notify callback if set.
	dn.mu.Lock()
	cb := dn.onNodeComplete
	dn.mu.Unlock()
	if cb != nil {
		cb(node, routeKey)
	}

	// Forward to the child DAG node.
	child.Accept(node)
}

// routingKey extracts the classification value used to route to child DAG nodes.
func (dn *DAGNode) routingKey(candidate *ProbabilityCandidate) string {
	return candidate.Value
}

// resolveChildren recursively traverses child configurations, bypassing any nodes of Kind == "passthrough".
func resolveChildren(nodeID string, nodesMap map[string]*DAGNode, cfgNodes map[string]DAGNodeConfig, logger *slog.Logger) map[string]*DAGNode {
	resolved := make(map[string]*DAGNode)
	nCfg, ok := cfgNodes[nodeID]
	if !ok {
		return resolved
	}

	for key, childID := range nCfg.Children {
		childNode, exists := nodesMap[childID]
		if !exists {
			if logger != nil {
				logger.Warn("DAG routing child not found during resolution", "parent", nodeID, "key", key, "missing_child", childID)
			}
			continue
		}

		if childNode.Kind == "passthrough" {
			// Recursively resolve children of the passthrough node
			passthroughChildren := resolveChildren(childID, nodesMap, cfgNodes, logger)
			for pk, pv := range passthroughChildren {
				resolved[pk] = pv
			}
		} else {
			resolved[key] = childNode
		}
	}
	return resolved
}

// BuildDAGFromConfig dynamically constructs the DAG topology from YAML configuration.
func BuildDAGFromConfig(cfg DAGConfig, logger *slog.Logger) *DAG {
	if len(cfg.Nodes) == 0 {
		logger.Warn("DAGConfig contains no nodes. Building empty DAG.")
		return NewDAG(nil, nil, logger)
	}

	nodesMap := make(map[string]*DAGNode)

	defaultBatchFlush := 5 * time.Second
	if config := GetConfig("", "", "default"); config != nil && config.HyperParameters.BatchFlushSeconds > 0 {
		defaultBatchFlush = time.Duration(config.HyperParameters.BatchFlushSeconds) * time.Second
	}

	// Step 1: Instantiate all nodes without relationships
	for id, nCfg := range cfg.Nodes {
		batchFlush := time.Duration(nCfg.BatchFlushSeconds) * time.Second
		if batchFlush == 0 {
			batchFlush = defaultBatchFlush // fallback default
		}

		node := NewDAGNode(id, DAGNodeKind(nCfg.Kind), nCfg.Name, nCfg.BatchSize, batchFlush, logger, nCfg)
		nodesMap[id] = node
	}

	// Step 2: Wire relationships
	for id, nCfg := range cfg.Nodes {
		node := nodesMap[id]

		// Wire resume child
		if nCfg.ResumeChild != "" {
			resumeNode, exists := nodesMap[nCfg.ResumeChild]
			if !exists {
				logger.Warn("DAG routing resume_child not found", "parent", id, "missing_child", nCfg.ResumeChild)
			} else {
				node.mu.Lock()
				node.resumeChild = resumeNode
				node.mu.Unlock()
			}
		}

		// Map specific children keys, bypassing passthrough nodes at runtime
		if node.Kind != "passthrough" {
			resolved := resolveChildren(id, nodesMap, cfg.Nodes, logger)
			for key, childNode := range resolved {
				node.AddChild(key, childNode)
			}
		}

		// Map default fallback child
		if nCfg.DefaultChild != "" {
			defaultNode, exists := nodesMap[nCfg.DefaultChild]
			if !exists {
				logger.Warn("DAG default routing child not found", "parent", id, "missing_child", nCfg.DefaultChild)
			} else {
				node.SetDefaultChild(defaultNode)
			}
		}
	}

	if cfg.EntryNode == "" {
		if len(nodesMap) == 1 {
			for k := range nodesMap {
				cfg.EntryNode = k
			}
		}
	}

	entryNode, exists := nodesMap[cfg.EntryNode]
	if !exists {
		logger.Error("DAG entry node not found in nodes definition", "entry_node", cfg.EntryNode)
		return NewDAG(nil, nodesMap, logger)
	}

	return NewDAG(entryNode, nodesMap, logger)
}

// StartAll starts the flush loops for the entire DAG topology.
func (dn *DAGNode) StartAll() {
	dn.Start()
	dn.mu.Lock()
	defer dn.mu.Unlock()
	for _, child := range dn.children {
		child.StartAll()
	}
	if dn.defaultChild != nil {
		dn.defaultChild.StartAll()
	}
}

// StopAll stops the flush loops for the entire DAG topology.
func (dn *DAGNode) StopAll() {
	dn.Stop()
	dn.mu.Lock()
	defer dn.mu.Unlock()
	for _, child := range dn.children {
		child.StopAll()
	}
	if dn.defaultChild != nil {
		dn.defaultChild.StopAll()
	}
}

func (d *DAG) StartAll() {
	if d.EntryNode != nil {
		d.EntryNode.StartAll()
	}
}

func (d *DAG) StopAll() {
	if d.EntryNode != nil {
		d.EntryNode.StopAll()
	}
}

// UpdateFromConfig updates the existing DAG nodes in memory with new configuration values.
// This allows hot-reloading properties like batch_size without dropping queues or stopping the flush loops.
func (d *DAG) UpdateFromConfig(cfg DAGConfig, logger *slog.Logger) {
	if d.Nodes == nil {
		return
	}

	// First pass: update properties and clear wiring
	for id, nCfg := range cfg.Nodes {
		node, exists := d.Nodes[id]
		if !exists {
			if logger != nil {
				logger.Warn("DAG node added in hot-reload is ignored", "node", id)
			}
			continue
		}

		node.mu.Lock()
		node.AutoAdvance = nCfg.AutoAdvance
		node.PromptKey = nCfg.PromptKey
		node.EdgeType = nCfg.EdgeType
		node.DynamicEdgeProvider = nCfg.DynamicEdgeProvider
		node.HoldStateSignal = nCfg.HoldStateSignal
		node.HoldReasonString = nCfg.HoldReasonString
		node.ExecutionParams = nCfg.ExecutionParams
		node.ContextConfig = nCfg.Context
		node.batchSize = nCfg.BatchSize
		node.batchFlush = time.Duration(nCfg.BatchFlushSeconds) * time.Second

		// Clear wiring for rewire
		node.children = make(map[string]*DAGNode)
		node.defaultChild = nil
		node.resumeChild = nil
		node.mu.Unlock()
	}

	// Second pass: rewire children
	for id, nCfg := range cfg.Nodes {
		node, exists := d.Nodes[id]
		if !exists {
			continue
		}

		// Wire resume child
		if nCfg.ResumeChild != "" {
			resumeNode, exists := d.Nodes[nCfg.ResumeChild]
			if exists {
				node.mu.Lock()
				node.resumeChild = resumeNode
				node.mu.Unlock()
			}
		}

		// Map specific children keys, bypassing passthrough nodes at runtime
		if node.Kind != "passthrough" {
			resolved := resolveChildren(id, d.Nodes, cfg.Nodes, logger)
			for key, childNode := range resolved {
				node.AddChild(key, childNode)
			}
		}

		// Map default fallback child
		if nCfg.DefaultChild != "" {
			defaultNode, exists := d.Nodes[nCfg.DefaultChild]
			if exists {
				node.SetDefaultChild(defaultNode)
			}
		}

		// Check if the new batch size should trigger an immediate flush
		node.mu.Lock()
		shouldFlush := node.batchSize > 0 && len(node.queue) >= node.batchSize
		node.mu.Unlock()
		if shouldFlush {
			go node.flush()
		}
	}
}

// DAGStats represents a snapshot of the current state of a DAG node.
type DAGStats struct {
	ID            string              `json:"id"`
	Kind          string              `json:"kind"`
	Name          string              `json:"name"`
	QueueLength   int                 `json:"queue_length"`
	HoldingLength int                 `json:"holding_length"`
	BatchSize     int                 `json:"batch_size"`
	Children      map[string]DAGStats `json:"children"`
}

// Stats returns a full recursive snapshot of the DAG topology and queues.
func (d *DAG) Stats() *DAGStats {
	if d.EntryNode == nil {
		return nil
	}
	stats := d.EntryNode.Stats()
	return &stats
}

// Stats returns the snapshot for this node and all its downstream children.
func (dn *DAGNode) Stats() DAGStats {
	dn.mu.Lock()
	queueLen := len(dn.queue)
	holdingLen := len(dn.holding)
	childrenSnapshot := make(map[string]*DAGNode, len(dn.children))
	for k, v := range dn.children {
		childrenSnapshot[k] = v
	}
	defaultChild := dn.defaultChild
	dn.mu.Unlock()

	stats := DAGStats{
		ID:            dn.ID,
		Kind:          string(dn.Kind),
		Name:          dn.Name,
		QueueLength:   queueLen,
		HoldingLength: holdingLen,
		BatchSize:     dn.batchSize,
		Children:      make(map[string]DAGStats),
	}

	for k, child := range childrenSnapshot {
		stats.Children[k] = child.Stats()
	}

	if defaultChild != nil {
		stats.Children["*default*"] = defaultChild.Stats()
	}

	return stats
}

// PopHoldingAgent removes and returns the oldest holding agent, if any.
func (dn *DAGNode) PopHoldingAgent() *AutonomousSemanticEngineNode {
	dn.mu.Lock()
	defer dn.mu.Unlock()
	if len(dn.holding) == 0 {
		return nil
	}
	agent := dn.holding[0]
	dn.holding = dn.holding[1:]
	return agent
}

// printDebugTerminalState prints out the current state, micro-agent execution traces, candidate evaluations,
// and payload for micro-agents reaching the debug_terminal node.
func printDebugTerminalState(dn *DAGNode, batch []*AutonomousSemanticEngineNode) {
	var sb strings.Builder
	sb.WriteString("\n================================================================================\n")
	sb.WriteString(fmt.Sprintf("[DEBUG TERMINAL] DAG Stage Reached: %s (Kind: %s)\n", dn.Name, dn.Kind))
	sb.WriteString(fmt.Sprintf("Batch Size: %d micro-agent(s)\n", len(batch)))
	sb.WriteString("--------------------------------------------------------------------------------\n")
	for i, node := range batch {
		node.Mu.RLock()
		sb.WriteString(fmt.Sprintf("Agent #%d | Node ID: %s | Tenant: %s | Realm: %s | DAG: %s\n", i+1, node.NodeID, node.TenantID, node.RealmID, node.DagName))
		sb.WriteString(fmt.Sprintf("  Current State: %s -> Final State: CLASSIFIED\n", node.CurrentState))
		sb.WriteString(fmt.Sprintf("  Unified Confidence: %.4f | Current Entropy: %.4f\n", node.UnifiedConfidence, node.CurrentEntropy))
		if len(node.Candidates) > 0 {
			sb.WriteString("  Property Candidates:\n")
			for prop, cands := range node.Candidates {
				sb.WriteString(fmt.Sprintf("    - %s:\n", prop))
				for _, c := range cands {
					sb.WriteString(fmt.Sprintf("        * %s (confidence: %.4f) [reasoning: %s]\n", c.Value, c.Confidence, c.Reasoning))
				}
			}
		}
		if len(node.ExecutionTrace) > 0 {
			sb.WriteString(fmt.Sprintf("  Execution Trace (%d step(s)):\n", len(node.ExecutionTrace)))
			for stepIdx, step := range node.ExecutionTrace {
				sb.WriteString(fmt.Sprintf("    Step %d: DAG Node: %s (Kind: %s) -> Selected Edge: %s [Timestamp: %s]\n",
					stepIdx+1, step.DAGNodeID, step.Kind, step.SelectedEdge, step.Timestamp.Format(time.RFC3339)))
			}
		}
		if len(node.Payload) > 0 {
			if payloadBytes, err := json.Marshal(node.Payload); err == nil {
				sb.WriteString(fmt.Sprintf("  Payload: %s\n", string(payloadBytes)))
			}
		}
		node.Mu.RUnlock()
		sb.WriteString("--------------------------------------------------------------------------------\n")
	}
	sb.WriteString("================================================================================\n")
	fmt.Print(sb.String())

	if dn.logger != nil {
		dn.logger.Info("[DEBUG TERMINAL] DAG state printed and micro-agents marked as finished",
			"dag_node", dn.Name,
			"batch_size", len(batch),
		)
	}
}

// applyHoldPolicyOrTransition evaluates the RecoveryPolicy decision sub-tree if present.
// If a native action resolves the HOLD, it is executed and the node is re-queued.
// If it fails or is non-native, the ActionID is injected into the HoldReason and the node transitions to the fallback state.
func (dn *DAGNode) applyHoldPolicyOrTransition(node *AutonomousSemanticEngineNode, fallbackState NodeState) {
	if dn.RecoveryPolicy != nil && DefaultRecoveryPolicyEngine != nil {
		selectedAction, err := DefaultRecoveryPolicyEngine.EvaluateTree(dn.RecoveryPolicy, node)
		if err == nil && selectedAction != nil {
			if dn.logger != nil {
				dn.logger.Info("ase_dag: Layer 3 Recovery Policy selected action", "node_id", node.NodeID, "action", selectedAction.Action.ActionID, "ev", selectedAction.ExpectedValue)
			}

			if DefaultRecoveryExecutor != nil {
				recovered, execErr := DefaultRecoveryExecutor.ExecuteAction(dn.ctx, selectedAction.Action.ActionID, node)
				if execErr != nil {
					node.Mu.Lock()
					node.HoldReason += fmt.Sprintf(" | Layer 3 execution failed: %v", execErr)
					node.Mu.Unlock()
				} else if recovered {
					if dn.logger != nil {
						dn.logger.Info("ase_dag: node rescued by Layer 3, re-queueing", "node_id", node.NodeID)
					}
					// Rethink with the new context!
					dn.Accept(node)
					return
				}
			}

			// Not natively recovered. Annotate the HoldReason.
			node.Mu.Lock()
			node.HoldReason = fmt.Sprintf("[Layer 3 Action: %s] ", selectedAction.Action.ActionID) + node.HoldReason
			node.Layer3SelectedAction = selectedAction.Action.ActionID
			node.Mu.Unlock()
		} else if err != nil && dn.logger != nil {
			dn.logger.Warn("ase_dag: Layer 3 Recovery Policy evaluation failed", "error", err)
		}
	}

	node.transition(fallbackState)
}
