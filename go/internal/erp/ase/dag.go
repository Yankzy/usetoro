package ase

import (
	"context"
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

	// Batching
	queue      []*AutonomousSemanticEngineNode
	holding    []*AutonomousSemanticEngineNode
	batchSize  int
	batchFlush time.Duration
	mu         sync.Mutex

	// LLM dispatch function — called during Think phase with a batch of nodes.
	thinkFn ThinkFunc

	// ExecutionParams holds node-level execution configuration (e.g. close_status for terminals).
	ExecutionParams map[string]string `json:"execution_params"`

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
	return &DAGNode{
		ID:                  id,
		Kind:                kind,
		Name:                name,
		AutoAdvance:         cfg.AutoAdvance,
		PromptKey:           cfg.PromptKey,
		EdgeType:            cfg.EdgeType,
		DynamicEdgeProvider: cfg.DynamicEdgeProvider,
		HoldStateSignal:     cfg.HoldStateSignal,
		HoldReasonString:    cfg.HoldReasonString,
		ExecutionParams:     cfg.ExecutionParams,
		queue:               make([]*AutonomousSemanticEngineNode, 0),
		batchSize:           batchSize,
		batchFlush:          batchFlush,
		children:            make(map[string]*DAGNode),
		logger:              logger.With("dag_node", id, "kind", string(kind)),
		ctx:                 ctx,
		cancel:              cancel,
		stopped:             make(chan struct{}),
	}
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
	shouldFlush := len(dn.queue) >= dn.batchSize
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

// flush drains the queue and executes the Think phase for all waiting agents.
func (dn *DAGNode) flush() {
	dn.mu.Lock()
	if len(dn.queue) == 0 || (dn.thinkFn == nil && dn.HoldStateSignal == "" && dn.Kind != "cash_direction_router" && dn.Kind != "terminal") {
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

	// 1) Fast-Path Dead-End Interceptor
	// If this node defines a static hold signal, instantly transition and halt.
	if dn.HoldStateSignal != "" {
		for _, node := range batch {
			node.mu.Lock()
			node.HoldReason = dn.HoldReasonString
			node.mu.Unlock()
			node.transition(NodeState(dn.HoldStateSignal))
		}
		return
	}

	// 1b) Fast-Path Terminal Nodes
	// Terminal nodes have no thinkFn but carry a close_status execution parameter
	// (e.g. COLLAPSED) to properly finalize the agent.
	if dn.Kind == "terminal" {
		closeStatus := dn.ExecutionParams["close_status"]
		if closeStatus == "" {
			closeStatus = string(StateCollapsed)
		}
		var wg sync.WaitGroup
		for _, node := range batch {
			wg.Add(1)
			go func(n *AutonomousSemanticEngineNode) {
				defer wg.Done()
				n.transition(NodeState(closeStatus))
			}(node)
		}
		wg.Wait()
		return
	}

	// 2) Fast-Path Cash Direction Router
	// Hardware-level bypass of LLM to segregate batches by cash flow direction.
	if dn.Kind == "cash_direction_router" {
		var wg sync.WaitGroup
		for _, node := range batch {
			wg.Add(1)
			go func(n *AutonomousSemanticEngineNode) {
				defer wg.Done()
				n.mu.RLock()
				dir := n.CashDirection
				n.mu.RUnlock()

				// Inject hardware state directly into the classification slot.
				n.SetPropertyCandidates(string(dn.Kind), []ProbabilityCandidate{{
					Value:      dir,
					Confidence: 1.0,
					Reasoning:  "Hardware property routing via cash_direction_router.",
				}})
				n.transition(StateActivating)
				dn.routeToChild(n, string(dn.Kind))
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
			node.mu.Lock()
			approved := node.HumanApproved
			node.mu.Unlock()

			if !approved {
				node.mu.Lock()
				node.HoldReason = fmt.Sprintf("auto_advance disabled at DAG node %s", dn.Name)
				node.mu.Unlock()
				node.transition(StateHoldMissingCtx)
				dn.holding = append(dn.holding, node)
			} else {
				// Clear the flag so it doesn't automatically bypass future nodes
				node.mu.Lock()
				node.HumanApproved = false
				node.mu.Unlock()
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
			node.mu.Lock()
			node.HoldReason = "think phase failed: " + err.Error()
			node.mu.Unlock()
			node.transition(StateHoldMissingCtx)
		}
		return
	}

	// Distribute results to each node.
	for _, node := range batch {
		classification, ok := results[node.NodeID]
		if !ok || len(classification.Candidates) == 0 {
			node.mu.Lock()
			node.HoldReason = "no classification candidates returned from think phase"
			node.mu.Unlock()
			node.transition(StateHoldMissingCtx)
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
		node.transition(StateHoldMissingCtx)
		return
	}

	// Take a snapshot of the candidates for the property to store in the trace.
	node.mu.RLock()
	candidatesCopy := make([]ProbabilityCandidate, len(node.Candidates[propertyKey]))
	copy(candidatesCopy, node.Candidates[propertyKey])
	node.mu.RUnlock()

	// Determine routing key based on this DAG node's kind (even if we hold, we can record what it *would* have been or just use top.Value).
	routeKey := dn.routingKey(top)

	// Append the execution step to the node's trace regardless of whether we hold or not.
	node.AppendExecutionStep(NodeExecutionStep{
		DAGNodeID:    dn.ID,
		Kind:         string(dn.Kind),
		PropertyKey:  propertyKey,
		Candidates:   candidatesCopy,
		SelectedEdge: routeKey,
		Timestamp:    time.Now().UTC(),
	})

	// Enforce strict top candidate confidence guardrail at each routing step.
	threshold := 0.98
	cfg := GetConfig(node.TenantID, node.RealmID, node.DagName)
	if cfg != nil && cfg.HyperParameters.ConfidenceThreshold > 0 {
		threshold = cfg.HyperParameters.ConfidenceThreshold
	}

	if top.Confidence < threshold {
		node.mu.Lock()
		node.HoldReason = fmt.Sprintf("top candidate '%s' confidence (%v) below %v guardrail during routing at %s. AI Reasoning: %s", top.Value, top.Confidence, threshold, dn.Name, top.Reasoning)
		// We can explicitly update UnifiedConfidence to match the failing node's confidence
		// so the UI clearly shows the drop in confidence.
		node.UnifiedConfidence = top.Confidence
		node.mu.Unlock()
		node.transition(StateHoldAmbiguous)
		return
	}

	searchKey := strings.ToLower(routeKey)



	dn.mu.Lock()
	child, exists := dn.children[searchKey]
	if !exists {
		child = dn.defaultChild
	}
	dn.mu.Unlock()

	if child == nil {
		// No downstream DAG node — check if we can collapse.
		if dn.Kind == "account_selection" {
			// Terminal classification stage: ready for final check.
			if node.IsConfident() {
				node.transition(StateClassified)
			} else {
				node.mu.Lock()
				cfg := GetConfig(node.TenantID, node.RealmID, node.DagName)
				if cfg != nil {
					node.HoldReason = fmt.Sprintf("Unified Confidence Score below %v structural threshold.", cfg.HyperParameters.ConfidenceThreshold)
				} else {
					node.HoldReason = "Unified Confidence Score below 0.98 structural threshold."
				}
				node.mu.Unlock()
				node.transition(StateHoldMissingCtx)
			}
		} else {
			node.mu.Lock()
			node.HoldReason = "no downstream DAG node for routing key: " + routeKey
			node.mu.Unlock()
			node.transition(StateHoldMissingCtx)
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
