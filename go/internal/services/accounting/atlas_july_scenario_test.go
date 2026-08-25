package accounting

import (
	"testing"
	"time"
)

// TestAtlasJulyAcceptanceScenario exercises the deterministic contracts at
// every persistence boundary. Database-backed worker tests cover the writes;
// this scenario protects the financial chain and the exact next opening.
func TestAtlasJulyAcceptanceScenario(t *testing.T) {
	stage2 := Stage2Output{
		SchemaVersion: Stage2SchemaVersion,
		Context: Stage2WorkflowContext{
			EntityID: "atlas-entity", RealmID: "rap_atlas_sarl",
			WorkflowID: "atlas-july-workflow", WorkflowTraceID: "postmark/atlas-july-workflow",
			SessionID: "atlas-july-staging", SourceDocumentIDs: []string{"atlas-july-statement"},
			StatementPeriodKey: "2026-07", StatementOpeningBalance: "184325.7200", StatementClosingBalance: "184075.7200",
		},
		BankLine: Stage2BankLine{
			StagingTransactionID:  "00000000-0000-0000-0000-000000000101",
			BankStatementLineID:   "00000000-0000-0000-0000-000000000201",
			BankAccountID:         "00000000-0000-0000-0000-000000000301",
			BankLedgerAccountCode: "514100", OperationDate: "2026-07-02",
			Direction: "OUTFLOW", Amount: "250.0000", Currency: "MAD", Description: "FRAIS BANCAIRES",
		},
		Classification: Stage2Classification{Intent: "BANK_FEE_COMMISSION", Confidence: 0.99},
		Outcome:        Stage2ProposedTreatment,
		Treatment: &ProposedAccountingTreatment{
			EntryDate: "2026-07-02", Currency: "MAD", Label: "FRAIS BANCAIRES",
			Lines: []ProposedJournalLine{
				{LineIndex: 0, AccountID: "00000000-0000-0000-0000-000000000401", AccountCode: "627100", Label: "FRAIS BANCAIRES", Debit: "250.0000", Credit: "0.0000"},
				{LineIndex: 1, AccountID: "00000000-0000-0000-0000-000000000402", AccountCode: "514100", Label: "FRAIS BANCAIRES", Debit: "0.0000", Credit: "250.0000"},
			},
		},
	}
	proposalHash, err := stage2.Hash()
	if err != nil || len(proposalHash) != 64 {
		t.Fatalf("Atlas Stage-2 proposal must be balanced and hashable: hash=%q err=%v", proposalHash, err)
	}

	bankDate := time.Date(2026, time.July, 2, 0, 0, 0, 0, time.UTC)
	if !IsEligibleOneToOneMatch(
		ReconciliationMovement{ID: stage2.BankLine.BankStatementLineID, Amount: MustReconciliationMoney("250"), Currency: "MAD", Direction: "OUTFLOW", Date: bankDate},
		ReconciliationMovement{ID: "atlas-bank-journal-line", Amount: MustReconciliationMoney("250.0000"), Currency: "MAD", Direction: "OUTFLOW", Date: bankDate},
		3,
	) {
		t.Fatal("posted Atlas bank-fee journal must be an exact 1:1 candidate")
	}

	carried := []StateMembershipRef{
		{JournalLineID: "atlas-outstanding-deposit", Disposition: "CARRIED_FORWARD", CarryForward: true},
		{JournalLineID: "atlas-outstanding-cheque", Disposition: "CARRIED_FORWARD", CarryForward: true},
	}
	junePosition := CalculateClosingPosition(
		MustReconciliationMoney("161575.7200"), MustReconciliationMoney("32000"),
		MustReconciliationMoney("9250"), MustReconciliationMoney("0"), MustReconciliationMoney("184325.7200"),
	)
	junePosition.StatementOpeningBalance = MustReconciliationMoney("150000")
	junePosition.BookOpeningBalance = MustReconciliationMoney("150000")
	june, err := BuildStateSnapshot(StateSnapshotInput{
		RealmID: "rap_atlas_sarl", BankAccountID: stage2.BankLine.BankAccountID,
		PeriodKey: "2026-06", Revision: 1, StateKind: "MIGRATED_BASELINE",
		IdempotencyKey: "atlas-june-baseline", RequestHash: StateHash([]string{"atlas-june-request"}, nil),
		Status: "CLOSED", Currency: "MAD", CreatedBy: "atlas-accountant",
		Position: junePosition, Memberships: carried,
	})
	if err != nil || !june.Position.Difference.IsZero() {
		t.Fatalf("June baseline must close exactly: %v", err)
	}

	julyPosition := CalculateClosingPosition(
		MustReconciliationMoney("161325.7200"), MustReconciliationMoney("32000"),
		MustReconciliationMoney("9250"), MustReconciliationMoney("0"), MustReconciliationMoney("184075.7200"),
	)
	julyPosition.StatementOpeningBalance = june.Position.StatementBalance
	julyPosition.BookOpeningBalance = june.Position.BookBankBalance
	julyClosed, err := BuildStateSnapshot(StateSnapshotInput{
		RealmID: "rap_atlas_sarl", BankAccountID: stage2.BankLine.BankAccountID,
		PeriodKey: "2026-07", Revision: 2,
		PreviousStateID: "00000000-0000-0000-0000-000000000501", StateKind: "PERIOD",
		IdempotencyKey: "atlas-july-close", RequestHash: StateHash([]string{"atlas-july-close", proposalHash}, nil),
		Status: "CLOSED", Currency: "MAD", CreatedBy: "atlas-accountant",
		Position: julyPosition, Memberships: carried,
	})
	if err != nil || !julyClosed.Position.Difference.IsZero() {
		t.Fatalf("July must close with explicit outstanding carry-forward: %v", err)
	}

	augustOpeningStatement := julyClosed.Position.StatementBalance
	augustOpeningBook := julyClosed.Position.BookBankBalance
	if augustOpeningStatement.String() != "184075.7200" || augustOpeningBook.String() != "161325.7200" {
		t.Fatalf("next opening must derive from July close; got statement=%s book=%s", augustOpeningStatement.String(), augustOpeningBook.String())
	}
}
