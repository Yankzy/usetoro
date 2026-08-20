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

	if cfg.EntryNode != "bank_cash_ingress" {
		t.Errorf("expected entry_node 'bank_cash_ingress', got '%s'", cfg.EntryNode)
	}

	entryNode := dag.GetNode("bank_cash_ingress")
	if entryNode == nil {
		t.Fatal("bank_cash_ingress node not found in DAG")
	}

	classifierNode := dag.GetNode("bank_transaction_classifier")
	if classifierNode == nil {
		t.Fatal("bank_transaction_classifier node not found in DAG")
	}

	feeSplitterNode := dag.GetNode("bank_fee_agios_splitter")
	if feeSplitterNode == nil {
		t.Fatal("bank_fee_agios_splitter node not found in DAG")
	}

	transitNode := dag.GetNode("transit_reconciler_node")
	if transitNode == nil {
		t.Fatal("transit_reconciler_node node not found in DAG")
	}

	rasNode := dag.GetNode("ras_tax_evaluator")
	if rasNode == nil {
		t.Fatal("ras_tax_evaluator node not found in DAG")
	}

	// Verify statutory annotations on holding gates
	feeHold := dag.GetNode("hold_ambiguous_bank_fee")
	if feeHold == nil || feeHold.Annotation == nil {
		t.Fatal("hold_ambiguous_bank_fee missing annotation")
	}
	if feeHold.Annotation.TaxRuleCode != "CGI Art. 89 (TVA Bancaire 10%)" {
		t.Errorf("expected CGI Art. 89, got %s", feeHold.Annotation.TaxRuleCode)
	}

	transitHold := dag.GetNode("hold_unmatched_transit_pair")
	if transitHold == nil || transitHold.Annotation == nil {
		t.Fatal("hold_unmatched_transit_pair missing annotation")
	}
	if transitHold.Annotation.TaxRuleCode != "PCGM Compte 5115 (Virements de fonds)" {
		t.Errorf("expected PCGM Compte 5115, got %s", transitHold.Annotation.TaxRuleCode)
	}

	iceHold := dag.GetNode("hold_invalid_ice")
	if iceHold == nil || iceHold.Annotation == nil {
		t.Fatal("hold_invalid_ice missing annotation")
	}
	if iceHold.Annotation.Severity != "BLOCKING" {
		t.Errorf("expected BLOCKING severity, got %s", iceHold.Annotation.Severity)
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

	holdFeeNode := dag.GetNode("hold_ambiguous_bank_fee")
	if holdFeeNode == nil {
		t.Fatal("hold_ambiguous_bank_fee node not found")
	}

	node := NewASENode("t-bank", "pcm_bank", map[string]any{
		"raw_description": "COMMISSIONS SUR EFFET 120 MAD",
		"raw_amount":      "-120.00",
		"cash_direction":  "OUTFLOW",
	})

	holdFeeNode.Accept(node)
	time.Sleep(100 * time.Millisecond)

	if node.GetState() != "HOLD_AMBIGUOUS_BANK_FEE" {
		t.Errorf("expected state HOLD_AMBIGUOUS_BANK_FEE, got %s", node.GetState())
	}

	node.Mu.RLock()
	taxRule, _ := node.Payload["tax_rule_code"].(string)
	docReq, _ := node.Payload["document_required"].(string)
	severity, _ := node.Payload["severity"].(string)
	instruction, _ := node.Payload["instruction"].(string)
	node.Mu.RUnlock()

	if taxRule != "CGI Art. 89 (TVA Bancaire 10%)" {
		t.Errorf("expected tax_rule_code %q, got %q", "CGI Art. 89 (TVA Bancaire 10%)", taxRule)
	}
	if docReq != "Avis d'opéré bancaire / Relevé d'agios" {
		t.Errorf("expected document_required 'Avis d'opéré bancaire / Relevé d'agios', got '%s'", docReq)
	}
	if severity != "WARNING" {
		t.Errorf("expected severity 'WARNING', got '%s'", severity)
	}
	if instruction == "" {
		t.Error("expected non-empty instruction in node payload")
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



