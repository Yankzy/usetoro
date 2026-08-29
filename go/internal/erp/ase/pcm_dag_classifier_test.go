package ase_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/fixtures"
	"gopkg.in/yaml.v3"
)

func loadPCMTestDAGConfig(t *testing.T) (ase.DAGConfig, map[string]string) {
	// Find path to pcm_bank_cash_accounting_dag.yml
	possiblePaths := []string{
		"dags/pcm_bank_cash_accounting_dag.yml",
		"go/internal/erp/ase/dags/pcm_bank_cash_accounting_dag.yml",
		"pcm_bank_cash_accounting_dag.yml",
	}

	var data []byte
	var err error
	for _, p := range possiblePaths {
		data, err = os.ReadFile(p)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("failed to read pcm_bank_cash_accounting_dag.yml: %v", err)
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	promptsMap := make(map[string]string)
	if pMap, ok := raw["prompts"].(map[string]interface{}); ok {
		for k, v := range pMap {
			switch val := v.(type) {
			case string:
				promptsMap[k] = val
			case []interface{}:
				var parts []string
				for _, part := range val {
					if s, ok := part.(string); ok {
						parts = append(parts, s)
					}
				}
				promptsMap[k] = strings.Join(parts, "\n\n")
			}
		}
	}

	dagRaw, ok := raw["dag"].(map[string]interface{})
	if !ok {
		t.Fatal("missing 'dag' section in YAML")
	}

	// Sanitize execution_parameters for all nodes so map[string]string unmarshals cleanly
	if nodesMap, ok := dagRaw["nodes"].(map[string]interface{}); ok {
		for _, nodeVal := range nodesMap {
			if nodeObj, ok := nodeVal.(map[string]interface{}); ok {
				if execParams, ok := nodeObj["execution_parameters"].(map[string]interface{}); ok {
					stringified := make(map[string]string)
					for pk, pv := range execParams {
						switch val := pv.(type) {
						case string:
							stringified[pk] = val
						default:
							bVal, _ := json.Marshal(val)
							stringified[pk] = string(bVal)
						}
					}
					nodeObj["execution_parameters"] = stringified
				}
			}
		}
	}

	dagBytes, err := json.Marshal(dagRaw)
	if err != nil {
		t.Fatalf("failed to marshal dag section: %v", err)
	}

	var cfg ase.DAGConfig
	if err := json.Unmarshal(dagBytes, &cfg); err != nil {
		t.Fatalf("failed to unmarshal into DAGConfig: %v", err)
	}

	return cfg, promptsMap
}

// TestPCM_All101MockEdgesTopology verifies that every single mock transaction
// corresponds to a valid, reachable child branch on the classifier nodes in the YAML DAG.
func TestPCM_All101MockEdgesTopology(t *testing.T) {
	cfg, _ := loadPCMTestDAGConfig(t)
	allMockTxns, err := fixtures.LoadMockTransactions()
	if err != nil {
		t.Fatalf("failed to load mock transactions: %v", err)
	}

	if len(allMockTxns) != 101 {
		t.Fatalf("expected 101 mock transactions, got %d", len(allMockTxns))
	}

	inflowNode, ok := cfg.Nodes["bank_transaction_classifier_inflow"]
	if !ok {
		t.Fatal("node 'bank_transaction_classifier_inflow' missing from DAG")
	}

	outflowNode, ok := cfg.Nodes["bank_transaction_classifier_outflow"]
	if !ok {
		t.Fatal("node 'bank_transaction_classifier_outflow' missing from DAG")
	}

	for _, tx := range allMockTxns {
		var targetParentNode ase.DAGNodeConfig
		if tx.CashDirection == "INFLOW" {
			targetParentNode = inflowNode
		} else {
			targetParentNode = outflowNode
		}

		childNodeID, exists := targetParentNode.Children[tx.EdgeKey]
		if !exists {
			t.Errorf("Edge '%s' for parent '%s' is not defined in DAG children", tx.EdgeKey, tx.ParentNode)
			continue
		}

		if !strings.EqualFold(childNodeID, tx.ExpectedChildNode) {
			t.Errorf("Edge '%s' routes to '%s' in DAG, but test case expects '%s'", tx.EdgeKey, childNodeID, tx.ExpectedChildNode)
		}

		// Ensure the target child node actually exists in the DAG nodes map
		if _, childExists := cfg.Nodes[childNodeID]; !childExists {
			t.Errorf("Child node '%s' (targeted by edge '%s') does not exist in DAG topology", childNodeID, tx.EdgeKey)
		}
	}
}

// TestPCM_PromptsIncludeAllEdgeKeys verifies that the system prompts contain definitions
// for all target edge keys.
func TestPCM_PromptsIncludeAllEdgeKeys(t *testing.T) {
	_, prompts := loadPCMTestDAGConfig(t)
	allMockTxns, err := fixtures.LoadMockTransactions()
	if err != nil {
		t.Fatalf("failed to load mock transactions: %v", err)
	}

	inflowPrompt := prompts["bank_transaction_classifier_inflow_prompt"]
	outflowPrompt := prompts["bank_transaction_classifier_outflow_prompt"]

	if inflowPrompt == "" {
		t.Fatal("missing prompt 'bank_transaction_classifier_inflow_prompt'")
	}
	if outflowPrompt == "" {
		t.Fatal("missing prompt 'bank_transaction_classifier_outflow_prompt'")
	}

	for _, tx := range allMockTxns {
		activePrompt := outflowPrompt
		if tx.CashDirection == "INFLOW" {
			activePrompt = inflowPrompt
		}

		if !strings.Contains(activePrompt, tx.EdgeKey) {
			t.Errorf("Edge key '%s' (%s) is not referenced in the system prompt", tx.EdgeKey, tx.CashDirection)
		}
	}
}
