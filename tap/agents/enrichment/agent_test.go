package enrichment

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Yankzy/usetoro/internal/database"
)

func TestEnrichmentAgent_enrichRow(t *testing.T) {
	// Initialize an agent with nil EntityResolver purely to test mapping/branch logic
	mockAgent := &EnrichmentAgent{}

	ctx := context.Background()
	realmID := "realm-123"

	// Test Case 1: Money Out (Expense) should favor PredictedVendorName string
	rowMoneyOut := database.GetPendingSessionRowsRow{
		RawAmount:             "-150.00",
		RawDescription:        pgtype.Text{String: "AWS Services", Valid: true},
		PredictedVendorName:   pgtype.Text{String: "Amazon Web Services", Valid: true},
		PredictedCustomerName: pgtype.Text{String: "Ignored Customer", Valid: true},
	}

	resMoneyOut, err := mockAgent.enrichRow(ctx, realmID, rowMoneyOut)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resMoneyOut.RawVendorName != "Amazon Web Services" {
		t.Errorf("expected vendor to be Amazon Web Services, got %s", resMoneyOut.RawVendorName)
	}
	if resMoneyOut.RawAmount != -150.00 {
		t.Errorf("expected amount -150.00, got %v", resMoneyOut.RawAmount)
	}

	// Test Case 2: Money In (Income) should plumb PredictedCustomerName
	rowMoneyIn := database.GetPendingSessionRowsRow{
		RawAmount:             "500.00",
		RawDescription:        pgtype.Text{String: "Client Payment", Valid: true},
		PredictedVendorName:   pgtype.Text{String: "Ignored Vendor", Valid: true},
		PredictedCustomerName: pgtype.Text{String: "Acme Corp", Valid: true},
	}

	resMoneyIn, err := mockAgent.enrichRow(ctx, realmID, rowMoneyIn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resMoneyIn.RawCustomerName != "Acme Corp" {
		t.Errorf("expected customer to be Acme Corp, got %s", resMoneyIn.RawCustomerName)
	}
	if resMoneyIn.RawAmount != 500.00 {
		t.Errorf("expected amount 500.00, got %v", resMoneyIn.RawAmount)
	}
}
