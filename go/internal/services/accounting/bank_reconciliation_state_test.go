package accounting

import (
	"testing"
	"time"
)

func TestCalculateClosingPosition(t *testing.T) {
	position := CalculateClosingPosition(
		MustReconciliationMoney("161575.7200"),
		MustReconciliationMoney("32000.0000"),
		MustReconciliationMoney("9250.0000"),
		MustReconciliationMoney("0.0000"),
		MustReconciliationMoney("184325.7200"),
	)
	if !position.Difference.IsZero() {
		t.Fatalf("expected balanced position, got difference %s", position.Difference.String())
	}
}

func TestReconciliationMoneyRejectsUnsupportedPrecision(t *testing.T) {
	if _, err := NewReconciliationMoney("1.00001"); err == nil {
		t.Fatal("expected amount with five decimal places to fail")
	}
}

func TestOneToOneMatchEligibility(t *testing.T) {
	bankDate := time.Date(2026, time.July, 2, 0, 0, 0, 0, time.UTC)
	bank := ReconciliationMovement{
		ID: "bank-1", Amount: MustReconciliationMoney("14000"), Currency: "MAD", Direction: "INFLOW", Date: bankDate,
	}
	book := ReconciliationMovement{
		ID: "journal-1", Amount: MustReconciliationMoney("14000"), Currency: "MAD", Direction: "INFLOW", Date: bankDate.AddDate(0, 0, -2),
	}
	if !IsEligibleOneToOneMatch(bank, book, 3) {
		t.Fatal("expected candidate within configured window to be eligible")
	}
	if IsEligibleOneToOneMatch(bank, book, 1) {
		t.Fatal("expected candidate outside configured window to be ineligible")
	}
	book.Amount = MustReconciliationMoney("13999.9999")
	if IsEligibleOneToOneMatch(bank, book, 3) {
		t.Fatal("expected different amounts to be ineligible")
	}
}

func TestStateHashIsMembershipOrderIndependent(t *testing.T) {
	header := []string{"state-1", "2026-07", "184325.7200"}
	a := StateHash(header, []string{"line-b", "line-a"})
	b := StateHash(header, []string{"line-a", "line-b"})
	if a != b {
		t.Fatalf("expected stable hash, got %s and %s", a, b)
	}
}

func TestBuildStateSnapshotRejectsUnbalancedClose(t *testing.T) {
	position := CalculateClosingPosition(
		MustReconciliationMoney("100"), MustReconciliationMoney("0"), MustReconciliationMoney("0"),
		MustReconciliationMoney("0"), MustReconciliationMoney("99.9999"),
	)
	_, err := BuildStateSnapshot(StateSnapshotInput{
		RealmID: "atlas", BankAccountID: "bank-1", PeriodKey: "2026-07", RequestHash: StateHash([]string{"request"}, nil), Status: "CLOSED", Currency: "MAD", CreatedBy: "accountant", Position: position,
	})
	if err == nil {
		t.Fatal("expected unbalanced closed state to be rejected")
	}
}

func TestBuildStateSnapshotReferencesWithoutDuplicatingEvidence(t *testing.T) {
	position := CalculateClosingPosition(
		MustReconciliationMoney("100"), MustReconciliationMoney("0"), MustReconciliationMoney("0"),
		MustReconciliationMoney("0"), MustReconciliationMoney("100"),
	)
	snapshot, err := BuildStateSnapshot(StateSnapshotInput{
		RealmID: "atlas", BankAccountID: "bank-1", PeriodKey: "2026-07", RequestHash: StateHash([]string{"request"}, nil), Status: "CLOSED", Currency: "MAD", CreatedBy: "accountant", Position: position,
		Memberships: []StateMembershipRef{{JournalLineID: "journal-line-1", Disposition: "CARRIED_FORWARD", CarryForward: true}},
	})
	if err != nil {
		t.Fatalf("expected valid snapshot: %v", err)
	}
	if len(snapshot.StateHash) != 64 {
		t.Fatalf("expected SHA-256 state hash, got %q", snapshot.StateHash)
	}
}
