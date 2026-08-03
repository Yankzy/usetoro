package pnm

import (
	"strings"
	"testing"
	"time"
)

func TestFormatPNM_DefaultTemplate(t *testing.T) {
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	lines := []JournalEntryLine{
		{
			JournalCode: "ACH",
			Date:        now,
			GeneralAcc:  "61440000",
			AuxAcc:      "",
			PieceRef:    "FACT-4412",
			Libelle:     "Maroc Telecom Invoice HT",
			Debit:       12500.00,
			Credit:      0.00,
		},
		{
			JournalCode: "ACH",
			Date:        now,
			GeneralAcc:  "34552000",
			AuxAcc:      "",
			PieceRef:    "FACT-4412",
			Libelle:     "TVA Recup 20%",
			Debit:       2500.00,
			Credit:      0.00,
		},
		{
			JournalCode: "ACH",
			Date:        now,
			GeneralAcc:  "44110000",
			AuxAcc:      "IAM001",
			PieceRef:    "FACT-4412",
			Libelle:     "Maroc Telecom TTC",
			Debit:       0.00,
			Credit:      15000.00,
		},
	}

	if !BalanceCheck(lines) {
		t.Fatalf("Expected lines to be balanced, but BalanceCheck returned false")
	}

	output := string(FormatPNM(lines, DefaultTemplate()))

	expectedRows := []string{
		"ACH;150726;61440000;;FACT-4412;Maroc Telecom Invoice HT;12500.00;0.00",
		"ACH;150726;34552000;;FACT-4412;TVA Recup 20%;2500.00;0.00",
		"ACH;150726;44110000;IAM001;FACT-4412;Maroc Telecom TTC;0.00;15000.00",
	}

	for _, exp := range expectedRows {
		if !strings.Contains(output, exp) {
			t.Errorf("Expected output to contain line: %s\nGot output:\n%s", exp, output)
		}
	}
}

func TestBalanceCheck_Unbalanced(t *testing.T) {
	lines := []JournalEntryLine{
		{Debit: 100.00, Credit: 0.00},
		{Debit: 0.00, Credit: 80.00},
	}

	if BalanceCheck(lines) {
		t.Errorf("Expected BalanceCheck to return false for unbalanced entry")
	}
}
