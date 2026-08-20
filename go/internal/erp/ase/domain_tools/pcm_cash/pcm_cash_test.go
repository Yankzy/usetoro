package pcm_cash

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
)

func TestPettyCashCaisseCreditricePrevention(t *testing.T) {
	// Current 5161 balance = 1,000 MAD
	// Attempted cash outflow = 1,500 MAD
	// Expect error: ErrCaisseCreditricePrevented
	currentBalance := 1000.00
	attemptedOutflow := 1500.00

	res, err := EvaluatePettyCashVoucher(currentBalance, attemptedOutflow, true, 0.0)
	if err == nil {
		t.Fatalf("expected error for caisse creditrice violation, got nil")
	}
	if !res.IsCaisseCreditrice {
		t.Errorf("expected IsCaisseCreditrice=true, got false")
	}
	if res.ProjectedBalance >= 0 {
		t.Errorf("expected negative projected balance (-500 MAD), got %.2f", res.ProjectedBalance)
	}
}

func TestPettyCashDailyCapExceeded(t *testing.T) {
	// Supplier daily total so far = 4,000 MAD TTC
	// New cash outflow voucher = 1,500 MAD TTC
	// Total = 5,500 MAD TTC > 5,000 MAD TTC (CGI Art. 193 limit)
	currentBalance := 10000.00
	previousDailyTotal := 4000.00
	newVoucherAmount := 1500.00

	res, err := EvaluatePettyCashVoucher(currentBalance, newVoucherAmount, true, previousDailyTotal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsCapExceeded {
		t.Errorf("expected IsCapExceeded=true, got false")
	}
	if res.IsVATDeductible {
		t.Errorf("expected IsVATDeductible=false due to CGI Art. 193 cash limit, got true")
	}
	if res.VatRestrictedAccount != "6181" {
		t.Errorf("expected restricted account '6181', got '%s'", res.VatRestrictedAccount)
	}
}

func TestBankFeeAutoSplit(t *testing.T) {
	// Commission description = "COMMISSIONS BANCAIRES JUILLET"
	// Total TTC = 110.00 MAD
	// HT = 100.00 MAD (Account 614700)
	// VAT 10% = 10.00 MAD (Account 345520)
	amountTTC := 110.00
	desc := "COMMISSIONS BANCAIRES JUILLET"

	if !IsBankFeeDescription(desc) {
		t.Fatalf("expected IsBankFeeDescription=true for '%s'", desc)
	}

	split, err := SplitBankFee(amountTTC, desc)
	if err != nil {
		t.Fatalf("failed to split bank fee: %v", err)
	}

	if split.AmountHT != 100.00 {
		t.Errorf("expected HT = 100.00 MAD, got %.2f", split.AmountHT)
	}
	if split.AmountVAT != 10.00 {
		t.Errorf("expected VAT = 10.00 MAD, got %.2f", split.AmountVAT)
	}
	if split.ExpenseAccount != "614700" {
		t.Errorf("expected expense account '614700', got '%s'", split.ExpenseAccount)
	}
	if split.VATAccount != "345520" {
		t.Errorf("expected VAT account '345520', got '%s'", split.VATAccount)
	}
}

func TestSIMPLTVAFieldsValidationAndXMLGeneration(t *testing.T) {
	rec := &SimplTVAFieldRecord{
		PaymentDate:      time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC),
		PaymentMode:      "VIREMENT",
		ReferenceNum:     "VIR-89102",
		CounterpartyName: "SOCIETE MAROCAINE DE DISTRIBUTION SARL",
		ICE:              "001523456000088", // Valid 15 digits
		AmountHT:         10000.00,
		AmountVAT:        2000.00,
		AmountTTC:        12000.00,
		VATRate:          0.20,
	}

	errs := ValidateSimplTVARecord(rec)
	if len(errs) > 0 {
		t.Fatalf("expected 0 validation errors, got: %v", errs)
	}

	xmlData, err := GenerateSIMPLTVAXML("12345678", 2026, 7, []*SimplTVAFieldRecord{rec})
	if err != nil {
		t.Fatalf("failed to generate SIMPL-TVA XML: %v", err)
	}

	xmlStr := string(xmlData)
	if !strings.Contains(xmlStr, "001523456000088") {
		t.Errorf("XML missing ICE number")
	}
	if !strings.Contains(xmlStr, "VIR-89102") {
		t.Errorf("XML missing reference number")
	}
}

func TestTransitAccountZeroNetting(t *testing.T) {
	outgoing := []TransitTransferLeg{
		{
			ID:            "leg-1",
			LegType:       TransitLegOutgoing,
			SourceAccount: "514101", // Bank A
			Amount:        50000.00,
			Timestamp:     time.Now(),
		},
	}
	incoming := []TransitTransferLeg{
		{
			ID:            "leg-2",
			LegType:       TransitLegIncoming,
			TargetAccount: "514102", // Bank B
			Amount:        50000.00,
			Timestamp:     time.Now(),
		},
	}

	res, err := ReconcileTransitAccount(outgoing, incoming)
	if err != nil {
		t.Fatalf("expected clean reconciliation to 0 MAD, got error: %v", err)
	}
	if !res.IsCleared {
		t.Errorf("expected IsCleared=true")
	}
	if res.NetBalance != 0.0 {
		t.Errorf("expected NetBalance=0.0, got %.2f", res.NetBalance)
	}
}

func TestRASTaxWithholdingForeignServices(t *testing.T) {
	// Test 10% Foreign Services RAS (CGI Art. 15)
	// Foreign SaaS Gross = $1,000.00 USD
	// 10% RAS = $100.00 USD (Account 445800)
	// Net Bank Outflow = $900.00 USD
	gross := 1000.00
	ras, err := CalculateRASWithholding(gross, RASTypeForeignService, "USD")
	if err != nil {
		t.Fatalf("failed to calculate foreign service RAS: %v", err)
	}

	if ras.WithholdingAmount != 100.00 {
		t.Errorf("expected 100.00 USD withholding, got %.2f", ras.WithholdingAmount)
	}
	if ras.NetBankOutflow != 900.00 {
		t.Errorf("expected 900.00 USD net bank outflow, got %.2f", ras.NetBankOutflow)
	}
	if ras.WithholdingAccount != "445800" {
		t.Errorf("expected withholding account '445800', got '%s'", ras.WithholdingAccount)
	}
}

func TestExecuteActionPcmTools(t *testing.T) {
	// Test PcmPettyCashTool ExecuteAction
	pettyTool := &PcmPettyCashTool{}
	node := ase.NewASENode("tenant-1", "pcm_petty_cash_dag", map[string]interface{}{
		"current_balance":    1000.0,
		"raw_amount":         "200.0",
		"cash_direction":     "OUTFLOW",
		"vendor_daily_total": 4000.0,
	})

	err := pettyTool.ExecuteAction(context.Background(), "petty_cash_guardrails", node)
	if err != nil {
		t.Fatalf("unexpected error running petty_cash_guardrails action: %v", err)
	}
	if node.Payload["is_caisse_creditrice"] != false {
		t.Errorf("expected is_caisse_creditrice=false")
	}

	// Test PcmBankCashTool ExecuteAction for bank fee
	bankTool := &PcmBankCashTool{}
	bankNode := ase.NewASENode("tenant-1", "pcm_bank_cash_accounting_dag", map[string]interface{}{
		"raw_description": "COMMISSIONS BANCAIRES",
		"raw_amount":      "110.0",
	})
	err = bankTool.ExecuteAction(context.Background(), "bank_fee_splitter", bankNode)
	if err != nil {
		t.Fatalf("unexpected error running bank_fee_splitter action: %v", err)
	}
	if bankNode.Payload["bank_fee_processed"] != true {
		t.Errorf("expected bank_fee_processed=true")
	}
}

func TestHydrateNodeFromPersistedEnrichment(t *testing.T) {
	node := ase.NewASENode("tenant-1", "pcm_bank_cash_accounting_dag", map[string]interface{}{
		"raw_description": "PRLV AWS EMEA SOFTWARE SUBSCRIPTION",
		"raw_amount":      "12000.00",
		"cash_direction":  "OUTFLOW",
	})

	persistedJSON := []byte(`{
		"confidence_score": 0.98,
		"counterparty": {
			"normalized_name": "Amazon Web Services (AWS)",
			"category": "FOREIGN_SAAS",
			"is_foreign_service": true,
			"identifiers": {
				"ice": "001524398000045"
			}
		},
		"tax_and_compliance": {
			"ras_applicable": true,
			"ras_rate": 0.10,
			"ras_amount_mad": 1200.00,
			"ras_account": "445800",
			"statutory_guardrails": {
				"guardrail_status": "PASS"
			}
		},
		"pcgm_accounting": {
			"suggested_account": "614400",
			"account_label": "Redevances pour brevets, licences, marques",
			"default_tva_rate": 0.20,
			"net_ht_amount": 10000.00,
			"tva_amount": 2000.00
		}
	}`)

	hydrateNodeFromPersistedEnrichment(node, database.FignodeStagingTransaction{}, persistedJSON)

	if node.Payload["normalized_merchant"] != "Amazon Web Services (AWS)" {
		t.Errorf("expected normalized_merchant='Amazon Web Services (AWS)', got %v", node.Payload["normalized_merchant"])
	}
	if node.Payload["suggested_account"] != "614400" {
		t.Errorf("expected suggested_account='614400', got %v", node.Payload["suggested_account"])
	}
	if node.Payload["is_foreign_service"] != true {
		t.Errorf("expected is_foreign_service=true, got %v", node.Payload["is_foreign_service"])
	}
	if node.Payload["ras_withholding_amount"] != 1200.00 {
		t.Errorf("expected ras_withholding_amount=1200.00, got %v", node.Payload["ras_withholding_amount"])
	}
	if node.Payload["ice_number"] != "001524398000045" {
		t.Errorf("expected ice_number='001524398000045', got %v", node.Payload["ice_number"])
	}
	if node.Payload["guardrail_status"] != "PASS" {
		t.Errorf("expected guardrail_status='PASS', got %v", node.Payload["guardrail_status"])
	}
}


