package pcm_cash

import (
	"context"
	"testing"

	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetCandidateAccounts_ProductionFailsClosedWithoutPool(t *testing.T) {
	ctx := context.Background()
	// In production (no test injection context), nil pool must fail closed with error
	cands, err := GetCandidateAccounts(ctx, nil, nil, "atlas", "EXPENSE", "OUTFLOW")
	assert.Error(t, err)
	assert.Nil(t, cands)
	assert.Contains(t, err.Error(), "authoritative CoA pool is unavailable")
}

func TestGetCandidateAccounts_ExplicitTestInjectionViaContext(t *testing.T) {
	ctx := context.Background()
	testCtx := ContextWithExplicitTestCatalog(ctx, BaselineMoroccanPCGECatalog)
	cands, err := GetCandidateAccounts(testCtx, nil, nil, "atlas", "EXPENSE", "OUTFLOW")
	require.NoError(t, err)
	assert.NotEmpty(t, cands)

	for _, c := range cands {
		assert.True(t, c.Code[0] == '6' || c.Role == "cogs_regular" || len(c.Role) > 3 && c.Role[:3] == "ex_")
	}
}

func TestPcmClassifier_ExplicitTestCatalogInjection(t *testing.T) {
	clf := NewPcmClassifier(nil, nil, nil, nil)
	// Without injection, dynamic think func fails closed because pool is nil
	thinkFn := clf.BuildDynamicThinkFunc("pcge")
	node := ase.NewASENode("test-co", "dag", map[string]any{
		"direction":   "OUTFLOW",
		"macro_class": "EXPENSE",
		"description": "ACHAT MATERIEL",
	})
	_, err := thinkFn(context.Background(), []*ase.AutonomousSemanticEngineNode{node})
	assert.Error(t, err, "must fail closed when pool is nil and no test catalog injected")

	// With explicit test catalog injected, think func resolves successfully
	clf.SetExplicitTestCatalog(BaselineMoroccanPCGECatalog)
	res, err := thinkFn(context.Background(), []*ase.AutonomousSemanticEngineNode{node})
	require.NoError(t, err)
	assert.Equal(t, "account_code", res[node.NodeID].Property)
	assert.NotEmpty(t, res[node.NodeID].Candidates)
}

func TestFilterAccountsByMacroClass_Filtering(t *testing.T) {
	accounts := BaselineMoroccanPCGECatalog

	expenses := filterAccountsByMacroClass(accounts, "EXPENSE")
	for _, a := range expenses {
		assert.Equal(t, byte('6'), a.Code[0])
	}

	revenues := filterAccountsByMacroClass(accounts, "REVENUE")
	for _, a := range revenues {
		assert.Equal(t, byte('7'), a.Code[0])
	}

	assets := filterAccountsByMacroClass(accounts, "ASSET")
	for _, a := range assets {
		first := a.Code[0]
		assert.True(t, first == '2' || first == '3' || first == '5', "asset account %s should be in class 2, 3, or 5", a.Code)
	}

	liabilities := filterAccountsByMacroClass(accounts, "LIABILITY")
	for _, a := range liabilities {
		first := a.Code[0]
		assert.True(t, first == '1' || first == '4' || first == '5', "liability account %s should be in class 1, 4, or 5", a.Code)
	}
}

func TestPcmClassifier_PayloadRouter(t *testing.T) {
	clf := NewPcmClassifier(nil, nil, nil, nil)
	fn := clf.BuildPayloadRouterThinkFunc("direction")

	node1 := ase.NewASENode("u1", "dag", map[string]any{
		"direction":   "OUTFLOW",
		"description": "ACHAT MATERIEL",
	})
	node2 := ase.NewASENode("u2", "dag", map[string]any{
		"direction":   "INFLOW",
		"description": "VIREMENT CLIENT",
	})
	node3 := ase.NewASENode("u3", "dag", map[string]any{
		"direction":   "OUTFLOW",
		"description": "AMBIGUOUS UNKNOWN ENTRY",
	})

	res, err := fn(context.Background(), []*ase.AutonomousSemanticEngineNode{node1, node2, node3})
	require.NoError(t, err)

	assert.Equal(t, "OUTFLOW", res[node1.NodeID].Candidates[0].Value)
	assert.Equal(t, "INFLOW", res[node2.NodeID].Candidates[0].Value)
	assert.Equal(t, "HOLD_AMBIGUOUS", res[node3.NodeID].Candidates[0].Value)
}

func TestBaselineMoroccanCatalog_ExplicitHelperOnly(t *testing.T) {
	cat := BaselineMoroccanCatalog()
	require.Len(t, cat, len(BaselineMoroccanPCGECatalog))
	assert.Equal(t, BaselineMoroccanPCGECatalog[0].Code, cat[0].Code)

	// Verify mutating the returned slice does not mutate the baseline
	cat[0].Code = "MUTATED"
	assert.NotEqual(t, "MUTATED", BaselineMoroccanPCGECatalog[0].Code)
}

func TestAuthoritativeDjangoCoaQuery_Invariants(t *testing.T) {
	q := AuthoritativeDjangoCoaQuery

	// Invariant 1: Authoritative Django tables only
	assert.Contains(t, q, "FROM ledger_accountmodel a")
	assert.Contains(t, q, "JOIN ledger_chartofaccountmodel coa ON coa.uuid = a.coa_model_id")

	// Invariant 2: Strict default_coa enforcement and company isolation
	assert.Contains(t, q, "JOIN ledger_entitymodel e ON (e.default_coa_id = coa.uuid AND coa.entity_id = e.uuid)")
	assert.NotContains(t, q, "e.default_coa_id IS NULL", "Query must not fall back when default_coa is null")

	// Invariant 3: Company / entity scoped
	assert.Contains(t, q, "WHERE (REPLACE(CAST(e.uuid AS text), '-', '') = REPLACE($1, '-', '') OR e.slug = $1)")
	assert.Contains(t, q, "AND a.active = true")

	// Invariant 4: Pure read-only SELECT
	assert.NotContains(t, q, "INSERT")
	assert.NotContains(t, q, "UPDATE")
	assert.NotContains(t, q, "DELETE")
	assert.NotContains(t, q, "DROP")
	assert.NotContains(t, q, "ALTER")

	// Invariant 5: Zero Shadow ERP authority
	assert.NotContains(t, q, "shadow_erp")
	assert.NotContains(t, q, "shadow_erp.accounts")
}

func TestGetCandidateAccountsWithSource_TelemetryTags(t *testing.T) {
	ctx := context.Background()

	// 1. In production, nil pool fails closed and emits error
	_, source, err := GetCandidateAccountsWithSource(ctx, nil, nil, "atlas", "EXPENSE", "OUTFLOW")
	assert.Error(t, err)
	assert.Equal(t, "", source)
	assert.Contains(t, err.Error(), "authoritative CoA pool is unavailable")

	// 2. Explicit test injection emits EXPLICIT_TEST_INJECTION
	testCtx := ContextWithExplicitTestCatalog(ctx, BaselineMoroccanCatalog())
	cands, source, err := GetCandidateAccountsWithSource(testCtx, nil, nil, "atlas", "EXPENSE", "OUTFLOW")
	require.NoError(t, err)
	assert.NotEmpty(t, cands)
	assert.Equal(t, CandidateSourceExplicitTestInjection, source)
}

func TestPcmClassifier_DiagnosticTelemetry(t *testing.T) {
	clf := NewPcmClassifier(nil, nil, nil, nil)
	clf.SetExplicitTestCatalog(BaselineMoroccanCatalog())

	thinkFn := clf.BuildDynamicThinkFunc("pcge")
	node := ase.NewASENode("test-co", "dag", map[string]any{
		"direction":   "OUTFLOW",
		"macro_class": "EXPENSE",
		"description": "ACHAT FOURNITURES DE BUREAU",
	})
	res, err := thinkFn(context.Background(), []*ase.AutonomousSemanticEngineNode{node})
	require.NoError(t, err)
	assert.NotEmpty(t, res)

	// Check diagnostic telemetry tag on node payload
	assert.Equal(t, CandidateSourceExplicitTestInjection, node.Payload["candidate_source"])
}

func TestFilterAccountsBySourceFamily_InvoiceConstrainsReceivableFamily(t *testing.T) {
	accounts := BaselineMoroccanCatalog()

	// 1. By source_artifact_kind = "INVOICE"
	invoiceCands := FilterAccountsBySourceFamily(accounts, "ASSET", "INVOICE", "")
	require.Len(t, invoiceCands, 1)
	assert.Equal(t, "3421", invoiceCands[0].Code)
	assert.Equal(t, "asset_ca_recv", invoiceCands[0].Role)

	// 2. By bookkeeping_role = "OPEN_RECEIVABLE"
	recvCands := FilterAccountsBySourceFamily(accounts, "ASSET", "", "OPEN_RECEIVABLE")
	require.Len(t, recvCands, 1)
	assert.Equal(t, "3421", recvCands[0].Code)

	// 3. Custom catalog with multiple receivable accounts: both must be retained, others excluded
	multiRecvCatalog := []AccountCandidate{
		{Code: "34211", Name: "Clients A", Role: "asset_ca_recv", Active: true},
		{Code: "34212", Name: "Clients B", Role: "asset_ca_recv", Active: true},
		{Code: "2355", Name: "Matériel", Role: "asset_ppe_equip", Active: true},
		{Code: "5141", Name: "Banque", Role: "asset_ca_cash", Active: true},
		{Code: "6111", Name: "Achats", Role: "cogs_regular", Active: true},
	}
	filteredMulti := FilterAccountsBySourceFamily(multiRecvCatalog, "ASSET", "INVOICE", "OPEN_RECEIVABLE")
	require.Len(t, filteredMulti, 2)
	assert.Equal(t, "34211", filteredMulti[0].Code)
	assert.Equal(t, "34212", filteredMulti[1].Code)

	// 4. Custom catalog with zero receivable accounts: returns empty slice (fail closed)
	noRecvCatalog := []AccountCandidate{
		{Code: "2355", Name: "Matériel", Role: "asset_ppe_equip", Active: true},
		{Code: "6111", Name: "Achats", Role: "cogs_regular", Active: true},
	}
	filteredEmpty := FilterAccountsBySourceFamily(noRecvCatalog, "ASSET", "INVOICE", "OPEN_RECEIVABLE")
	assert.Empty(t, filteredEmpty)
}

func TestFilterAccountsBySourceFamily_BillConstrainsPayableFamily(t *testing.T) {
	accounts := BaselineMoroccanCatalog()

	// 1. By source_artifact_kind = "BILL"
	billCands := FilterAccountsBySourceFamily(accounts, "LIABILITY", "BILL", "")
	require.Len(t, billCands, 1)
	assert.Equal(t, "4411", billCands[0].Code)
	assert.Equal(t, "lia_cl_acc_payable", billCands[0].Role)

	// 2. By bookkeeping_role = "OPEN_PAYABLE"
	payCands := FilterAccountsBySourceFamily(accounts, "LIABILITY", "", "OPEN_PAYABLE")
	require.Len(t, payCands, 1)
	assert.Equal(t, "4411", payCands[0].Code)

	// 3. Custom catalog with multiple payable accounts: both retained, others excluded
	multiPayCatalog := []AccountCandidate{
		{Code: "44111", Name: "Fournisseurs A", Role: "lia_cl_acc_payable", Active: true},
		{Code: "44112", Name: "Fournisseurs B", Role: "lia_cl_acc_payable", Active: true},
		{Code: "4432", Name: "Salaires", Role: "lia_cl_wages_payable", Active: true},
		{Code: "4455", Name: "TVA Facturée", Role: "lia_cl_taxes_payable", Active: true},
		{Code: "6111", Name: "Achats", Role: "cogs_regular", Active: true},
	}
	filteredMulti := FilterAccountsBySourceFamily(multiPayCatalog, "LIABILITY", "BILL", "OPEN_PAYABLE")
	require.Len(t, filteredMulti, 2)
	assert.Equal(t, "44111", filteredMulti[0].Code)
	assert.Equal(t, "44112", filteredMulti[1].Code)

	// 4. Custom catalog with zero payable accounts: returns empty slice (fail closed)
	noPayCatalog := []AccountCandidate{
		{Code: "4432", Name: "Salaires", Role: "lia_cl_wages_payable", Active: true},
		{Code: "6111", Name: "Achats", Role: "cogs_regular", Active: true},
	}
	filteredEmpty := FilterAccountsBySourceFamily(noPayCatalog, "LIABILITY", "BILL", "OPEN_PAYABLE")
	assert.Empty(t, filteredEmpty)
}

func TestFilterAccountsBySourceFamily_DirectOtherUsesMacroClass(t *testing.T) {
	accounts := BaselineMoroccanCatalog()

	// Direct / Other outflow expense
	expenses := FilterAccountsBySourceFamily(accounts, "EXPENSE", "", "")
	for _, a := range expenses {
		assert.Equal(t, byte('6'), a.Code[0])
	}

	// Direct / Other inflow revenue
	revenues := FilterAccountsBySourceFamily(accounts, "REVENUE", "", "")
	for _, a := range revenues {
		assert.Equal(t, byte('7'), a.Code[0])
	}
}

func TestPcmClassifier_SourceAwareLifecycleConstraints_InvoiceAndBill(t *testing.T) {
	clf := NewPcmClassifier(nil, nil, nil, nil)
	clf.SetExplicitTestCatalog(BaselineMoroccanCatalog())

	macroFn := clf.BuildGenericThinkFunc("book_macro_classifier_inflow")
	resolverFn := clf.BuildDynamicThinkFunc("pcge")

	// 1. Invoice item: open receivable obligation
	kindInvoice := "INVOICE"
	roleReceivable := "OPEN_RECEIVABLE"
	invoiceNode := ase.NewASENode("test-co", "dag", map[string]any{
		"source_artifact_kind": kindInvoice,
		"bookkeeping_role":    roleReceivable,
		"direction":            "INFLOW",
		"description":          "Facture client n° 1024",
		"amount":               int64(2500000),
		"currency":             "MAD",
	})

	macroRes, err := macroFn(context.Background(), []*ase.AutonomousSemanticEngineNode{invoiceNode})
	require.NoError(t, err)
	assert.Equal(t, "ASSET", macroRes[invoiceNode.NodeID].Candidates[0].Value)
	assert.Equal(t, 1.0, macroRes[invoiceNode.NodeID].Candidates[0].Confidence)
	assert.Equal(t, "INVOICE", invoiceNode.Payload["source_kind"])
	assert.Equal(t, "ASSET", invoiceNode.Payload["constrained_macro"])

	resolveRes, err := resolverFn(context.Background(), []*ase.AutonomousSemanticEngineNode{invoiceNode})
	require.NoError(t, err)
	assert.Equal(t, "3421", resolveRes[invoiceNode.NodeID].Candidates[0].Value)
	assert.Equal(t, 0.99, resolveRes[invoiceNode.NodeID].Candidates[0].Confidence)
	assert.Equal(t, []string{"3421"}, invoiceNode.Payload["candidate_codes"])
	assert.Equal(t, CandidateSourceExplicitTestInjection, invoiceNode.Payload["candidate_source"])

	// 2. Bill item: open payable obligation
	kindBill := "BILL"
	rolePayable := "OPEN_PAYABLE"
	billNode := ase.NewASENode("test-co", "dag", map[string]any{
		"source_artifact_kind": kindBill,
		"bookkeeping_role":    rolePayable,
		"direction":            "OUTFLOW",
		"description":          "Facture fournisseur AWS",
		"amount":               int64(100000),
		"currency":             "MAD",
	})

	billMacroFn := clf.BuildGenericThinkFunc("book_macro_classifier_outflow")
	billMacroRes, err := billMacroFn(context.Background(), []*ase.AutonomousSemanticEngineNode{billNode})
	require.NoError(t, err)
	assert.Equal(t, "LIABILITY", billMacroRes[billNode.NodeID].Candidates[0].Value)
	assert.Equal(t, 1.0, billMacroRes[billNode.NodeID].Candidates[0].Confidence)
	assert.Equal(t, "BILL", billNode.Payload["source_kind"])
	assert.Equal(t, "LIABILITY", billNode.Payload["constrained_macro"])

	billResolveRes, err := resolverFn(context.Background(), []*ase.AutonomousSemanticEngineNode{billNode})
	require.NoError(t, err)
	assert.Equal(t, "4411", billResolveRes[billNode.NodeID].Candidates[0].Value)
	assert.Equal(t, 0.99, billResolveRes[billNode.NodeID].Candidates[0].Confidence)
	assert.Equal(t, []string{"4411"}, billNode.Payload["candidate_codes"])
	assert.Equal(t, CandidateSourceExplicitTestInjection, billNode.Payload["candidate_source"])
}

func TestPcmClassifier_DirectOther_InvokesSemanticMacro(t *testing.T) {
	clf := NewPcmClassifier(nil, nil, nil, nil)
	clf.SetExplicitTestCatalog(BaselineMoroccanCatalog())

	macroFnOutflow := clf.BuildGenericThinkFunc("book_macro_classifier_outflow")
	macroFnInflow := clf.BuildGenericThinkFunc("book_macro_classifier_inflow")
	resolverFn := clf.BuildDynamicThinkFunc("pcge")

	// Direct expense outflow (no invoice, no bill)
	directExpense := ase.NewASENode("test-co", "dag", map[string]any{
		"direction":   "OUTFLOW",
		"description": "LOYER MENSUEL BUREAU",
		"amount":      int64(2500000),
		"currency":    "MAD",
	})
	resOut, err := macroFnOutflow(context.Background(), []*ase.AutonomousSemanticEngineNode{directExpense})
	require.NoError(t, err)
	assert.Equal(t, "EXPENSE", resOut[directExpense.NodeID].Candidates[0].Value)
	assert.Equal(t, "DIRECT_OR_OTHER", directExpense.Payload["source_kind"])

	resCodeOut, err := resolverFn(context.Background(), []*ase.AutonomousSemanticEngineNode{directExpense})
	require.NoError(t, err)
	assert.Equal(t, "6131", resCodeOut[directExpense.NodeID].Candidates[0].Value)

	// Direct revenue inflow (no invoice, no bill)
	directRevenue := ase.NewASENode("test-co", "dag", map[string]any{
		"direction":   "INFLOW",
		"description": "VENTE CLIENT MARCHANDISE",
		"amount":      int64(500000),
		"currency":    "MAD",
	})
	resIn, err := macroFnInflow(context.Background(), []*ase.AutonomousSemanticEngineNode{directRevenue})
	require.NoError(t, err)
	assert.Equal(t, "REVENUE", resIn[directRevenue.NodeID].Candidates[0].Value)
	assert.Equal(t, "DIRECT_OR_OTHER", directRevenue.Payload["source_kind"])

	resCodeIn, err := resolverFn(context.Background(), []*ase.AutonomousSemanticEngineNode{directRevenue})
	require.NoError(t, err)
	assert.Equal(t, "7111", resCodeIn[directRevenue.NodeID].Candidates[0].Value)
}

func TestPcmClassifier_PostedTransaction_Decision(t *testing.T) {
	clf := NewPcmClassifier(nil, nil, nil, nil)
	clf.SetExplicitTestCatalog(BaselineMoroccanCatalog())

	macroFn := clf.BuildGenericThinkFunc("book_macro_classifier_outflow")
	resolverFn := clf.BuildDynamicThinkFunc("pcge")

	// Case 1: Posted transaction with pre-existing validated account code
	existingCode := "6134"
	postedWithCode := ase.NewASENode("test-co", "dag", map[string]any{
		"source_artifact_kind":  "TRANSACTION",
		"bookkeeping_role":     "POSTED_CASH_MOVEMENT",
		"existing_account_code": &existingCode,
		"direction":             "OUTFLOW",
		"description":           "Payment for Software Subscription",
	})
	resMacro1, err := macroFn(context.Background(), []*ase.AutonomousSemanticEngineNode{postedWithCode})
	require.NoError(t, err)
	assert.Equal(t, "EXPENSE", resMacro1[postedWithCode.NodeID].Candidates[0].Value)
	assert.Equal(t, 1.0, resMacro1[postedWithCode.NodeID].Candidates[0].Confidence)
	assert.Equal(t, "POSTED_TRANSACTION", postedWithCode.Payload["source_kind"])

	resCode1, err := resolverFn(context.Background(), []*ase.AutonomousSemanticEngineNode{postedWithCode})
	require.NoError(t, err)
	assert.Equal(t, "6134", resCode1[postedWithCode.NodeID].Candidates[0].Value)
	assert.Equal(t, 0.99, resCode1[postedWithCode.NodeID].Candidates[0].Confidence)

	// Case 2: Posted transaction missing account code (reclassification prohibited)
	postedWithoutCode := ase.NewASENode("test-co", "dag", map[string]any{
		"source_artifact_kind": "TRANSACTION",
		"bookkeeping_role":    "POSTED_CASH_MOVEMENT",
		"direction":            "OUTFLOW",
		"description":          "Payment for Software Subscription",
	})
	resMacro2, err := macroFn(context.Background(), []*ase.AutonomousSemanticEngineNode{postedWithoutCode})
	require.NoError(t, err)
	assert.Equal(t, "HOLD_AMBIGUOUS", resMacro2[postedWithoutCode.NodeID].Candidates[0].Value)

	resCode2, err := resolverFn(context.Background(), []*ase.AutonomousSemanticEngineNode{postedWithoutCode})
	require.NoError(t, err)
	assert.Equal(t, "HOLD_INSUFFICIENT_EVIDENCE", resCode2[postedWithoutCode.NodeID].Candidates[0].Value)
}

func TestPcmClassifier_MultipleReceivableAccounts_ConfiguredDeterministic(t *testing.T) {
	clf := NewPcmClassifier(nil, nil, nil, nil)

	// Catalog with 2 receivable accounts (e.g. 34211 and 34212)
	customCatalog := []AccountCandidate{
		{Code: "34211", Name: "Clients Cat A", Role: "asset_ca_recv", Active: true},
		{Code: "34212", Name: "Clients Cat B", Role: "asset_ca_recv", Active: true},
		{Code: "4411", Name: "Fournisseurs", Role: "lia_cl_acc_payable", Active: true},
		{Code: "6111", Name: "Achats", Role: "cogs_regular", Active: true},
	}
	clf.SetExplicitTestCatalog(customCatalog)

	resolverFn := clf.BuildDynamicThinkFunc("pcge")
	node := ase.NewASENode("test-co", "dag", map[string]any{
		"source_artifact_kind": "INVOICE",
		"bookkeeping_role":    "OPEN_RECEIVABLE",
		"macro_class":          "ASSET",
		"direction":            "INFLOW",
		"description":          "Facture Client",
	})

	res, err := resolverFn(context.Background(), []*ase.AutonomousSemanticEngineNode{node})
	require.NoError(t, err)
	// Candidates must be constrained strictly to {34211, 34212}
	cands := node.Payload["candidate_codes"].([]string)
	assert.ElementsMatch(t, []string{"34211", "34212"}, cands)
	// Must select from allowed candidates (not blindly 3421 which is not in CoA)
	selected := res[node.NodeID].Candidates[0].Value
	assert.True(t, selected == "34211" || selected == "34212", "must select an admissible candidate from entity CoA")
}

func TestPcmClassifier_GAAPToolUnaffected(t *testing.T) {
	// 1. Verify Moroccan tool is registered and returns PcmClassifier
	pcmTool := domain_tools.Get("pcm_cash_accounting")
	require.NotNil(t, pcmTool)
	pcmClassifier := pcmTool.GetClassifier(domain_tools.ToolDependencies{})
	require.IsType(t, &PcmClassifier{}, pcmClassifier)

	// 2. Verify GAAP tool is registered and remains separate
	gaapTool := domain_tools.Get("bookkeeping")
	require.NotNil(t, gaapTool)
	assert.NotEqual(t, pcmTool, gaapTool)
}

