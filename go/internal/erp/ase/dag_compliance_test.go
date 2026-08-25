package ase

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func loadActualDAGConfig(t *testing.T) DAGConfig {
	b, err := os.ReadFile("dags/ase_gaap_us.yml")
	if err != nil {
		t.Fatalf("failed to read ase_gaap_us.yml: %v", err)
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

	node := NewASENode("t1", "default", map[string]any{"raw_description": "Office Supplies $85", "cash_direction": "OUTFLOW", "raw_amount": "-85.00"})
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

	node := NewASENode("t2", "default", map[string]any{"raw_description": "Contractor Payment", "cash_direction": "OUTFLOW", "raw_amount": "-650.00"})
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

	accountSelectionNode := dag.GetNode("account_selection")
	if accountSelectionNode == nil {
		t.Fatal("node account_selection not found in DAG")
	}
	accountSelectionNode.SetThinkFunc(func(ctx context.Context, batch []*AutonomousSemanticEngineNode) (map[string]NodeClassification, error) {
		entityOutflowProcessed.Store(true)
		return map[string]NodeClassification{}, nil
	})

	dag.StartAll()
	defer dag.StopAll()

	node := NewASENode("t3", "default", map[string]any{"raw_description": "Software Subscription", "cash_direction": "OUTFLOW", "raw_amount": "-15.00"})
	routerNode.Accept(node)

	time.Sleep(300 * time.Millisecond)

	if !entityOutflowProcessed.Load() {
		t.Errorf("Transaction did not reach entity_outflow branch for a compliant flow, state: %s, hold_reason: %s", node.GetState(), node.HoldReason)
	}
}

func TestDAG_ComplianceRouting_DefaultInboundEmail(t *testing.T) {
	logger := testLogger()
	b, err := os.ReadFile("dags/default_inbound_email.yml")
	if err != nil {
		b, err = os.ReadFile("go/internal/erp/ase/dags/default_inbound_email.yml")
	}
	if err != nil {
		t.Fatalf("failed to read default_inbound_email.yml: %v", err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(b, &raw); err != nil {
		t.Fatalf("failed to parse default_inbound_email.yml: %v", err)
	}

	dagRaw, ok := raw["dag"]
	if !ok {
		t.Fatal("missing dag key in default_inbound_email.yml")
	}

	j, err := json.Marshal(dagRaw)
	if err != nil {
		t.Fatalf("failed to marshal to json: %v", err)
	}

	var cfg DAGConfig
	if err := json.Unmarshal(j, &cfg); err != nil {
		t.Fatalf("failed to unmarshal into DAGConfig: %v", err)
	}

	dag := BuildDAGFromConfig(cfg, logger)

	entryNode := dag.GetNode("extract_intent")
	if entryNode == nil {
		t.Fatal("extract_intent node not found in default_inbound_email DAG")
	}

	completeNode := dag.GetNode("triage_complete")
	if completeNode == nil {
		t.Fatal("triage_complete node not found in default_inbound_email DAG")
	}

	if len(cfg.Nodes) != 5 {
		t.Errorf("expected 5 nodes in default_inbound_email DAG, got %d", len(cfg.Nodes))
	}
}

func TestDAG_ComplianceRouting_PcmPettyCash(t *testing.T) {
	logger := testLogger()
	b, err := os.ReadFile("dags/pcm_petty_cash_dag.yml")
	if err != nil {
		b, err = os.ReadFile("go/internal/erp/ase/dags/pcm_petty_cash_dag.yml")
	}
	if err != nil {
		t.Fatalf("failed to read pcm_petty_cash_dag.yml: %v", err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(b, &raw); err != nil {
		t.Fatalf("failed to parse pcm_petty_cash_dag.yml: %v", err)
	}

	dagRaw, ok := raw["dag"]
	if !ok {
		t.Fatal("missing dag key in pcm_petty_cash_dag.yml")
	}

	j, err := json.Marshal(dagRaw)
	if err != nil {
		t.Fatalf("failed to marshal to json: %v", err)
	}

	var cfg DAGConfig
	if err := json.Unmarshal(j, &cfg); err != nil {
		t.Fatalf("failed to unmarshal into DAGConfig: %v", err)
	}

	dag := BuildDAGFromConfig(cfg, logger)

	if cfg.EntryNode != "petty_cash_ingress" {
		t.Errorf("expected entry_node 'petty_cash_ingress', got '%s'", cfg.EntryNode)
	}

	entryNode := dag.GetNode("petty_cash_ingress")
	if entryNode == nil {
		t.Fatal("petty_cash_ingress node not found in DAG")
	}

	guardrailsNode := dag.GetNode("petty_cash_guardrails")
	if guardrailsNode == nil {
		t.Fatal("petty_cash_guardrails node not found in DAG")
	}

	classifierNode := dag.GetNode("petty_cash_rubrique_classifier")
	if classifierNode == nil {
		t.Fatal("petty_cash_rubrique_classifier node not found in DAG")
	}

	simplTvaNode := dag.GetNode("simpl_tva_extractor")
	if simplTvaNode == nil {
		t.Fatal("simpl_tva_extractor node not found in DAG")
	}

	holdCaisseNode := dag.GetNode("hold_caisse_creditrice")
	if holdCaisseNode == nil {
		t.Fatal("hold_caisse_creditrice node not found in DAG")
	}
}

func TestDAG_ComplianceRouting_PcmBankCashAccounting(t *testing.T) {
	logger := testLogger()
	b, err := os.ReadFile("dags/pcm_bank_cash_accounting_dag.yml")
	if err != nil {
		b, err = os.ReadFile("go/internal/erp/ase/dags/pcm_bank_cash_accounting_dag.yml")
	}
	if err != nil {
		t.Fatalf("failed to read pcm_bank_cash_accounting_dag.yml: %v", err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(b, &raw); err != nil {
		t.Fatalf("failed to parse pcm_bank_cash_accounting_dag.yml: %v", err)
	}

	dagRaw, ok := raw["dag"]
	if !ok {
		t.Fatal("missing dag key in pcm_bank_cash_accounting_dag.yml")
	}

	j, err := json.Marshal(dagRaw)
	if err != nil {
		t.Fatalf("failed to marshal to json: %v", err)
	}

	var cfg DAGConfig
	if err := json.Unmarshal(j, &cfg); err != nil {
		t.Fatalf("failed to unmarshal into DAGConfig: %v", err)
	}

	dag := BuildDAGFromConfig(cfg, logger)

	if cfg.EntryNode != "direction_router" {
		t.Errorf("expected entry_node 'direction_router', got '%s'", cfg.EntryNode)
	}

	entryNode := dag.GetNode("direction_router")
	if entryNode == nil {
		t.Fatal("direction_router node not found in DAG")
	}

	for _, nodeID := range []string{"bank_transaction_classifier_inflow", "bank_transaction_classifier_outflow", "stage2_treatment_builder", "proposed_accounting_treatment", "human_review", "hold_unreliable_input", "hold_bank_account_configuration", "hold_account_configuration", "hold_unsupported_treatment"} {
		if dag.GetNode(nodeID) == nil {
			t.Fatalf("required Stage-2 node %s not found", nodeID)
		}
	}
	for nodeID, nodeCfg := range cfg.Nodes {
		for edge, child := range nodeCfg.Children {
			if _, ok := cfg.Nodes[child]; !ok {
				t.Fatalf("node %s edge %s references missing child %s", nodeID, edge, child)
			}
		}
		if nodeCfg.DefaultChild != "" {
			if _, ok := cfg.Nodes[nodeCfg.DefaultChild]; !ok {
				t.Fatalf("node %s references missing default child %s", nodeID, nodeCfg.DefaultChild)
			}
		}
		if nodeID == "debug_terminal" || strings.Contains(nodeID, "reconciler") || strings.Contains(nodeID, "posting") {
			t.Fatalf("Stage-2 DAG contains forbidden terminal responsibility %s", nodeID)
		}
	}
}

func TestDAG_PCMBankCashDAG_HoldAnnotationInjection(t *testing.T) {
	logger := testLogger()
	b, err := os.ReadFile("dags/pcm_bank_cash_accounting_dag.yml")
	if err != nil {
		b, err = os.ReadFile("go/internal/erp/ase/dags/pcm_bank_cash_accounting_dag.yml")
	}
	if err != nil {
		t.Fatalf("failed to read pcm_bank_cash_accounting_dag.yml: %v", err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(b, &raw); err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	j, _ := json.Marshal(raw["dag"])
	var cfg DAGConfig
	_ = json.Unmarshal(j, &cfg)

	for k, v := range cfg.Nodes {
		v.BatchSize = 1
		cfg.Nodes[k] = v
	}

	dag := BuildDAGFromConfig(cfg, logger)
	dag.StartAll()
	defer dag.StopAll()

	holdNode := dag.GetNode("hold_bank_account_configuration")
	if holdNode == nil {
		t.Fatal("hold_bank_account_configuration node not found")
	}

	node := NewASENode("t-bank", "pcm_bank", map[string]any{
		"raw_description": "COMMISSIONS SUR EFFET 120 MAD",
		"raw_amount":      "-120.00",
		"cash_direction":  "OUTFLOW",
	})

	holdNode.Accept(node)
	time.Sleep(100 * time.Millisecond)

	if node.GetState() != "HOLD_BANK_ACCOUNT_CONFIGURATION" {
		t.Errorf("expected state HOLD_BANK_ACCOUNT_CONFIGURATION, got %s", node.GetState())
	}
	if !strings.Contains(node.GetHoldReason(), "physical bank account") {
		t.Fatalf("expected explainable bank configuration hold, got %q", node.GetHoldReason())
	}
}

func TestDAG_PCMPettyCashDAG_HoldAnnotationInjection(t *testing.T) {
	logger := testLogger()
	b, err := os.ReadFile("dags/pcm_petty_cash_dag.yml")
	if err != nil {
		b, err = os.ReadFile("go/internal/erp/ase/dags/pcm_petty_cash_dag.yml")
	}
	if err != nil {
		t.Fatalf("failed to read pcm_petty_cash_dag.yml: %v", err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(b, &raw); err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	j, _ := json.Marshal(raw["dag"])
	var cfg DAGConfig
	_ = json.Unmarshal(j, &cfg)

	for k, v := range cfg.Nodes {
		v.BatchSize = 1
		cfg.Nodes[k] = v
	}

	dag := BuildDAGFromConfig(cfg, logger)
	dag.StartAll()
	defer dag.StopAll()

	holdCaisseNode := dag.GetNode("hold_caisse_creditrice")
	if holdCaisseNode == nil {
		t.Fatal("hold_caisse_creditrice node not found")
	}

	node := NewASENode("t-caisse", "pcm_petty_cash", map[string]any{
		"raw_description": "ACHAT FOURNITURES ESPECES 300 MAD",
		"raw_amount":      "-300.00",
		"cash_direction":  "OUTFLOW",
	})

	holdCaisseNode.Accept(node)
	time.Sleep(100 * time.Millisecond)

	if node.GetState() != "HOLD_CAISSE_CREDITRICE_PREVENTED" {
		t.Errorf("expected state HOLD_CAISSE_CREDITRICE_PREVENTED, got %s", node.GetState())
	}

	node.Mu.RLock()
	taxRule, _ := node.Payload["tax_rule_code"].(string)
	docReq, _ := node.Payload["document_required"].(string)
	severity, _ := node.Payload["severity"].(string)
	node.Mu.RUnlock()

	if taxRule != "CGI Art. 210 & 145 (Solde Caisse Créditeur Interdit)" {
		t.Errorf("expected tax_rule_code 'CGI Art. 210 & 145 (Solde Caisse Créditeur Interdit)', got '%s'", taxRule)
	}
	if docReq != "Pièce d'approvisionnement caisse (Retrait bancaire 5115 / Apport)" {
		t.Errorf("expected document_required 'Pièce d'approvisionnement caisse (Retrait bancaire 5115 / Apport)', got '%s'", docReq)
	}
	if severity != "BLOCKING" {
		t.Errorf("expected severity 'BLOCKING', got '%s'", severity)
	}
}
