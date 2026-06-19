package ase

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestDAGNode_AcceptAndFlush(t *testing.T) {
	logger := testLogger()
	dagNode := NewDAGNode("test_macro", "account_selection", "Test Terminal", 3, 50*time.Millisecond, logger, DAGNodeConfig{})

	var mu sync.Mutex
	var processedBatches [][]*AutonomousSemanticEngineNode
	dagNode.SetThinkFunc(func(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
		mu.Lock()
		processedBatches = append(processedBatches, batch)
		mu.Unlock()

		results := make(map[string]NodeClassification, len(batch))
		for _, node := range batch {
			results[node.NodeID] = NodeClassification{
				Property: "account_selection",
				Candidates: []ProbabilityCandidate{
					{Value: "EXPENSE", Confidence: 1.0, Reasoning: "test"},
				},
			}
		}
		return results, nil
	})

	dagNode.Start()
	defer dagNode.Stop()

	// Accept 3 nodes — should trigger immediate flush (batch size reached).
	node1 := NewASENode("t1", "", "default", "Office supplies", "OUTFLOW", "-100")
	node2 := NewASENode("t2", "", "default", "Client payment", "INFLOW", "500")
	node3 := NewASENode("t3", "", "default", "Software subscription", "OUTFLOW", "-29.99")

	// Pre-fill missing properties so entropy = 0 for them.
	for _, n := range []*AutonomousSemanticEngineNode{node1, node2, node3} {
		n.SetPropertyCandidates("macro_classifier", []ProbabilityCandidate{{Value: "M", Confidence: 1.0}})
		n.SetPropertyCandidates("account_type", []ProbabilityCandidate{{Value: "A", Confidence: 1.0}})
		n.SetPropertyCandidates("entity", []ProbabilityCandidate{{Value: "E", Confidence: 1.0}})
	}

	dagNode.Accept(node1)
	dagNode.Accept(node2)
	dagNode.Accept(node3)

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(processedBatches) < 1 {
		t.Fatal("expected at least 1 processed batch")
	}
	if len(processedBatches[0]) != 3 {
		t.Errorf("expected batch of 3, got %d", len(processedBatches[0]))
	}

	// Terminal node → all nodes should reach StateClassified.
	for _, node := range []*AutonomousSemanticEngineNode{node1, node2, node3} {
		state := node.GetState()
		if state != StateClassified {
			t.Errorf("node %s expected state CLASSIFIED, got %s", node.NodeID, state)
		}
		if len(node.Candidates) != 4 {
			t.Errorf("expected 4 candidates, got %d", len(node.Candidates))
		}
	}
}

func TestDAGNode_FlushOnTimer(t *testing.T) {
	logger := testLogger()
	dagNode := NewDAGNode("test", "account_selection", "Test Terminal", 10, 50*time.Millisecond, logger, DAGNodeConfig{})

	var mu sync.Mutex
	var processedCount int
	dagNode.SetThinkFunc(func(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
		mu.Lock()
		processedCount += len(batch)
		mu.Unlock()

		results := make(map[string]NodeClassification, len(batch))
		for _, node := range batch {
			results[node.NodeID] = NodeClassification{
				Property: "account_selection",
				Candidates: []ProbabilityCandidate{
					{Value: "EXPENSE", Confidence: 1.0, Reasoning: "timer flush"},
				},
			}
		}
		return results, nil
	})

	dagNode.Start()
	defer dagNode.Stop()

	// Accept only 1 node — timer should flush it.
	node := NewASENode("t1", "", "default", "Test", "OUTFLOW", "-10")
	node.SetPropertyCandidates("macro_classifier", []ProbabilityCandidate{{Value: "M", Confidence: 1.0}})
	node.SetPropertyCandidates("account_type", []ProbabilityCandidate{{Value: "A", Confidence: 1.0}})
	node.SetPropertyCandidates("entity", []ProbabilityCandidate{{Value: "E", Confidence: 1.0}})
	dagNode.Accept(node)

	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	if processedCount != 1 {
		t.Errorf("expected 1 processed node from timer flush, got %d", processedCount)
	}
	mu.Unlock()

	if node.GetState() != StateClassified {
		t.Errorf("expected CLASSIFIED, got %s", node.GetState())
	}
}

func TestDAGNode_ThinkFailureResultsInHold(t *testing.T) {
	logger := testLogger()
	dagNode := NewDAGNode("test", "macro_classifier", "Test", 1, 50*time.Millisecond, logger, DAGNodeConfig{})
	dagNode.SetThinkFunc(func(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
		return nil, fmt.Errorf("simulated LLM failure")
	})

	dagNode.Start()
	defer dagNode.Stop()

	node := NewASENode("t1", "", "default", "Test", "OUTFLOW", "-10")
	dagNode.Accept(node)

	time.Sleep(150 * time.Millisecond)

	if node.GetState() != StateHoldMissingCtx {
		t.Errorf("expected StateHoldMissingCtx after Think failure, got %s", node.GetState())
	}
	if node.HoldReason == "" {
		t.Error("expected non-empty HoldReason after failure")
	}
}

func TestDAGNode_Routing(t *testing.T) {
	logger := testLogger()

	// Build a simple 2-level DAG.
	macroNode := NewDAGNode("macro", "macro_classifier", "Macro", 2, 50*time.Millisecond, logger, DAGNodeConfig{})
	expenseNode := NewDAGNode("expense", "account_type", "AccountType-Expense", 2, 50*time.Millisecond, logger, DAGNodeConfig{})
	revenueNode := NewDAGNode("revenue", "account_type", "AccountType-Revenue", 2, 50*time.Millisecond, logger, DAGNodeConfig{})

	macroNode.AddChild("EXPENSE", expenseNode)
	macroNode.AddChild("REVENUE", revenueNode)

	// Macro ThinkFunc: classify first node as EXPENSE, second as REVENUE.
	macroNode.SetThinkFunc(func(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
		results := make(map[string]NodeClassification, len(batch))
		for _, node := range batch {
			if node.RawDescription == "expense item" {
				results[node.NodeID] = NodeClassification{
					Property: "macro_class",
					Candidates: []ProbabilityCandidate{
						{Value: "EXPENSE", Confidence: 1.0, Reasoning: "macro route"},
					},
				}
			} else {
				results[node.NodeID] = NodeClassification{
					Property: "macro_class",
					Candidates: []ProbabilityCandidate{
						{Value: "REVENUE", Confidence: 1.0, Reasoning: "macro route"},
					},
				}
			}
		}
		return results, nil
	})

	// AccountType ThinkFuncs — verify routing happened correctly.
	var expenseNodeProcessed, revenueNodeProcessed atomic.Bool
	expenseNode.SetThinkFunc(func(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
		expenseNodeProcessed.Store(true)
		results := make(map[string]NodeClassification, len(batch))
		for _, node := range batch {
			results[node.NodeID] = NodeClassification{
				Property: "account_type",
				Candidates: []ProbabilityCandidate{
					{Value: "Expense", Confidence: 1.0, Reasoning: "expense child"},
				},
			}
		}
		return results, nil
	})
	revenueNode.SetThinkFunc(func(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
		revenueNodeProcessed.Store(true)
		results := make(map[string]NodeClassification, len(batch))
		for _, node := range batch {
			results[node.NodeID] = NodeClassification{
				Property: "account_type",
				Candidates: []ProbabilityCandidate{
					{Value: "Income", Confidence: 1.0, Reasoning: "revenue child"},
				},
			}
		}
		return results, nil
	})

	macroNode.StartAll()
	defer macroNode.StopAll()

	nodeExpense := NewASENode("t1", "", "default", "expense item", "OUTFLOW", "-50")
	nodeRevenue := NewASENode("t1", "", "default", "revenue item", "INFLOW", "200")

	macroNode.Accept(nodeExpense)
	macroNode.Accept(nodeRevenue)

	time.Sleep(300 * time.Millisecond)

	if !expenseNodeProcessed.Load() {
		t.Error("expense child DAG node was not processed")
	}
	if !revenueNodeProcessed.Load() {
		t.Error("revenue child DAG node was not processed")
	}
}

func TestDAGNode_NoChildResultsInHold(t *testing.T) {
	logger := testLogger()

	dagNode := NewDAGNode("macro", "macro_classifier", "Macro", 1, 50*time.Millisecond, logger, DAGNodeConfig{})
	// No children added — routing has nowhere to go.
	dagNode.SetThinkFunc(func(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
		return map[string]NodeClassification{
			batch[0].NodeID: {Property: "macro_class", Candidates: []ProbabilityCandidate{{Value: "EQUITY", Confidence: 1.0, Reasoning: "no child"}}},
		}, nil
	})

	dagNode.Start()
	defer dagNode.Stop()

	node := NewASENode("t1", "", "default", "Owner draw", "OUTFLOW", "-5000")
	dagNode.Accept(node)

	time.Sleep(150 * time.Millisecond)

	state := node.GetState()
	if state != StateHoldMissingCtx {
		t.Errorf("expected StateHoldMissingCtx for unroutable classification, got %s", state)
	}
}

func TestDAG_TerminalNodeCollapse(t *testing.T) {
	logger := testLogger()

	terminalNode := NewDAGNode("terminal", "account_selection", "AccountSelection", 1, 50*time.Millisecond, logger, DAGNodeConfig{})
	terminalNode.SetThinkFunc(func(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
		results := make(map[string]NodeClassification, len(batch))
		for _, node := range batch {
			results[node.NodeID] = NodeClassification{
				Property: "resolved_account_id",
				Candidates: []ProbabilityCandidate{
					{Value: "Bank", Confidence: 1.0, Reasoning: "terminal account"},
				},
			}
		}
		return results, nil
	})

	terminalNode.Start()
	defer terminalNode.Stop()

	node := NewASENode("t1", "", "default", "Test", "OUTFLOW", "-10")
	node.SetPropertyCandidates("macro_classifier", []ProbabilityCandidate{{Value: "M", Confidence: 1.0}})
	node.SetPropertyCandidates("account_type", []ProbabilityCandidate{{Value: "A", Confidence: 1.0}})
	node.SetPropertyCandidates("entity", []ProbabilityCandidate{{Value: "E", Confidence: 1.0}})
	terminalNode.Accept(node)

	time.Sleep(150 * time.Millisecond)

	// Terminal DAG node should transition to Classified.
	state := node.GetState()
	if state != StateClassified {
		t.Errorf("expected StateClassified for terminal node, got %s", state)
	}
}

func TestBuildDAGFromConfig(t *testing.T) {
	logger := testLogger()
	cfg := DAGConfig{
		EntryNode: "macro_classifier",
		Nodes: map[string]DAGNodeConfig{
			"macro_classifier": {
				Kind: "macro_classifier",
				Children: map[string]string{
					"ASSET":   "account_type_asset",
					"EXPENSE": "account_type_expense",
				},
			},
			"account_type_asset": {
				Kind: "account_type",
			},
			"account_type_expense": {
				Kind: "account_type",
			},
		},
	}
	dag := BuildDAGFromConfig(cfg, logger)

	if dag.EntryNode == nil {
		t.Fatal("expected non-nil entry node")
	}
	if dag.EntryNode.Kind != "macro_classifier" {
		t.Errorf("expected entry node kind macro_classifier, got %s", dag.EntryNode.Kind)
	}

	// Verify the topology: Macro → AccountType
	dag.EntryNode.mu.Lock()
	children := len(dag.EntryNode.children)
	dag.EntryNode.mu.Unlock()

	if children != 2 {
		t.Errorf("expected 2 children, got %d", children)
	}
}

func TestBuildDAGFromConfig_Passthrough(t *testing.T) {
	logger := testLogger()
	cfg := DAGConfig{
		EntryNode: "root",
		Nodes: map[string]DAGNodeConfig{
			"root": {
				Kind: "account_selection",
				Children: map[string]string{
					"group_a": "group_node",
				},
			},
			"group_node": {
				Kind: "passthrough",
				Children: map[string]string{
					"leaf_1": "leaf_node_1",
					"leaf_2": "leaf_node_2",
				},
			},
			"leaf_node_1": {
				Kind: "terminal",
			},
			"leaf_node_2": {
				Kind: "terminal",
			},
		},
	}

	dag := BuildDAGFromConfig(cfg, logger)

	if dag.EntryNode == nil {
		t.Fatal("expected non-nil entry node")
	}

	// Verify that "root" directly maps to leaf_node_1 and leaf_node_2
	dag.EntryNode.mu.Lock()
	children := dag.EntryNode.children
	dag.EntryNode.mu.Unlock()

	if len(children) != 2 {
		t.Fatalf("expected 2 children resolved under root, got %d", len(children))
	}

	child1, ok := children["leaf_1"]
	if !ok || child1.ID != "leaf_node_1" {
		t.Errorf("expected leaf_1 to map to leaf_node_1, got %v", child1)
	}

	child2, ok := children["leaf_2"]
	if !ok || child2.ID != "leaf_node_2" {
		t.Errorf("expected leaf_2 to map to leaf_node_2, got %v", child2)
	}

	// Verify that the passthrough node is still in the nodes map
	passthroughNode := dag.GetNode("group_node")
	if passthroughNode == nil {
		t.Fatal("expected group_node to exist in the DAG nodes list")
	}
}

func TestMoroccanCOAPassthroughCompilation(t *testing.T) {
	logger := testLogger()
	cfg := DAGConfig{
		EntryNode: "class_0_special_accounts",
		Nodes: map[string]DAGNodeConfig{
			"class_0_special_accounts": {
				Kind: "account_type",
				Children: map[string]string{
					"OPENING BALANCE SHEET": "opening_balance_sheet_01",
				},
			},
			"opening_balance_sheet_01": {
				Kind: "account_type",
				Children: map[string]string{
					"Reopening of permanent financing accounts": "account_11",
				},
			},
			"account_11": {
				Kind: "passthrough",
				Children: map[string]string{
					"Reopening of equity accounts": "account_0111",
					"Reopening of assimilated equity accounts": "account_0113",
				},
			},
			"account_0111": {
				Kind: "terminal",
			},
			"account_0113": {
				Kind: "terminal",
			},
		},
	}

	dag := BuildDAGFromConfig(cfg, logger)

	if dag.EntryNode == nil {
		t.Fatal("expected non-nil entry node")
	}
	if dag.EntryNode.ID != "class_0_special_accounts" {
		t.Errorf("expected entry node class_0_special_accounts, got %s", dag.EntryNode.ID)
	}

	subNode := dag.GetNode("opening_balance_sheet_01")
	if subNode == nil {
		t.Fatal("expected opening_balance_sheet_01 to exist")
	}

	subNode.mu.Lock()
	children := subNode.children
	subNode.mu.Unlock()

	if len(children) != 2 {
		t.Fatalf("expected 2 children resolved under opening_balance_sheet_01, got %d", len(children))
	}

	// children keys in memory are lowercased by AddChild
	child1, ok := children["reopening of equity accounts"]
	if !ok || child1.ID != "account_0111" {
		t.Errorf("expected reopening of equity accounts to map to account_0111, got %v", child1)
	}

	child2, ok := children["reopening of assimilated equity accounts"]
	if !ok || child2.ID != "account_0113" {
		t.Errorf("expected reopening of assimilated equity accounts to map to account_0113, got %v", child2)
	}
}

func TestBuildDAGFromConfig_DefaultChild(t *testing.T) {
	logger := testLogger()
	cfg := DAGConfig{
		EntryNode: "root",
		Nodes: map[string]DAGNodeConfig{
			"root": {
				Kind:         "entity",
				DefaultChild: "next_node",
			},
			"next_node": {
				Kind: "terminal",
			},
		},
	}
	dag := BuildDAGFromConfig(cfg, logger)
	if dag.EntryNode == nil {
		t.Fatal("expected non-nil entry node")
	}
	if dag.EntryNode.defaultChild == nil {
		t.Fatal("expected non-nil defaultChild on root node")
	}
	if dag.EntryNode.defaultChild.ID != "next_node" {
		t.Errorf("expected defaultChild ID to be next_node, got %s", dag.EntryNode.defaultChild.ID)
	}
}


type testError struct {
	msg string
}

func (e *testError) Error() string { return e.msg }
