package ase

import (
	"context"
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func loadActualDAGConfig(t *testing.T) DAGConfig {
	b, err := os.ReadFile("ase.yml")
	if err != nil {
		t.Fatalf("failed to read ase.yml: %v", err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(b, &raw); err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	dagRaw, ok := raw["dag"]
	if !ok {
		t.Fatal("missing dag key in ase.yml")
	}

	j, err := json.Marshal(dagRaw)
	if err != nil {
		t.Fatalf("failed to marshal to json: %v", err)
	}

	var cfg DAGConfig
	if err := json.Unmarshal(j, &cfg); err != nil {
		t.Fatalf("failed to unmarshal into DAGConfig: %v", err)
	}

	// Override batch size to 1 for tests to force immediate flush
	for k, v := range cfg.Nodes {
		v.BatchSize = 1
		cfg.Nodes[k] = v
	}

	return cfg
}

func TestDAG_ComplianceRouting_IRS75Receipt(t *testing.T) {
	logger := testLogger()
	cfg := loadActualDAGConfig(t)
	dag := BuildDAGFromConfig(cfg, logger)

	routerNode := dag.GetNode("expense_compliance_router")
	if routerNode == nil {
		t.Fatal("node expense_compliance_router not found in DAG")
	}

	routerNode.SetThinkFunc(func(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
		results := make(map[string]NodeClassification, len(batch))
		for _, node := range batch {
			results[node.NodeID] = NodeClassification{
				Property: "expense_compliance_decision",
				Candidates: []ProbabilityCandidate{
					{Value: "HOLD_MISSING_RECEIPT", Confidence: 0.99, Reasoning: "Test mock > $75"},
				},
			}
		}
		return results, nil
	})

	dag.StartAll()
	defer dag.StopAll()

	node := NewASENode("t1", "", "default", "Office Supplies $85", "OUTFLOW", "-85.00")
	node.HumanApproved = true
	routerNode.Accept(node)

	time.Sleep(300 * time.Millisecond)

	if node.GetState() != "HOLD_MISSING_DOCUMENTATION" {
		t.Errorf("Transaction expected state HOLD_MISSING_DOCUMENTATION, got state: %s, hold_reason: %s", node.GetState(), node.HoldReason)
	}
}

func TestDAG_ComplianceRouting_1099W9Rule(t *testing.T) {
	logger := testLogger()
	cfg := loadActualDAGConfig(t)
	dag := BuildDAGFromConfig(cfg, logger)

	routerNode := dag.GetNode("universal_outflow_compliance_gate")
	if routerNode == nil {
		t.Fatal("node universal_outflow_compliance_gate not found in DAG")
	}

	routerNode.SetThinkFunc(func(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
		results := make(map[string]NodeClassification, len(batch))
		for _, node := range batch {
			results[node.NodeID] = NodeClassification{
				Property: "outflow_compliance_decision",
				Candidates: []ProbabilityCandidate{
					{Value: "HOLD_W9_REQUIRED", Confidence: 0.99, Reasoning: "Service >= $600"},
				},
			}
		}
		return results, nil
	})

	dag.StartAll()
	defer dag.StopAll()

	node := NewASENode("t2", "", "default", "Contractor Payment", "OUTFLOW", "-650.00")
	node.HumanApproved = true
	routerNode.Accept(node)

	time.Sleep(300 * time.Millisecond)

	if node.GetState() != "HOLD_MISSING_DOCUMENTATION" {
		t.Errorf("Transaction expected state HOLD_MISSING_DOCUMENTATION for W-9, got state: %s, hold_reason: %s", node.GetState(), node.HoldReason)
	}
}

func TestDAG_ComplianceRouting_CompliantOutflow(t *testing.T) {
	logger := testLogger()
	cfg := loadActualDAGConfig(t)
	dag := BuildDAGFromConfig(cfg, logger)

	routerNode := dag.GetNode("universal_outflow_compliance_gate")
	if routerNode == nil {
		t.Fatal("node universal_outflow_compliance_gate not found in DAG")
	}

	var entityOutflowProcessed atomic.Bool

	routerNode.SetThinkFunc(func(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
		results := make(map[string]NodeClassification, len(batch))
		for _, node := range batch {
			results[node.NodeID] = NodeClassification{
				Property: "outflow_compliance_decision",
				Candidates: []ProbabilityCandidate{
					{Value: "COMPLIANT_OUTFLOW", Confidence: 0.99, Reasoning: "Valid outflow"},
				},
			}
		}
		return results, nil
	})

	entityNode := dag.GetNode("entity_outflow")
	if entityNode == nil {
		t.Fatal("node entity_outflow not found in DAG")
	}
	entityNode.SetThinkFunc(func(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
		entityOutflowProcessed.Store(true)
		return map[string]NodeClassification{}, nil
	})

	dag.StartAll()
	defer dag.StopAll()

	node := NewASENode("t3", "", "default", "Software Subscription", "OUTFLOW", "-15.00")
	node.HumanApproved = true
	routerNode.Accept(node)

	time.Sleep(300 * time.Millisecond)

	if !entityOutflowProcessed.Load() {
		t.Errorf("Transaction did not reach entity_outflow branch for a compliant flow, state: %s, hold_reason: %s", node.GetState(), node.HoldReason)
	}
}
