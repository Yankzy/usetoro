package accounting

import (
	"testing"
	"time"
)

func validStage2Output() Stage2Output {
	return Stage2Output{
		SchemaVersion:  Stage2SchemaVersion,
		GeneratedAt:    time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
		Context:        Stage2WorkflowContext{EntityID: "entity-1", RealmID: "atlas", SessionID: "session-1", SourceDocumentIDs: []string{"document-1"}, StatementPeriodKey: "2026-07", StatementOpeningBalance: "100.0000", StatementClosingBalance: "80.0000"},
		BankLine:       Stage2BankLine{StagingTransactionID: "staging-1", BankStatementLineID: "bank-line-1", BankAccountID: "bank-1", BankLedgerAccountCode: "514100", OperationDate: "2026-07-03", Direction: "OUTFLOW", Amount: "120.0000", Currency: "MAD"},
		Classification: Stage2Classification{Intent: "BANK_FEE_COMMISSION", Confidence: .99},
		Outcome:        Stage2ProposedTreatment,
		Treatment: &ProposedAccountingTreatment{EntryDate: "2026-07-03", Currency: "MAD", Label: "Bank fee", Lines: []ProposedJournalLine{
			{LineIndex: 0, AccountID: "expense-1", AccountCode: "614700", Label: "Bank fee", Debit: "120.0000", Credit: "0.0000"},
			{LineIndex: 1, AccountID: "bank-ledger-1", AccountCode: "514100", Label: "Bank fee", Debit: "0.0000", Credit: "120.0000"},
		}},
	}
}

func TestStage2OutputValidatesBalancedSingleBankLine(t *testing.T) {
	output := validStage2Output()
	if err := output.Validate(); err != nil {
		t.Fatalf("expected valid proposal: %v", err)
	}
	first, err := output.Hash()
	if err != nil {
		t.Fatal(err)
	}
	second, _ := output.Hash()
	if first != second || len(first) != 64 {
		t.Fatalf("expected stable SHA-256 hash, got %q and %q", first, second)
	}
}

func TestStage2OutputRejectsWrongPostingDateAndUnbalancedLines(t *testing.T) {
	output := validStage2Output()
	output.Treatment.EntryDate = "2026-07-04"
	if err := output.Validate(); err == nil {
		t.Fatal("expected operation-date mismatch to fail")
	}
	output = validStage2Output()
	output.Treatment.Lines[0].Debit = "119.9999"
	if err := output.Validate(); err == nil {
		t.Fatal("expected unbalanced proposal to fail")
	}
}

func TestStage2HoldCannotSmuggleJournal(t *testing.T) {
	output := validStage2Output()
	output.Outcome = Stage2HumanReview
	output.ReviewReason = "counterpart account is ambiguous"
	if err := output.Validate(); err == nil {
		t.Fatal("expected hold with journal payload to fail")
	}
}
