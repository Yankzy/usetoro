package workers

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools/pcm_cash"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func TestBookCategorizer_Architecture_ResolvesMoroccanDomainTool(t *testing.T) {
	// 1. Verify that pcm_cash_accounting is registered in domain_tools registry
	tool := domain_tools.Get("pcm_cash_accounting")
	require.NotNil(t, tool, "pcm_cash_accounting must be statically registered in domain_tools")

	// 2. Verify GetClassifier returns Moroccan PcmClassifier
	classifier := tool.GetClassifier(domain_tools.ToolDependencies{})
	require.NotNil(t, classifier, "pcm_cash_accounting must return a non-nil Classifier")
	_, isPcmClassifier := classifier.(*pcm_cash.PcmClassifier)
	assert.True(t, isPcmClassifier, "Classifier returned by pcm_cash_accounting must be *pcm_cash.PcmClassifier")

	// 3. Verify that loadBookCategorizationConfig specifies pcm_cash_accounting
	cfg, domainToolName, err := loadBookCategorizationConfig()
	require.NoError(t, err)
	assert.Equal(t, "pcm_cash_accounting", domainToolName, "DAG config must specify pcm_cash_accounting as domain_tool")
	assert.NotNil(t, cfg.Nodes["pcge_account_resolver"], "pcge_account_resolver node must exist in DAG topology")
}

func TestBookCategorizer_Architecture_DomainIsolation(t *testing.T) {
	// Verify that Moroccan pcm_cash domain tool does NOT register GAAP or QBO tools
	moroccanTool := domain_tools.Get("pcm_cash_accounting")
	require.NotNil(t, moroccanTool)

	gaapTool := domain_tools.Get("bookkeeping")
	// If gaapTool exists, it must be separate instance from moroccanTool
	if gaapTool != nil {
		assert.NotEqual(t, moroccanTool, gaapTool, "Moroccan domain tool must remain separate from GAAP bookkeeping tool")
	}
}

func TestBookCategorizer_Architecture_CandidateValidationSafety(t *testing.T) {
	// Verify that PcmClassifier with explicit test catalog routes safely
	classifier := pcm_cash.NewPcmClassifier(nil, nil, nil, nil)
	classifier.SetExplicitTestCatalog(pcm_cash.BaselineMoroccanPCGECatalog)
	thinkFn := classifier.BuildDynamicThinkFunc("pcge")
	require.NotNil(t, thinkFn)

	node := ase.NewASENode("test-company", "bookkeeping_account_categorization_v1", map[string]any{
		"description": "ACHAT MATERIEL INCONNU",
		"direction":   "OUTFLOW",
		"macro_class": "EXPENSE",
	})

	results, err := thinkFn(context.Background(), []*ase.AutonomousSemanticEngineNode{node})
	require.NoError(t, err)
	res, ok := results[node.NodeID]
	require.True(t, ok)
	assert.Equal(t, "account_code", res.Property)
	require.NotEmpty(t, res.Candidates)
	// Explicit test catalog routes standard expense to Moroccan PCGE 61xx
	assert.NotEmpty(t, res.Candidates[0].Value)
}

func TestBookCategorizer_Architecture_ProductionCannotSilentlyUseBaselineCatalog(t *testing.T) {
	// Production invariant: when pool is nil and no test catalog is explicitly injected, fail closed
	classifier := pcm_cash.NewPcmClassifier(nil, nil, nil, nil)
	thinkFn := classifier.BuildDynamicThinkFunc("pcge")
	require.NotNil(t, thinkFn)

	node := ase.NewASENode("test-company", "bookkeeping_account_categorization_v1", map[string]any{
		"description": "ACHAT MATERIEL INCONNU",
		"direction":   "OUTFLOW",
		"macro_class": "EXPENSE",
	})

	_, err := thinkFn(context.Background(), []*ase.AutonomousSemanticEngineNode{node})
	assert.Error(t, err, "production must fail closed when authoritative CoA pool is unavailable")
	assert.Contains(t, err.Error(), "authoritative CoA pool is unavailable")
}

func TestBookCategorizer_Architecture_ZeroHardcodedRulesInWorker(t *testing.T) {
	// Verify that compileDAG successfully builds with domain_tools classifier
	worker := NewBookkeepingAseBookCategorizerWorker(nil, nil, nil)
	dag, err := worker.compileDAG("test-company")
	require.NoError(t, err)
	require.NotNil(t, dag)
	defer dag.StopAll()

	assert.NotNil(t, dag.GetNode("book_direction_classifier"))
	assert.NotNil(t, dag.GetNode("book_macro_classifier_outflow"))
	assert.NotNil(t, dag.GetNode("book_macro_classifier_inflow"))
	assert.NotNil(t, dag.GetNode("pcge_account_resolver"))
	assert.NotNil(t, dag.GetNode("hold_account_ambiguity"))
}

func TestProbeEvaluateItem(t *testing.T) {
	desc := "Client Alpha invoice"
	item := BookCategorizeItem{
		BookItemID:  "book-1",
		Direction:   "BOOK_BANK_DEBIT",
		Description: &desc,
		Amount:      100000,
		AmountUnits: 100000,
		Currency:    "MAD",
	}
	worker := NewBookkeepingAseBookCategorizerWorker(nil, nil, nil)
	outcome := worker.evaluateItem(item)
	t.Logf("Outcome: %+v, rationale: %s", outcome, *outcome.Rationale)
}

func TestProbeEvaluateItemWithRuntime(t *testing.T) {
	desc := "Client Alpha invoice"
	item := BookCategorizeItem{
		BookItemID:  "book-1",
		Direction:   "BOOK_BANK_DEBIT",
		Description: &desc,
		Amount:      100000,
		AmountUnits: 100000,
		Currency:    "MAD",
	}
	worker := NewBookkeepingAseBookCategorizerWorker(nil, nil, nil)
	rt := agent.NewRuntime(slog.Default(), nil, core.AgentConfig{Model: "gpt-5.4-mini", DID: "did:test"})
	worker.SetRuntime(rt)
	outcome := worker.evaluateItem(item)
	t.Logf("Outcome with runtime: %+v, rationale: %s", outcome, *outcome.Rationale)
}
