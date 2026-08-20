package pcm_cash

import (
	"bytes"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestGenerateMoroccanBookkeepingWorkbook(t *testing.T) {
	records := []ExportRecord{
		{
			ID:             "tx-1",
			DateStr:        "15/07/2026",
			RawDescription: "COMMISSIONS BANCAIRES JUILLET",
			Amount:         110.00,
			Direction:      "OUTFLOW",
			AccountCode:    "614700",
			AuxiliaryCode:  "F_BQ",
			Counterparty:   "Attijariwafa Bank",
			StatementType:  "BANK_STATEMENT",
		},
		{
			ID:             "tx-2",
			DateStr:        "16/07/2026",
			RawDescription: "LOYER COMMERCIAL BUREAU BD ANFA",
			Amount:         10000.00,
			Direction:      "OUTFLOW",
			AccountCode:    "613100",
			AuxiliaryCode:  "F_BAIL",
			Counterparty:   "Société Foncière Anfa SARL",
			StatementType:  "BANK_STATEMENT",
		},
		{
			ID:             "tx-3",
			DateStr:        "17/07/2026",
			RawDescription: "PRLV AWS EMEA SOFTWARE",
			Amount:         1000.00,
			Direction:      "OUTFLOW",
			AccountCode:    "613600",
			AuxiliaryCode:  "F_AWS",
			Counterparty:   "Amazon Web Services (AWS)",
			StatementType:  "BANK_STATEMENT",
			MoroccanEnrichment: []byte(`{
				"counterparty": {
					"normalized_name": "Amazon Web Services (AWS)",
					"is_foreign_service": true
				}
			}`),
		},
		{
			ID:             "tx-4",
			DateStr:        "18/07/2026",
			RawDescription: "ACHAT FOURNITURES ESPECES GROS",
			Amount:         6000.00,
			Direction:      "OUTFLOW",
			AccountCode:    "516100",
			AuxiliaryCode:  "F_SUP",
			Counterparty:   "Grossiste Papeterie",
			StatementType:  "PETTY_CASH",
		},
		{
			ID:             "tx-5",
			DateStr:        "19/07/2026",
			RawDescription: "VIREMENT CLIENT DISTRIBUTION SARL",
			Amount:         50000.00,
			Direction:      "INFLOW",
			AccountCode:    "342100",
			AuxiliaryCode:  "C_DISTRIB",
			Counterparty:   "Distribution SARL",
			StatementType:  "BANK_STATEMENT",
			MoroccanEnrichment: []byte(`{
				"counterparty": {
					"normalized_name": "Distribution SARL",
					"identifiers": {
						"ice": "001524398000045"
					}
				}
			}`),
		},
	}

	xlsxBytes, err := GenerateMoroccanBookkeepingWorkbook(records)
	if err != nil {
		t.Fatalf("failed to generate workbook: %v", err)
	}

	f, err := excelize.OpenReader(bytes.NewReader(xlsxBytes))
	if err != nil {
		t.Fatalf("failed to open generated xlsx buffer: %v", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	expectedSheets := []string{
		"Journal des Écritures",
		"Pièces Manquantes & Actions",
		"SIMPL-TVA (DGI Ready)",
		"Synthèse Fiscale",
	}

	for _, exp := range expectedSheets {
		found := false
		for _, s := range sheets {
			if s == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected sheet '%s' in workbook, got sheets: %v", exp, sheets)
		}
	}

	// Verify Tab 1 has rows
	rows, err := f.GetRows("Journal des Écritures")
	if err != nil || len(rows) < 6 {
		t.Fatalf("expected at least 6 rows in Journal des Écritures, got %d", len(rows))
	}

	// Verify Tab 2 has annotations
	actionRows, err := f.GetRows("Pièces Manquantes & Actions")
	if err != nil || len(actionRows) < 5 {
		t.Fatalf("expected at least 5 rows in Pièces Manquantes, got %d", len(actionRows))
	}

	// Verify Tab 3 has SIMPL records
	simplRows, err := f.GetRows("SIMPL-TVA (DGI Ready)")
	if err != nil || len(simplRows) < 5 {
		t.Fatalf("expected at least 5 rows in SIMPL-TVA, got %d", len(simplRows))
	}

	// Verify Tab 4 has KPI summary
	kpiRows, err := f.GetRows("Synthèse Fiscale")
	if err != nil || len(kpiRows) < 8 {
		t.Fatalf("expected at least 8 KPI rows in Synthèse Fiscale, got %d", len(kpiRows))
	}
}
