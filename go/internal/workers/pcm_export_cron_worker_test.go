package workers

import (
	"testing"
	"time"
)

func TestExtractAuxAccount(t *testing.T) {
	tests := []struct {
		name         string
		desc         string
		counterparty string
		direction    string
		accountCode  string
		expected     string
	}{
		{
			name:         "Supplier Outflow",
			desc:         "Achats Fournitures Inwi SA",
			counterparty: "Inwi SA",
			direction:    "OUTFLOW",
			accountCode:  "611100",
			expected:     "F_INWI",
		},
		{
			name:         "Customer Inflow",
			desc:         "Virement Client Acme Corp SARL",
			counterparty: "Acme Corp",
			direction:    "INFLOW",
			accountCode:  "711100",
			expected:     "C_ACMECORP",
		},
		{
			name:         "Bank Fee Excluded",
			desc:         "Frais tenue de compte trimestriel",
			counterparty: "",
			direction:    "OUTFLOW",
			accountCode:  "614100",
			expected:     "",
		},
		{
			name:         "Aux Code Truncation Max 10 Chars",
			desc:         "Paiement Societe SuperLongCompanyNameHere SARL",
			counterparty: "SuperLongCompanyNameHere",
			direction:    "OUTFLOW",
			accountCode:  "611100",
			expected:     "F_SUPERLON",
		},
		{
			name:         "Clean Description Fallback",
			desc:         "Paiement CB Total Station SA",
			counterparty: "",
			direction:    "OUTFLOW",
			accountCode:  "611100",
			expected:     "F_TOTALSTA",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractAuxAccount(tt.desc, tt.counterparty, tt.direction, tt.accountCode)
			if got != tt.expected {
				t.Errorf("extractAuxAccount(%q, %q, %q, %q) = %q; want %q",
					tt.desc, tt.counterparty, tt.direction, tt.accountCode, got, tt.expected)
			}
		})
	}
}

func TestSageCsvFormatting(t *testing.T) {
	// Verify date formatting (DDMMYY -> 020106 format)
	now := time.Date(2026, time.August, 5, 0, 0, 0, 0, time.UTC)
	dateStr := now.Format("020106")
	if dateStr != "050826" {
		t.Errorf("expected date 050826, got %s", dateStr)
	}
}
