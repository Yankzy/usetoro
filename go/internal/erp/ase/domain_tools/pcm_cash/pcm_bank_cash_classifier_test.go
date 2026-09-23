package pcm_cash

import (
	"context"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPcmClassifier_ResidualBankItemPayload_NoLegacyBookItemFields proves residual bank items
// (without legacy BookItem fields like source_artifact_kind or bookkeeping_role) execute cleanly.
func TestPcmClassifier_ResidualBankItemPayload_NoLegacyBookItemFields(t *testing.T) {
	clf := NewPcmClassifier(nil, nil, nil, nil)
	clf.SetExplicitTestCatalog(BaselineMoroccanPCGECatalog)

	// Residual Bank item payload: residual_amount_units, original_amount_units, etc.
	node := ase.NewASENode("test-co", "bookkeeping_bank_categorization_v1", map[string]any{
		"company_id":            "test-co",
		"session_id":            "sess-1",
		"bank_item_id":          "staged:bank-item-101",
		"residual_amount_units": int64(150000),
		"original_amount_units": int64(150000),
		"direction":             "OUTFLOW",
		"currency":              "MAD",
		"description":           "FACTURE ORANGE INTERNET MAROC",
		"counterparty_name":     "ORANGE MAROC",
		"reference":             "VIR-ORANGE-992",
		"provenance_refs":       []string{"bank_feed:tx_101"},
	})

	// 1. Router ThinkFunc
	routerFn := clf.BuildPayloadRouterThinkFunc("direction")
	routerRes, err := routerFn(context.Background(), []*ase.AutonomousSemanticEngineNode{node})
	require.NoError(t, err)
	assert.Equal(t, "OUTFLOW", routerRes[node.NodeID].Candidates[0].Value)

	// 2. Outflow Macro ThinkFunc
	macroFn := clf.BuildGenericThinkFunc("bank_macro_classifier_outflow")
	macroRes, err := macroFn(context.Background(), []*ase.AutonomousSemanticEngineNode{node})
	require.NoError(t, err)
	assert.Equal(t, "EXPENSE", macroRes[node.NodeID].Candidates[0].Value)

	// 3. Dynamic Account Resolver ThinkFunc
	dynFn := clf.BuildDynamicThinkFunc("account_resolver")
	dynRes, err := dynFn(context.Background(), []*ase.AutonomousSemanticEngineNode{node})
	require.NoError(t, err)
	assert.Equal(t, "account_code", dynRes[node.NodeID].Property)
	accCode := dynRes[node.NodeID].Candidates[0].Value
	assert.True(t, strings.HasPrefix(accCode, "6"), "expected class 6 expense account, got %s", accCode)
}

// TestPcmClassifier_DirectionRouting_Deterministic proves direction routing is deterministic
// and correctly handles OUTFLOW, INFLOW, and ambiguous movements.
func TestPcmClassifier_DirectionRouting_Deterministic(t *testing.T) {
	clf := NewPcmClassifier(nil, nil, nil, nil)
	routerFn := clf.BuildPayloadRouterThinkFunc("direction")

	outNode := ase.NewASENode("co-1", "dag", map[string]any{
		"direction":   "OUTFLOW",
		"description": "ACHAT MATERIEL",
	})
	inNode := ase.NewASENode("co-1", "dag", map[string]any{
		"direction":   "INFLOW",
		"description": "VIREMENT CLIENT ENCAISSEMENT",
	})
	ambigNode := ase.NewASENode("co-1", "dag", map[string]any{
		"direction":   "OUTFLOW",
		"description": "AMBIGUOUS MOVEMENT",
	})

	res, err := routerFn(context.Background(), []*ase.AutonomousSemanticEngineNode{outNode, inNode, ambigNode})
	require.NoError(t, err)

	assert.Equal(t, "OUTFLOW", res[outNode.NodeID].Candidates[0].Value)
	assert.Equal(t, "INFLOW", res[inNode.NodeID].Candidates[0].Value)
	assert.Equal(t, "HOLD_AMBIGUOUS", res[ambigNode.NodeID].Candidates[0].Value)
}

// TestPcmClassifier_MacroNodes_OutflowVsInflowSeparation proves dedicated bank macro nodes
// separate outflow classes from inflow classes.
func TestPcmClassifier_MacroNodes_OutflowVsInflowSeparation(t *testing.T) {
	clf := NewPcmClassifier(nil, nil, nil, nil)

	// Outflow node: standard outflow is EXPENSE
	outNode := ase.NewASENode("co-1", "dag", map[string]any{
		"direction":   "OUTFLOW",
		"description": "HONORAIRES COMPTABLE FIDUCIAIRE",
	})
	outMacroFn := clf.BuildGenericThinkFunc("bank_macro_classifier_outflow")
	resOut, err := outMacroFn(context.Background(), []*ase.AutonomousSemanticEngineNode{outNode})
	require.NoError(t, err)
	assert.Equal(t, "EXPENSE", resOut[outNode.NodeID].Candidates[0].Value)

	// Inflow node: standard inflow is REVENUE
	inNode := ase.NewASENode("co-1", "dag", map[string]any{
		"direction":   "INFLOW",
		"description": "ENCAISSEMENT PRESTATION SERVICE",
	})
	inMacroFn := clf.BuildGenericThinkFunc("bank_macro_classifier_inflow")
	resIn, err := inMacroFn(context.Background(), []*ase.AutonomousSemanticEngineNode{inNode})
	require.NoError(t, err)
	assert.Equal(t, "REVENUE", resIn[inNode.NodeID].Candidates[0].Value)
}

// TestPcmClassifier_AuthoritativeCoA_ContainmentCheck proves the resolved account code
// must strictly be contained within the supplied candidate set.
func TestPcmClassifier_AuthoritativeCoA_ContainmentCheck(t *testing.T) {
	clf := NewPcmClassifier(nil, nil, nil, nil)

	// Custom restricted catalog with only 2 expense accounts
	restrictedCatalog := []AccountCandidate{
		{Code: "6131", Name: "Locations et charges locatives", Role: "ex_rent", BalanceType: "DEBIT", Active: true},
		{Code: "6134", Name: "Services bancaires", Role: "ex_service", BalanceType: "DEBIT", Active: true},
	}
	clf.SetExplicitTestCatalog(restrictedCatalog)

	node := ase.NewASENode("co-1", "dag", map[string]any{
		"direction":   "OUTFLOW",
		"macro_class": "EXPENSE",
		"description": "FACTURE DIVERS SANS COMPTE EXPLICITE",
	})

	resolverFn := clf.BuildDynamicThinkFunc("account_resolver")
	res, err := resolverFn(context.Background(), []*ase.AutonomousSemanticEngineNode{node})
	require.NoError(t, err)

	selectedCode := res[node.NodeID].Candidates[0].Value
	assert.Contains(t, []string{"6131", "6134"}, selectedCode, "selected code must belong to allowed candidates")
}

// TestPcmClassifier_AuthoritativeCoA_FailsClosedWithoutPool proves that in production (pool == nil,
// no explicit test injection), account prefetch fails closed with a Go operational error.
func TestPcmClassifier_AuthoritativeCoA_FailsClosedWithoutPool(t *testing.T) {
	// PcmClassifier with nil pool and NO explicit catalog
	clf := NewPcmClassifier(nil, nil, nil, nil)

	node := ase.NewASENode("co-1", "dag", map[string]any{
		"direction":   "OUTFLOW",
		"macro_class": "EXPENSE",
		"description": "ACHAT MATERIEL",
	})

	resolverFn := clf.BuildDynamicThinkFunc("account_resolver")
	_, err := resolverFn(context.Background(), []*ase.AutonomousSemanticEngineNode{node})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "authoritative CoA pool is unavailable")
}

// TestPcmClassifier_AuthoritativeCoA_EmptyCandidatesReturnsHold proves that when an entity's CoA
// has no accounts for the constrained macro family, the resolver returns semantic HOLD_INSUFFICIENT_EVIDENCE.
func TestPcmClassifier_AuthoritativeCoA_EmptyCandidatesReturnsHold(t *testing.T) {
	clf := NewPcmClassifier(nil, nil, nil, nil)
	// Inject empty catalog (e.g. company has no expense accounts defined)
	clf.SetExplicitTestCatalog([]AccountCandidate{})

	node := ase.NewASENode("co-1", "dag", map[string]any{
		"direction":   "OUTFLOW",
		"macro_class": "EXPENSE",
		"description": "ACHAT MATERIEL",
	})

	resolverFn := clf.BuildDynamicThinkFunc("account_resolver")
	res, err := resolverFn(context.Background(), []*ase.AutonomousSemanticEngineNode{node})
	require.NoError(t, err)
	assert.Equal(t, "HOLD_INSUFFICIENT_EVIDENCE", res[node.NodeID].Candidates[0].Value)
	assert.Contains(t, res[node.NodeID].Candidates[0].Reasoning, "No candidate accounts available")
}

// TestPcmClassifier_InternalTransfer_ReturnsHold proves that internal transfer patterns
// (e.g., "VIREMENT INTERNE", "VIREMENT DE COMPTE A COMPTE", "TRANSIT 5115") return semantic HOLD
// rather than being classified into operating expense (6xxx) or revenue (7xxx) accounts.
func TestPcmClassifier_InternalTransfer_ReturnsHold(t *testing.T) {
	clf := NewPcmClassifier(nil, nil, nil, nil)
	clf.SetExplicitTestCatalog(BaselineMoroccanPCGECatalog)

	transferCases := []struct {
		description string
		cpName      string
	}{
		{description: "VIREMENT INTERNE VERS COMPTE BMCE", cpName: ""},
		{description: "VIR DE COMPTE A COMPTE", cpName: ""},
		{description: "VIREMENT COMPTE A COMPTE", cpName: ""},
		{description: "VIR C/C", cpName: ""},
		{description: "VIREMENT PROPRE COMPTE", cpName: ""},
		{description: "TRANSIT 5115 ALIMENTATION", cpName: ""},
		{description: "ALIMENTATION CAISSE 5115", cpName: ""},
		{description: "VIREMENT VERS AUTRE COMPTE", cpName: "COMPTE COURANT BMCE (VIR C/C)"},
	}

	for _, tc := range transferCases {
		t.Run(tc.description, func(t *testing.T) {
			node := ase.NewASENode("co-1", "dag", map[string]any{
				"direction":             "OUTFLOW",
				"residual_amount_units": int64(5000000),
				"original_amount_units": int64(5000000),
				"description":           tc.description,
				"counterparty_name":     tc.cpName,
			})

			// 1. Outflow macro classifier must detect internal transfer and return HOLD
			macroFn := clf.BuildGenericThinkFunc("bank_macro_classifier_outflow")
			mRes, err := macroFn(context.Background(), []*ase.AutonomousSemanticEngineNode{node})
			require.NoError(t, err)
			assert.Equal(t, "HOLD_INSUFFICIENT_EVIDENCE", mRes[node.NodeID].Candidates[0].Value)
			assert.Contains(t, mRes[node.NodeID].Candidates[0].Reasoning, "Internal transfer detected")

			// 2. Account resolver must also return HOLD if reached
			dynFn := clf.BuildDynamicThinkFunc("account_resolver")
			dRes, err := dynFn(context.Background(), []*ase.AutonomousSemanticEngineNode{node})
			require.NoError(t, err)
			assert.Equal(t, "HOLD_INSUFFICIENT_EVIDENCE", dRes[node.NodeID].Candidates[0].Value)
			assert.Contains(t, dRes[node.NodeID].Candidates[0].Reasoning, "Internal transfer detected")
		})
	}
}

// TestPcmClassifier_AmbiguousResidual_ReturnsHold proves that ambiguous residual movements return HOLD.
func TestPcmClassifier_AmbiguousResidual_ReturnsHold(t *testing.T) {
	clf := NewPcmClassifier(nil, nil, nil, nil)
	clf.SetExplicitTestCatalog(BaselineMoroccanPCGECatalog)

	node := ase.NewASENode("co-1", "dag", map[string]any{
		"direction":             "OUTFLOW",
		"residual_amount_units": int64(100000),
		"original_amount_units": int64(100000),
		"description":           "UNKNOWN TRANSACTION / AMBIGUOUS REF",
	})

	macroFn := clf.BuildGenericThinkFunc("bank_macro_classifier_outflow")
	res, err := macroFn(context.Background(), []*ase.AutonomousSemanticEngineNode{node})
	require.NoError(t, err)
	assert.Equal(t, "HOLD_AMBIGUOUS", res[node.NodeID].Candidates[0].Value)
}
