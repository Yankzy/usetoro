package pcm_cash

import (
	"testing"
)

func TestAnnotationFromPayload_NodeInjectedHold(t *testing.T) {
	rec := ExportRecord{
		RawDescription:   "COMMISSIONS BANCAIRES JUILLET",
		Amount:           110.0,
		Direction:        "OUTFLOW",
		AccountCode:      "614700",
		TaxRuleCode:      "CGI Art. 89 (TVA Bancaire 10%)",
		DocumentRequired: "Avis d'opéré bancaire / Relevé d'agios",
		Instruction:      "Vérifier la décomposition HT et TVA 10% sur l'avis d'opéré bancaire émis par la banque.",
		ComplianceStatus: "WARNING",
	}
	ann := AnnotationFromPayload(rec)
	if ann.ComplianceStatus != StatusWarning {
		t.Errorf("expected StatusWarning, got %s", ann.ComplianceStatus)
	}
	if ann.TaxRuleCode != "CGI Art. 89 (TVA Bancaire 10%)" {
		t.Errorf("expected CGI Art. 89, got %s", ann.TaxRuleCode)
	}
	if ann.DocumentRequired != "Avis d'opéré bancaire / Relevé d'agios" {
		t.Errorf("expected Avis d'opéré bancaire, got %s", ann.DocumentRequired)
	}
}

func TestAnnotationFromPayload_BlockingSeverity(t *testing.T) {
	rec := ExportRecord{
		RawDescription:   "ACHAT FOURNITURES ESPECES",
		Amount:           300.0,
		Direction:        "OUTFLOW",
		AccountCode:      "516100",
		TaxRuleCode:      "CGI Art. 210 & 145 (Solde Caisse Créditeur Interdit)",
		DocumentRequired: "Pièce d'approvisionnement caisse (Retrait bancaire 5115 / Apport)",
		ComplianceStatus: "BLOCKING",
		HoldReason:       "Petty cash voucher would cause Compte 5161 balance to drop below 0 MAD.",
	}
	ann := AnnotationFromPayload(rec)
	if ann.ComplianceStatus != StatusActionRequired {
		t.Errorf("BLOCKING should map to StatusActionRequired, got %s", ann.ComplianceStatus)
	}
	if ann.TaxRuleCode != "CGI Art. 210 & 145 (Solde Caisse Créditeur Interdit)" {
		t.Errorf("unexpected tax rule: %s", ann.TaxRuleCode)
	}
}

func TestAnnotationFromPayload_CompliantNoAnnotations(t *testing.T) {
	rec := ExportRecord{
		RawDescription: "VIREMENT RECU CLIENT MAROC",
		Amount:         5000.0,
		Direction:      "INFLOW",
		AccountCode:    "342100",
	}
	ann := AnnotationFromPayload(rec)
	if ann.ComplianceStatus != StatusCompliant {
		t.Errorf("expected StatusCompliant for unannotated record, got %s", ann.ComplianceStatus)
	}
}

func TestAnnotationFromPayload_HoldReasonFallback(t *testing.T) {
	rec := ExportRecord{
		RawDescription: "PRELEVEMENT INCONNU",
		Amount:         800.0,
		Direction:      "OUTFLOW",
		AccountCode:    "471000",
		HoldReason:     "Bank statement description is ambiguous and requires human review.",
	}
	ann := AnnotationFromPayload(rec)
	if ann.ComplianceStatus != StatusActionRequired {
		t.Errorf("expected StatusActionRequired when HoldReason present, got %s", ann.ComplianceStatus)
	}
	if ann.Instruction != "Bank statement description is ambiguous and requires human review." {
		t.Errorf("expected HoldReason to populate Instruction, got %q", ann.Instruction)
	}
}

func TestExtractICEFromEnrichment_Valid(t *testing.T) {
	enrichmentJSON := []byte(`{
		"counterparty": {
			"normalized_name": "Papeterie Centrale SARL",
			"identifiers": {
				"ice": "001524398000045"
			}
		}
	}`)
	ice, valid := ExtractICEFromEnrichment(enrichmentJSON)
	if ice != "001524398000045" {
		t.Errorf("expected ICE '001524398000045', got %s", ice)
	}
	if !valid {
		t.Error("expected valid ICE")
	}
}

func TestExtractICEFromEnrichment_Empty(t *testing.T) {
	ice, valid := ExtractICEFromEnrichment(nil)
	if ice != "" || valid {
		t.Error("expected empty ICE and invalid for nil input")
	}
}
