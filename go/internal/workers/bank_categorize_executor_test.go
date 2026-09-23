package workers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools"
)

// DeterministicFakeClassifier implements ase.Classifier for testing the executor adapter.
type DeterministicFakeClassifier struct {
	mu           sync.Mutex
	CapturedNode *ase.AutonomousSemanticEngineNode
	CalledStages []string

	DirectionOverride       string
	MacroOverride           string
	AccountCodeOverride     string
	ConfidenceOverride      float64
	ReasoningOverride       string
	OperationalError        error
	OperationalErrorStage   string // "router", "macro", "dynamic", or "" for all

	PayloadEvidenceOverride   any
	DocumentRequiredOverride  string
	CandidateEvidenceOverride []string
}

func NewDeterministicFakeClassifier() *DeterministicFakeClassifier {
	return &DeterministicFakeClassifier{
		ConfidenceOverride: 0.99,
		ReasoningOverride:  "Deterministic test classifier decision",
	}
}

func (f *DeterministicFakeClassifier) BuildPayloadRouterThinkFunc(payloadKey string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		f.mu.Lock()
		f.CalledStages = append(f.CalledStages, "router:"+payloadKey)
		if f.OperationalError != nil && (f.OperationalErrorStage == "" || f.OperationalErrorStage == "router") {
			err := f.OperationalError
			f.mu.Unlock()
			return nil, err
		}
		f.mu.Unlock()

		results := make(map[string]ase.NodeClassification, len(batch))
		for _, node := range batch {
			f.mu.Lock()
			f.CapturedNode = node
			dir := f.DirectionOverride
			f.mu.Unlock()

			if dir == "" {
				if d, ok := node.Payload["direction"].(string); ok && d != "" {
					dir = d
				} else {
					dir = "OUTFLOW"
				}
			}

			results[node.NodeID] = ase.NodeClassification{
				Property: "direction",
				Candidates: []ase.ProbabilityCandidate{
					{
						Value:      dir,
						Confidence: 0.99,
						Reasoning:  "Route based on direction " + dir,
					},
				},
			}
		}
		return results, nil
	}
}

func (f *DeterministicFakeClassifier) BuildGenericThinkFunc(promptKey string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		f.mu.Lock()
		f.CalledStages = append(f.CalledStages, "generic:"+promptKey)
		if f.OperationalError != nil && (f.OperationalErrorStage == "" || f.OperationalErrorStage == "macro") {
			err := f.OperationalError
			f.mu.Unlock()
			return nil, err
		}
		macro := f.MacroOverride
		f.mu.Unlock()

		results := make(map[string]ase.NodeClassification, len(batch))
		for _, node := range batch {
			chosenMacro := macro
			if chosenMacro == "" {
				if strings.Contains(promptKey, "inflow") {
					chosenMacro = "REVENUE"
				} else {
					chosenMacro = "EXPENSE"
				}
			}

			results[node.NodeID] = ase.NodeClassification{
				Property: "macro_class",
				Candidates: []ase.ProbabilityCandidate{
					{
						Value:      chosenMacro,
						Confidence: 0.99,
						Reasoning:  "Classified macro to " + chosenMacro,
					},
				},
			}
		}
		return results, nil
	}
}

func (f *DeterministicFakeClassifier) BuildDynamicThinkFunc(provider string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		f.mu.Lock()
		f.CalledStages = append(f.CalledStages, "dynamic:"+provider)
		if f.OperationalError != nil && (f.OperationalErrorStage == "" || f.OperationalErrorStage == "dynamic") {
			err := f.OperationalError
			f.mu.Unlock()
			return nil, err
		}
		code := f.AccountCodeOverride
		conf := f.ConfidenceOverride
		reasoning := f.ReasoningOverride
		payloadEv := f.PayloadEvidenceOverride
		docReq := f.DocumentRequiredOverride
		candEv := f.CandidateEvidenceOverride
		f.mu.Unlock()

		results := make(map[string]ase.NodeClassification, len(batch))
		for _, node := range batch {
			if payloadEv != nil {
				if node.Payload == nil {
					node.Payload = make(map[string]any)
				}
				node.Payload["required_evidence"] = payloadEv
			}
			if docReq != "" {
				if node.Payload == nil {
					node.Payload = make(map[string]any)
				}
				node.Payload["document_required"] = docReq
			}
			if len(candEv) > 0 {
				cands := make([]ase.ProbabilityCandidate, len(candEv))
				for i, ev := range candEv {
					cands[i] = ase.ProbabilityCandidate{Value: ev, Confidence: 1.0}
				}
				node.Candidates["required_evidence"] = cands
			}

			accCode := code
			if accCode == "" {
				accCode = "401000" // Domain-agnostic generic test account
			}

			results[node.NodeID] = ase.NodeClassification{
				Property: "account_code",
				Candidates: []ase.ProbabilityCandidate{
					{
						Value:      accCode,
						Confidence: conf,
						Reasoning:  reasoning,
					},
				},
			}
		}
		return results, nil
	}
}

func testBankItem(id string, direction string) BankCategorizeItem {
	ref := "REF-" + id
	bName := "Test Checking Account"
	iName := "Global Test Bank"
	cpName := "Acme Corp"

	return BankCategorizeItem{
		BankItemID:          id,
		BankAccountID:       "acc-xyz",
		ResidualAmountUnits: 25000,
		OriginalAmountUnits: 25000,
		Direction:           direction,
		Date:                "2026-03-15",
		Currency:            "MAD",
		Description:         "Payment for invoice #100",
		Reference:           &ref,
		ProvenanceRefs:      []string{"doc-1", "doc-2"},
		BankAccountName:     &bName,
		InstitutionName:     &iName,
		CounterpartyName:    &cpName,
	}
}

func testBankRequest(items ...BankCategorizeItem) BankCategorizeRequest {
	if len(items) == 0 {
		items = []BankCategorizeItem{testBankItem("staged:item-1", "OUTFLOW")}
	}
	return BankCategorizeRequest{
		SchemaVersion:       BankCategorizeSchemaVersion,
		RequestID:           "req-unit-1",
		IdempotencyKey:      "idem-unit-1",
		CompanyID:           "comp-alpha",
		SessionID:           "sess-alpha",
		StateRevision:       1,
		PersistenceRevision: 1,
		DagID:               BankCategorizeDagID,
		RequestedAt:         "2026-03-15T12:00:00Z",
		BankItems:           items,
	}
}

// A. valid BankCategorizeRequest is transformed into ASE nodes
func TestBankCategorizationExecutor_A_RequestTransformedIntoASENodes(t *testing.T) {
	clf := NewDeterministicFakeClassifier()
	exec, err := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clf)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	req := testBankRequest()
	resp, err := exec.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected execute error: %v", err)
	}

	if resp.Status != "COMPLETED" {
		t.Errorf("expected response status COMPLETED, got %q", resp.Status)
	}

	clf.mu.Lock()
	node := clf.CapturedNode
	clf.mu.Unlock()

	if node == nil {
		t.Fatal("expected ASE node to be created and passed to classifier, got nil")
	}

	if node.DagName != BankCategorizeDagID {
		t.Errorf("expected node DagName %q, got %q", BankCategorizeDagID, node.DagName)
	}
	if node.TenantID != req.CompanyID {
		t.Errorf("expected node TenantID %q, got %q", req.CompanyID, node.TenantID)
	}
}

// B. request fields arrive in ASE payload unchanged
func TestBankCategorizationExecutor_B_RequestFieldsArriveInPayloadUnchanged(t *testing.T) {
	clf := NewDeterministicFakeClassifier()
	exec, err := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clf)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	item := testBankItem("staged:item-audit", "OUTFLOW")
	req := testBankRequest(item)

	_, err = exec.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected execute error: %v", err)
	}

	clf.mu.Lock()
	node := clf.CapturedNode
	clf.mu.Unlock()

	if node == nil {
		t.Fatal("expected ASE node to be captured")
	}

	payload := node.Payload
	if payload["bank_item_id"] != item.BankItemID {
		t.Errorf("bank_item_id mismatch: expected %v, got %v", item.BankItemID, payload["bank_item_id"])
	}
	if payload["bank_account_id"] != item.BankAccountID {
		t.Errorf("bank_account_id mismatch: expected %v, got %v", item.BankAccountID, payload["bank_account_id"])
	}
	if payload["residual_amount_units"] != item.ResidualAmountUnits {
		t.Errorf("residual_amount_units mismatch: expected %v, got %v", item.ResidualAmountUnits, payload["residual_amount_units"])
	}
	if payload["original_amount_units"] != item.OriginalAmountUnits {
		t.Errorf("original_amount_units mismatch: expected %v, got %v", item.OriginalAmountUnits, payload["original_amount_units"])
	}
	if payload["direction"] != item.Direction {
		t.Errorf("direction mismatch: expected %v, got %v", item.Direction, payload["direction"])
	}
	if payload["date"] != item.Date {
		t.Errorf("date mismatch: expected %v, got %v", item.Date, payload["date"])
	}
	if payload["currency"] != item.Currency {
		t.Errorf("currency mismatch: expected %v, got %v", item.Currency, payload["currency"])
	}
	if payload["description"] != item.Description {
		t.Errorf("description mismatch: expected %v, got %v", item.Description, payload["description"])
	}
	if payload["reference"] != item.Reference {
		t.Errorf("reference mismatch: expected %v, got %v", item.Reference, payload["reference"])
	}
	if !reflect.DeepEqual(payload["provenance_refs"], item.ProvenanceRefs) {
		t.Errorf("provenance_refs mismatch: expected %v, got %v", item.ProvenanceRefs, payload["provenance_refs"])
	}
	if payload["bank_account_name"] != item.BankAccountName {
		t.Errorf("bank_account_name mismatch: expected %v, got %v", item.BankAccountName, payload["bank_account_name"])
	}
	if payload["institution_name"] != item.InstitutionName {
		t.Errorf("institution_name mismatch: expected %v, got %v", item.InstitutionName, payload["institution_name"])
	}
	if payload["counterparty_name"] != item.CounterpartyName {
		t.Errorf("counterparty_name mismatch: expected %v, got %v", item.CounterpartyName, payload["counterparty_name"])
	}

	// Verify no unexpected fields or expected account codes were injected
	if _, exists := payload["account_code"]; exists {
		t.Error("unexpected account_code truth label present in input payload")
	}
	if _, exists := payload["expected_account_code"]; exists {
		t.Error("unexpected expected_account_code truth label present in input payload")
	}
}

// C. authoritative direction routes through the expected DAG branch
func TestBankCategorizationExecutor_C_DirectionRoutesThroughExpectedBranch(t *testing.T) {
	// 1. OUTFLOW routes through bank_macro_classifier_outflow
	clfOutflow := NewDeterministicFakeClassifier()
	execOutflow, _ := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clfOutflow)
	reqOutflow := testBankRequest(testBankItem("staged:outflow-1", "OUTFLOW"))

	_, err := execOutflow.Execute(context.Background(), reqOutflow)
	if err != nil {
		t.Fatalf("outflow execution error: %v", err)
	}

	clfOutflow.mu.Lock()
	stagesOutflow := strings.Join(clfOutflow.CalledStages, ",")
	clfOutflow.mu.Unlock()

	if !strings.Contains(stagesOutflow, "generic:bank_macro_classifier_outflow") {
		t.Errorf("expected OUTFLOW to visit bank_macro_classifier_outflow, stages visited: %s", stagesOutflow)
	}
	if strings.Contains(stagesOutflow, "generic:bank_macro_classifier_inflow") {
		t.Errorf("OUTFLOW should NOT visit bank_macro_classifier_inflow, stages visited: %s", stagesOutflow)
	}

	// 2. INFLOW routes through bank_macro_classifier_inflow
	clfInflow := NewDeterministicFakeClassifier()
	execInflow, _ := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clfInflow)
	reqInflow := testBankRequest(testBankItem("staged:inflow-1", "INFLOW"))

	_, err = execInflow.Execute(context.Background(), reqInflow)
	if err != nil {
		t.Fatalf("inflow execution error: %v", err)
	}

	clfInflow.mu.Lock()
	stagesInflow := strings.Join(clfInflow.CalledStages, ",")
	clfInflow.mu.Unlock()

	if !strings.Contains(stagesInflow, "generic:bank_macro_classifier_inflow") {
		t.Errorf("expected INFLOW to visit bank_macro_classifier_inflow, stages visited: %s", stagesInflow)
	}
	if strings.Contains(stagesInflow, "generic:bank_macro_classifier_outflow") {
		t.Errorf("INFLOW should NOT visit bank_macro_classifier_outflow, stages visited: %s", stagesInflow)
	}
}

// D. fake classifier can produce CLASSIFIED and it maps correctly
func TestBankCategorizationExecutor_D_ClassifiedOutcomeMapping(t *testing.T) {
	clf := NewDeterministicFakeClassifier()
	clf.AccountCodeOverride = "520100"
	clf.ConfidenceOverride = 0.99
	clf.ReasoningOverride = "High-confidence semantic classification to office supplies"

	exec, _ := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clf)
	item := testBankItem("staged:classified-item", "OUTFLOW")
	req := testBankRequest(item)

	resp, err := exec.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected execute error: %v", err)
	}

	if len(resp.Outcomes) != 1 {
		t.Fatalf("expected 1 outcome, got %d", len(resp.Outcomes))
	}

	outcome := resp.Outcomes[0]
	if outcome.BankItemID != item.BankItemID {
		t.Errorf("bank_item_id mismatch: expected %q, got %q", item.BankItemID, outcome.BankItemID)
	}
	if outcome.Status != "CLASSIFIED" {
		t.Errorf("expected status CLASSIFIED, got %q", outcome.Status)
	}
	if outcome.AccountCode == nil || *outcome.AccountCode != "520100" {
		t.Errorf("account_code mismatch: expected '520100', got %v", outcome.AccountCode)
	}
	if outcome.Confidence == nil || *outcome.Confidence != 0.99 {
		t.Errorf("confidence mismatch: expected 0.99, got %v", outcome.Confidence)
	}
	if outcome.Rationale == nil || *outcome.Rationale != clf.ReasoningOverride {
		t.Errorf("rationale mismatch: expected %q, got %v", clf.ReasoningOverride, outcome.Rationale)
	}
	if outcome.TerminalProperty == nil || *outcome.TerminalProperty != "account_code" {
		t.Errorf("terminal_property mismatch: expected 'account_code', got %v", outcome.TerminalProperty)
	}
	if outcome.AseNodeID == nil || *outcome.AseNodeID != "terminal_classified" {
		t.Errorf("expected ase_node_id 'terminal_classified', got %v", outcome.AseNodeID)
	}
	if outcome.HoldReason != nil {
		t.Errorf("CLASSIFIED outcome must not have hold_reason, got %v", outcome.HoldReason)
	}
	if !reflect.DeepEqual(outcome.EvidenceRefs, item.ProvenanceRefs) {
		t.Errorf("evidence_refs mismatch: expected %v, got %v", item.ProvenanceRefs, outcome.EvidenceRefs)
	}
}

// E. fake classifier can produce HOLD and it maps correctly
func TestBankCategorizationExecutor_E_HoldOutcomeMapping(t *testing.T) {
	// 1. Semantic HOLD from candidate
	clf := NewDeterministicFakeClassifier()
	clf.AccountCodeOverride = "HOLD_INSUFFICIENT_EVIDENCE"
	clf.ReasoningOverride = "Multiple potential matching accounts detected, requires manual review"

	exec, _ := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clf)
	item := testBankItem("staged:hold-item", "OUTFLOW")
	req := testBankRequest(item)

	resp, err := exec.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected execute error: %v", err)
	}

	if len(resp.Outcomes) != 1 {
		t.Fatalf("expected 1 outcome, got %d", len(resp.Outcomes))
	}

	outcome := resp.Outcomes[0]
	if outcome.BankItemID != item.BankItemID {
		t.Errorf("bank_item_id mismatch: expected %q, got %q", item.BankItemID, outcome.BankItemID)
	}
	if outcome.Status != "HOLD" {
		t.Errorf("expected status HOLD, got %q", outcome.Status)
	}
	if outcome.AccountCode != nil {
		t.Errorf("HOLD outcome must not have account_code, got %v", outcome.AccountCode)
	}
	if outcome.HoldReason == nil || *outcome.HoldReason == "" {
		t.Error("HOLD outcome requires non-empty hold_reason")
	}
	if outcome.Rationale == nil || *outcome.Rationale != clf.ReasoningOverride {
		t.Errorf("rationale mismatch: expected %q, got %v", clf.ReasoningOverride, outcome.Rationale)
	}
	if len(outcome.RequiredEvidence) != 0 {
		t.Errorf("generic executor must not invent required_evidence when classifier gives none, got %v", outcome.RequiredEvidence)
	}
	if outcome.AseNodeID == nil || *outcome.AseNodeID != "terminal_hold" {
		t.Errorf("expected ase_node_id 'terminal_hold', got %v", outcome.AseNodeID)
	}

	// 2. Low confidence guardrail triggers HOLD_AMBIGUOUS
	clfLowConf := NewDeterministicFakeClassifier()
	clfLowConf.AccountCodeOverride = "520100"
	clfLowConf.ConfidenceOverride = 0.50 // below 0.98 threshold

	execLowConf, _ := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clfLowConf)
	respLowConf, err := execLowConf.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected execute error for low confidence: %v", err)
	}
	if len(respLowConf.Outcomes) != 1 || respLowConf.Outcomes[0].Status != "HOLD" {
		t.Errorf("expected low confidence item to map to HOLD, got %v", respLowConf.Outcomes)
	}
	if respLowConf.Outcomes[0].AseNodeID == nil || *respLowConf.Outcomes[0].AseNodeID != "account_resolver" {
		t.Errorf("expected low confidence item to report halt node 'account_resolver', got %v", respLowConf.Outcomes[0].AseNodeID)
	}
}

// F. multiple items return exactly one outcome each
// G. response bank_item_id correlation is exact
func TestBankCategorizationExecutor_F_G_MultipleItemsOrderAndCorrelation(t *testing.T) {
	clf := NewDeterministicFakeClassifier()
	exec, _ := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clf)

	items := []BankCategorizeItem{
		testBankItem("staged:batch-1", "OUTFLOW"),
		testBankItem("staged:batch-2", "INFLOW"),
		testBankItem("staged:batch-3", "OUTFLOW"),
	}
	req := testBankRequest(items...)

	resp, err := exec.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected execute error: %v", err)
	}

	if len(resp.Outcomes) != len(items) {
		t.Fatalf("expected exactly %d outcomes, got %d", len(items), len(resp.Outcomes))
	}

	for idx, expectedItem := range items {
		actualOutcome := resp.Outcomes[idx]
		if actualOutcome.BankItemID != expectedItem.BankItemID {
			t.Errorf("item at index %d mismatch: expected %q, got %q", idx, expectedItem.BankItemID, actualOutcome.BankItemID)
		}
		if actualOutcome.Status != "CLASSIFIED" {
			t.Errorf("expected item %s to be CLASSIFIED, got %s", expectedItem.BankItemID, actualOutcome.Status)
		}
	}
}

// H. operational classifier/DAG error returns error, not HOLD
func TestBankCategorizationExecutor_H_OperationalErrorReturnsErrorNotHold(t *testing.T) {
	clf := NewDeterministicFakeClassifier()
	clf.OperationalError = errors.New("network timeout communicating with inference engine")

	exec, _ := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clf)
	req := testBankRequest()

	resp, err := exec.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("expected operational error from Execute, got nil error")
	}

	if !strings.Contains(err.Error(), "network timeout") {
		t.Errorf("expected error to mention 'network timeout', got: %v", err)
	}

	if len(resp.Outcomes) > 0 {
		t.Errorf("operational error must NOT return semantic outcomes, got %d outcomes", len(resp.Outcomes))
	}
}

// I. production executor construction without classifier/domain tool fails closed
func TestBankCategorizationExecutor_I_ConstructionFailsClosed(t *testing.T) {
	// 1. Config with nil classifier and nil resolver fails
	_, err := NewBankCategorizationASEExecutor(BankCategorizationASEExecutorConfig{
		Logger: slog.Default(),
	})
	if err == nil {
		t.Error("expected error constructing executor with nil classifier and nil resolver")
	}

	// 2. Convenience constructor with nil classifier fails
	_, err = NewBankCategorizationASEExecutorWithClassifier(slog.Default(), nil)
	if err == nil {
		t.Error("expected error constructing executor with nil classifier")
	}

	// 3. Domain tool constructor with empty name fails
	_, err = NewBankCategorizationASEExecutorFromDomainTool(slog.Default(), "", domain_tools.ToolDependencies{})
	if err == nil {
		t.Error("expected error constructing executor with empty domain tool name")
	}

	// 4. Domain tool constructor with unregistered tool fails
	_, err = NewBankCategorizationASEExecutorFromDomainTool(slog.Default(), "nonexistent_domain_tool_xyz", domain_tools.ToolDependencies{})
	if err == nil {
		t.Error("expected error constructing executor with unregistered domain tool")
	}
}

// J. executor contains no PCGE codes / Moroccan keyword rules
func TestBankCategorizationExecutor_J_NoPcgeCodesOrMoroccanKeywordRules(t *testing.T) {
	paths := []string{
		"bank_categorize_executor.go",
		"../erp/ase/dags/bookkeeping_bank_categorization_v1.yml",
	}

	prohibitedKeywords := []string{
		// Moroccan PCGE specific codes
		"6111", "6131", "6134", "6136", "6147", "4432", "7111", "4411", "5141",
		// Moroccan keyword heuristics
		"loyer", "salaire", "honoraire", "avocat", "location", "bail", "commission",
		// Domain specific tool references in generic adapter
		"pcm_cash_accounting",
	}

	for _, p := range paths {
		contentBytes, err := os.ReadFile(p)
		if err != nil {
			// Try relative from test runner
			candidate := "internal/workers/" + p
			if b, err2 := os.ReadFile(candidate); err2 == nil {
				contentBytes = b
			} else {
				t.Fatalf("could not read file %s for keyword check: %v", p, err)
			}
		}

		content := strings.ToLower(string(contentBytes))
		for _, kw := range prohibitedKeywords {
			if strings.Contains(content, kw) {
				t.Errorf("file %s contains domain-specific PCGE code or keyword rule: %q", p, kw)
			}
		}
	}
}

// K. executor performs no DB/accounting writes
func TestBankCategorizationExecutor_K_NoDBOrAccountingWrites(t *testing.T) {
	// Inspect struct fields of BankCategorizationASEExecutor via reflection
	execType := reflect.TypeOf(BankCategorizationASEExecutor{})
	for i := 0; i < execType.NumField(); i++ {
		field := execType.Field(i)
		fieldNameLower := strings.ToLower(field.Name)
		fieldTypeLower := strings.ToLower(field.Type.String())

		if strings.Contains(fieldNameLower, "db") || strings.Contains(fieldNameLower, "pool") || strings.Contains(fieldNameLower, "writer") {
			t.Errorf("executor struct contains potential database write handle: %s of type %s", field.Name, field.Type)
		}
		if strings.Contains(fieldTypeLower, "pgx") || strings.Contains(fieldTypeLower, "sql") {
			t.Errorf("executor struct field %s contains SQL/pgx type: %s", field.Name, field.Type)
		}
	}

	// Verify executing does not require DB
	clf := NewDeterministicFakeClassifier()
	exec, err := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clf)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	req := testBankRequest()
	resp, err := exec.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected execution error in DB-free environment: %v", err)
	}
	if len(resp.Outcomes) != 1 {
		t.Fatalf("expected 1 outcome, got %d", len(resp.Outcomes))
	}
}

// L. existing worker boundary tests remain green
func TestBankCategorizationExecutor_L_WorkerBoundaryIntegration(t *testing.T) {
	logger := slog.Default()
	worker := NewBookkeepingAseBankCategorizerWorker(logger, nil, nil)

	store := NewMemoryIdempotencyStore()
	worker.SetIdempotencyStoreForTesting(store)
	worker.SetLeaseDurationForTesting(100 * time.Millisecond)
	worker.SetPollParamsForTesting(5, 10*time.Millisecond)

	clf := NewDeterministicFakeClassifier()
	clf.AccountCodeOverride = "401000"

	exec, err := NewBankCategorizationASEExecutorWithClassifier(logger, clf)
	if err != nil {
		t.Fatalf("failed to construct executor: %v", err)
	}
	worker.SetExecutor(exec)

	req := testBankRequest()
	b, _ := json.Marshal(req)
	msg := &nats.Msg{
		Subject: BankCategorizeSubject,
		Data:    b,
	}

	err = worker.Handle(context.Background(), msg)
	if err != nil {
		t.Fatalf("expected nil error from worker Handle, got: %v", err)
	}

	// Verify idempotency record in store was marked COMPLETED
	digest, err := ComputeBankRequestPayloadDigest(req)
	if err != nil {
		t.Fatalf("failed to compute digest: %v", err)
	}
	kvKey := deriveKVKey(req.CompanyID, req.IdempotencyKey)
	rec, _, err := store.Get(context.Background(), kvKey)
	if err != nil {
		t.Fatalf("failed to retrieve idempotency record: %v", err)
	}
	if rec.Status != IdempotencyStatusCompleted {
		t.Errorf("expected idempotency status COMPLETED, got %q", rec.Status)
	}
	if rec.RequestPayloadDigest != digest {
		t.Errorf("digest mismatch: expected %q, got %q", digest, rec.RequestPayloadDigest)
	}

	// Verify normalized response in record
	var resp BankCategorizeResponse
	if err := json.Unmarshal(rec.NormalizedResponseBytes, &resp); err != nil {
		t.Fatalf("failed to unmarshal cached response: %v", err)
	}
	if len(resp.Outcomes) != 1 {
		t.Fatalf("expected 1 outcome in cached response, got %d", len(resp.Outcomes))
	}
	if resp.Outcomes[0].Status != "CLASSIFIED" {
		t.Errorf("expected outcome status CLASSIFIED, got %q", resp.Outcomes[0].Status)
	}
	if resp.Outcomes[0].AccountCode == nil || *resp.Outcomes[0].AccountCode != "401000" {
		t.Errorf("expected account code '401000', got %v", resp.Outcomes[0].AccountCode)
	}
}

// M. Required evidence forwarded if and only if provided by classifier/ASE
func TestBankCategorizationExecutor_M_RequiredEvidenceForwardedWhenProvided(t *testing.T) {
	t.Run("FromPayloadSlice", func(t *testing.T) {
		clf := NewDeterministicFakeClassifier()
		clf.AccountCodeOverride = "HOLD_INSUFFICIENT_EVIDENCE"
		clf.PayloadEvidenceOverride = []string{"tax_invoice", "purchase_order"}

		exec, err := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clf)
		if err != nil {
			t.Fatalf("failed to construct executor: %v", err)
		}
		resp, err := exec.Execute(context.Background(), testBankRequest(testBankItem("staged:ev-1", "OUTFLOW")))
		if err != nil {
			t.Fatalf("unexpected execution error: %v", err)
		}
		if len(resp.Outcomes) != 1 {
			t.Fatalf("expected 1 outcome, got %d", len(resp.Outcomes))
		}
		expected := []string{"tax_invoice", "purchase_order"}
		if !reflect.DeepEqual(resp.Outcomes[0].RequiredEvidence, expected) {
			t.Errorf("required_evidence mismatch: expected %v, got %v", expected, resp.Outcomes[0].RequiredEvidence)
		}
	})

	t.Run("FromPayloadString", func(t *testing.T) {
		clf := NewDeterministicFakeClassifier()
		clf.AccountCodeOverride = "HOLD_INSUFFICIENT_EVIDENCE"
		clf.PayloadEvidenceOverride = "signed_lease_agreement"

		exec, err := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clf)
		if err != nil {
			t.Fatalf("failed to construct executor: %v", err)
		}
		resp, err := exec.Execute(context.Background(), testBankRequest(testBankItem("staged:ev-2", "OUTFLOW")))
		if err != nil {
			t.Fatalf("unexpected execution error: %v", err)
		}
		expected := []string{"signed_lease_agreement"}
		if !reflect.DeepEqual(resp.Outcomes[0].RequiredEvidence, expected) {
			t.Errorf("required_evidence mismatch: expected %v, got %v", expected, resp.Outcomes[0].RequiredEvidence)
		}
	})

	t.Run("FromDocumentRequired", func(t *testing.T) {
		clf := NewDeterministicFakeClassifier()
		clf.AccountCodeOverride = "HOLD_INSUFFICIENT_EVIDENCE"
		clf.DocumentRequiredOverride = "bank_statement_extract"

		exec, err := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clf)
		if err != nil {
			t.Fatalf("failed to construct executor: %v", err)
		}
		resp, err := exec.Execute(context.Background(), testBankRequest(testBankItem("staged:ev-3", "OUTFLOW")))
		if err != nil {
			t.Fatalf("unexpected execution error: %v", err)
		}
		expected := []string{"bank_statement_extract"}
		if !reflect.DeepEqual(resp.Outcomes[0].RequiredEvidence, expected) {
			t.Errorf("required_evidence mismatch: expected %v, got %v", expected, resp.Outcomes[0].RequiredEvidence)
		}
	})

	t.Run("FromCandidateProperty", func(t *testing.T) {
		clf := NewDeterministicFakeClassifier()
		clf.AccountCodeOverride = "HOLD_INSUFFICIENT_EVIDENCE"
		clf.CandidateEvidenceOverride = []string{"counterparty_contract"}

		exec, err := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clf)
		if err != nil {
			t.Fatalf("failed to construct executor: %v", err)
		}
		resp, err := exec.Execute(context.Background(), testBankRequest(testBankItem("staged:ev-4", "OUTFLOW")))
		if err != nil {
			t.Fatalf("unexpected execution error: %v", err)
		}
		expected := []string{"counterparty_contract"}
		if !reflect.DeepEqual(resp.Outcomes[0].RequiredEvidence, expected) {
			t.Errorf("required_evidence mismatch: expected %v, got %v", expected, resp.Outcomes[0].RequiredEvidence)
		}
	})

	t.Run("EmptyWhenUnprovided", func(t *testing.T) {
		clf := NewDeterministicFakeClassifier()
		clf.AccountCodeOverride = "HOLD_INSUFFICIENT_EVIDENCE"

		exec, err := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clf)
		if err != nil {
			t.Fatalf("failed to construct executor: %v", err)
		}
		resp, err := exec.Execute(context.Background(), testBankRequest(testBankItem("staged:ev-5", "OUTFLOW")))
		if err != nil {
			t.Fatalf("unexpected execution error: %v", err)
		}
		if len(resp.Outcomes[0].RequiredEvidence) != 0 {
			t.Errorf("expected empty required_evidence, got %v", resp.Outcomes[0].RequiredEvidence)
		}
	})
}

// N. AseNodeID is derived from actual execution trace or left unset
func TestBankCategorizationExecutor_N_AseNodeIDTraceDerivedOrUnset(t *testing.T) {
	// 1. Terminal classified
	clf := NewDeterministicFakeClassifier()
	clf.AccountCodeOverride = "401000"
	exec, _ := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clf)
	resp, err := exec.Execute(context.Background(), testBankRequest(testBankItem("staged:trace-1", "OUTFLOW")))
	if err != nil {
		t.Fatalf("unexpected execution error: %v", err)
	}
	if resp.Outcomes[0].AseNodeID == nil || *resp.Outcomes[0].AseNodeID != "terminal_classified" {
		t.Errorf("expected 'terminal_classified', got %v", resp.Outcomes[0].AseNodeID)
	}

	// 2. Direct extractAseNodeID helper unit test with empty trace
	emptyNode := ase.NewASENode("test-user", "test-dag", nil)
	emptyNode.ExecutionTrace = nil
	if id := extractAseNodeID(emptyNode); id != nil {
		t.Errorf("expected nil node id for empty trace, got %v", *id)
	}

	// 3. Node with synthetic trace
	tracedNode := ase.NewASENode("test-user", "test-dag", nil)
	tracedNode.AppendExecutionStep(ase.NodeExecutionStep{
		DAGNodeID: "custom_interceptor_node",
		Kind:      "generic_classifier",
	})
	if id := extractAseNodeID(tracedNode); id == nil || *id != "custom_interceptor_node" {
		t.Errorf("expected 'custom_interceptor_node', got %v", id)
	}
}

// O. Confidence threshold provenance and boundary behavior (0.98 guardrail)
func TestBankCategorizationExecutor_O_ThresholdProvenanceAndBoundary(t *testing.T) {
	// 1. Above or equal to threshold (0.98) -> CLASSIFIED
	clfPass := NewDeterministicFakeClassifier()
	clfPass.AccountCodeOverride = "401000"
	clfPass.ConfidenceOverride = 0.98

	execPass, _ := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clfPass)
	respPass, err := execPass.Execute(context.Background(), testBankRequest(testBankItem("staged:thresh-pass", "OUTFLOW")))
	if err != nil {
		t.Fatalf("unexpected execution error: %v", err)
	}
	if respPass.Outcomes[0].Status != "CLASSIFIED" {
		t.Errorf("expected status CLASSIFIED at 0.98, got %q", respPass.Outcomes[0].Status)
	}

	// 2. Below threshold (0.979) -> HOLD
	clfFail := NewDeterministicFakeClassifier()
	clfFail.AccountCodeOverride = "401000"
	clfFail.ConfidenceOverride = 0.979

	execFail, _ := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clfFail)
	respFail, err := execFail.Execute(context.Background(), testBankRequest(testBankItem("staged:thresh-fail", "OUTFLOW")))
	if err != nil {
		t.Fatalf("unexpected execution error: %v", err)
	}
	if respFail.Outcomes[0].Status != "HOLD" {
		t.Errorf("expected status HOLD at 0.979, got %q", respFail.Outcomes[0].Status)
	}
	if respFail.Outcomes[0].AseNodeID == nil || *respFail.Outcomes[0].AseNodeID != "account_resolver" {
		t.Errorf("expected halt at 'account_resolver', got %v", respFail.Outcomes[0].AseNodeID)
	}
}

// P. Production fails closed when canonical config file is missing (no silent fallback)
func TestBankCategorizationExecutor_P_ConfigFailsClosedWithoutSilentFallback(t *testing.T) {
	// Call loadBankCategorizationDAGConfigFromPaths with non-existent paths
	_, err := loadBankCategorizationDAGConfigFromPaths([]string{
		"/nonexistent/path/one/dag.yml",
		"/nonexistent/path/two/dag.yml",
	})
	if err == nil {
		t.Fatal("expected error when canonical DAG file is missing, but got nil (silent fallback occurred)")
	}
	if !strings.Contains(err.Error(), "canonical bank categorization DAG config") {
		t.Errorf("expected error to mention canonical DAG config, got: %v", err)
	}
}

// Q. Semantic HOLD remains semantic HOLD vs Operational error remains Go error
func TestBankCategorizationExecutor_Q_SemanticHoldVsOperationalError(t *testing.T) {
	// 1. Semantic HOLDs return response with Status: "HOLD" and nil Go error
	semanticHoldCases := []struct {
		name       string
		code       string
		conf       float64
		expectedHR string
	}{
		{
			name:       "HoldInsufficientEvidence",
			code:       "HOLD_INSUFFICIENT_EVIDENCE",
			conf:       0.99,
			expectedHR: "HOLD_INSUFFICIENT_EVIDENCE",
		},
		{
			name:       "HoldAmbiguous",
			code:       "HOLD_AMBIGUOUS",
			conf:       0.99,
			expectedHR: "HOLD_AMBIGUOUS",
		},
		{
			name:       "LowConfidenceThresholdTrigger",
			code:       "401000",
			conf:       0.60,
			expectedHR: "guardrail",
		},
	}

	for _, tc := range semanticHoldCases {
		t.Run("Semantic_"+tc.name, func(t *testing.T) {
			clf := NewDeterministicFakeClassifier()
			clf.AccountCodeOverride = tc.code
			clf.ConfidenceOverride = tc.conf

			exec, err := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clf)
			if err != nil {
				t.Fatalf("constructor failed: %v", err)
			}

			resp, err := exec.Execute(context.Background(), testBankRequest(testBankItem("staged:sem-"+tc.name, "OUTFLOW")))
			if err != nil {
				t.Fatalf("semantic HOLD must NOT return Go error, got: %v", err)
			}
			if len(resp.Outcomes) != 1 {
				t.Fatalf("expected 1 outcome, got %d", len(resp.Outcomes))
			}
			if resp.Outcomes[0].Status != "HOLD" {
				t.Errorf("expected Status 'HOLD', got %q", resp.Outcomes[0].Status)
			}
			if resp.Outcomes[0].HoldReason == nil || !strings.Contains(*resp.Outcomes[0].HoldReason, tc.expectedHR) {
				t.Errorf("expected hold reason to contain %q, got %v", tc.expectedHR, resp.Outcomes[0].HoldReason)
			}
		})
	}

	// 2. Operational errors at each stage return non-nil Go error and zero outcomes
	stages := []string{"router", "macro", "dynamic"}
	for _, stage := range stages {
		t.Run("OperationalError_"+stage, func(t *testing.T) {
			clf := NewDeterministicFakeClassifier()
			clf.OperationalError = errors.New("simulated operational failure in stage " + stage)
			clf.OperationalErrorStage = stage

			exec, err := NewBankCategorizationASEExecutorWithClassifier(slog.Default(), clf)
			if err != nil {
				t.Fatalf("constructor failed: %v", err)
			}

			resp, err := exec.Execute(context.Background(), testBankRequest(testBankItem("staged:op-"+stage, "OUTFLOW")))
			if err == nil {
				t.Fatalf("expected operational error for stage %s, got nil error", stage)
			}
			if !strings.Contains(err.Error(), "simulated operational failure in stage "+stage) {
				t.Errorf("expected error message to mention simulated failure, got: %v", err)
			}
			if len(resp.Outcomes) > 0 {
				t.Errorf("operational error must return zero outcomes, got %d outcomes", len(resp.Outcomes))
			}
		})
	}
}
