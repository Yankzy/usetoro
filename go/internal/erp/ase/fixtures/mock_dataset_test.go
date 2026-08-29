package fixtures

import (
	"testing"
)

func TestLoadMockTransactions(t *testing.T) {
	txns, err := LoadMockTransactions()
	if err != nil {
		t.Fatalf("failed to load mock transactions: %v", err)
	}

	if len(txns) != 101 {
		t.Fatalf("expected exactly 101 mock transactions, got %d", len(txns))
	}

	inflows, err := FilterByDirection("INFLOW")
	if err != nil {
		t.Fatalf("failed to filter inflows: %v", err)
	}
	if len(inflows) != 41 {
		t.Errorf("expected 41 inflows, got %d", len(inflows))
	}

	outflows, err := FilterByDirection("OUTFLOW")
	if err != nil {
		t.Fatalf("failed to filter outflows: %v", err)
	}
	if len(outflows) != 60 {
		t.Errorf("expected 60 outflows, got %d", len(outflows))
	}

	// Verify sample node creation
	tx, err := GetMockTransactionByEdge("CUSTOMER_INVOICE_RECEIPT_3421")
	if err != nil {
		t.Fatalf("failed to get CUSTOMER_INVOICE_RECEIPT_3421: %v", err)
	}
	node := tx.ToASENode("pcm_bank_cash_accounting_dag", "test_session")
	if node == nil {
		t.Fatal("expected non-nil ASE node")
	}
	if node.Payload["raw_description"] != tx.RawDescription {
		t.Errorf("expected raw_description %s, got %v", tx.RawDescription, node.Payload["raw_description"])
	}
}
