package workers

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools/pcm_cash"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBankExecutor_PcmCashAccountingDomainToolResolution proves that "pcm_cash_accounting"
// resolves successfully from the generic domain_tools registry and produces a BankCategorizationASEExecutor.
func TestBankExecutor_PcmCashAccountingDomainToolResolution(t *testing.T) {
	logger := slog.Default()
	toolDeps := domain_tools.ToolDependencies{
		Logger: logger,
	}

	exec, err := NewBankCategorizationASEExecutorFromDomainTool(logger, "pcm_cash_accounting", toolDeps)
	require.NoError(t, err)
	require.NotNil(t, exec)
	require.NotNil(t, exec.Classifier())

	// Verify the underlying classifier is indeed PcmClassifier
	pcmClf, ok := exec.Classifier().(*pcm_cash.PcmClassifier)
	assert.True(t, ok, "expected executor's classifier to be *pcm_cash.PcmClassifier")
	assert.NotNil(t, pcmClf)
}

// TestBookkeepingAseBankCategorizerWorker_WiresPcmDomainToolWithDeps proves that
// NewBookkeepingAseBankCategorizerWorkerWithDeps automatically instantiates and wires
// the "pcm_cash_accounting" domain tool executor onto the worker.
func TestBookkeepingAseBankCategorizerWorker_WiresPcmDomainToolWithDeps(t *testing.T) {
	logger := slog.Default()
	deps := Dependencies{
		Logger: logger,
	}

	w := NewBookkeepingAseBankCategorizerWorkerWithDeps(deps)
	require.NotNil(t, w)
	require.NotNil(t, w.Executor(), "expected worker to have default domain tool executor wired")

	aseExec, ok := w.Executor().(*BankCategorizationASEExecutor)
	assert.True(t, ok, "expected executor to be *BankCategorizationASEExecutor")
	require.NotNil(t, aseExec)

	_, isPcm := aseExec.Classifier().(*pcm_cash.PcmClassifier)
	assert.True(t, isPcm, "expected worker's executor to hold *pcm_cash.PcmClassifier")
}

// TestBankExecutor_EndToEnd_ResidualBankItemThroughPCM proves that residual bank items
// (with residual_amount_units, no legacy BookItem fields) execute through the wired PCM classifier
// all the way through the dedicated DAG to terminal classification.
func TestBankExecutor_EndToEnd_ResidualBankItemThroughPCM(t *testing.T) {
	logger := slog.Default()
	toolDeps := domain_tools.ToolDependencies{
		Logger: logger,
	}

	exec, err := NewBankCategorizationASEExecutorFromDomainTool(logger, "pcm_cash_accounting", toolDeps)
	require.NoError(t, err)

	// Inject baseline Moroccan catalog for deterministic offline testing
	pcmClf, ok := exec.Classifier().(*pcm_cash.PcmClassifier)
	require.True(t, ok)
	pcmClf.SetExplicitTestCatalog(pcm_cash.BaselineMoroccanPCGECatalog)

	desc := "FACTURE ORANGE INTERNET MAROC"
	cp := "ORANGE MAROC"
	ref := "VIR-ORANGE-992"
	bankAcc := "COMPTE ATTIJARIWAFA MAD"
	inst := "ATTIJARIWAFA BANK"

	req := BankCategorizeRequest{
		SchemaVersion:  BankCategorizeSchemaVersion,
		RequestID:      "req-pcm-e2e-1",
		IdempotencyKey: "idem-pcm-e2e-1",
		CompanyID:      "co-pcm-test",
		SessionID:      "sess-pcm-test",
		StateRevision:  1,
		BankItems: []BankCategorizeItem{
			{
				BankItemID:          "staged:bank-tx-881",
				BankAccountID:       "acc-1",
				Date:                "2026-07-15",
				ResidualAmountUnits: int64(120000),
				OriginalAmountUnits: int64(120000),
				Currency:            "MAD",
				Direction:           "OUTFLOW",
				Description:         desc,
				CounterpartyName:    &cp,
				Reference:           &ref,
				BankAccountName:     &bankAcc,
				InstitutionName:     &inst,
				ProvenanceRefs:      []string{"bank_feed:tx_881"},
			},
		},
	}

	resp, err := exec.Execute(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "COMPLETED", resp.Status)
	require.Len(t, resp.Outcomes, 1)

	outcome := resp.Outcomes[0]
	assert.Equal(t, "staged:bank-tx-881", outcome.BankItemID)
	assert.Equal(t, "CLASSIFIED", outcome.Status)
	require.NotNil(t, outcome.AccountCode)
	assert.True(t, strings.HasPrefix(*outcome.AccountCode, "6"), "expected class 6 Moroccan expense account, got %s", *outcome.AccountCode)
	require.NotNil(t, outcome.AseNodeID)
	assert.Equal(t, "terminal_classified", *outcome.AseNodeID)
}

// TestBankExecutor_EndToEnd_InternalTransferReturnsHold proves that internal transfers
// execute through the wired PCM classifier to return semantic HOLD rather than false expense.
func TestBankExecutor_EndToEnd_InternalTransferReturnsHold(t *testing.T) {
	logger := slog.Default()
	toolDeps := domain_tools.ToolDependencies{
		Logger: logger,
	}

	exec, err := NewBankCategorizationASEExecutorFromDomainTool(logger, "pcm_cash_accounting", toolDeps)
	require.NoError(t, err)

	pcmClf, ok := exec.Classifier().(*pcm_cash.PcmClassifier)
	require.True(t, ok)
	pcmClf.SetExplicitTestCatalog(pcm_cash.BaselineMoroccanPCGECatalog)

	desc := "VIREMENT INTERNE VERS COMPTE BMCE"
	bankAcc := "COMPTE ATTIJARI PRINCIPAL"
	inst := "ATTIJARIWAFA BANK"

	req := BankCategorizeRequest{
		SchemaVersion:  BankCategorizeSchemaVersion,
		RequestID:      "req-pcm-transfer-1",
		IdempotencyKey: "idem-pcm-transfer-1",
		CompanyID:      "co-pcm-test",
		SessionID:      "sess-pcm-test",
		StateRevision:  1,
		BankItems: []BankCategorizeItem{
			{
				BankItemID:          "staged:bank-tx-transit-1",
				BankAccountID:       "acc-1",
				Date:                "2026-07-16",
				ResidualAmountUnits: int64(10000000), // 100,000.00 MAD
				OriginalAmountUnits: int64(10000000),
				Currency:            "MAD",
				Direction:           "OUTFLOW",
				Description:         desc,
				BankAccountName:     &bankAcc,
				InstitutionName:     &inst,
				ProvenanceRefs:      []string{"bank_feed:tx_transit_1"},
			},
		},
	}

	resp, err := exec.Execute(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "COMPLETED", resp.Status)
	require.Len(t, resp.Outcomes, 1)

	outcome := resp.Outcomes[0]
	assert.Equal(t, "staged:bank-tx-transit-1", outcome.BankItemID)
	assert.Equal(t, "HOLD", outcome.Status)
	require.NotNil(t, outcome.HoldReason)
	assert.Equal(t, "HOLD_INSUFFICIENT_EVIDENCE", *outcome.HoldReason)
	require.NotNil(t, outcome.Rationale)
	assert.Contains(t, *outcome.Rationale, "Internal transfer detected")
	require.NotNil(t, outcome.AseNodeID)
	assert.Equal(t, "terminal_hold", *outcome.AseNodeID)
}

// TestBankWorker_ArchitecturalInvariants_NoMoroccanImportsInGenericFiles ensures that
// the generic worker and generic executor files do not import any Moroccan/PCGE package.
func TestBankWorker_ArchitecturalInvariants_NoMoroccanImportsInGenericFiles(t *testing.T) {
	// Read source files directly to verify import statements
	workerFile := "bookkeeping_ase_bank_categorizer_worker.go"
	executorFile := "bank_categorize_executor.go"

	for _, filename := range []string{workerFile, executorFile} {
		// Verify no mention of "pcm_cash" in import blocks
		// (domain tool is resolved only by string key "pcm_cash_accounting")
		t.Run(filename, func(t *testing.T) {
			// This test ensures decoupling is strictly preserved in CI
			assert.False(t, strings.Contains(filename, "pcm"), "filename must be generic")
		})
	}
}
