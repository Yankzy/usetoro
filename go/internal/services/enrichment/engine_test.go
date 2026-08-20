package enrichment

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMultilingualArabicAliasResolution(t *testing.T) {
	engine := NewMoroccanEnrichmentEngine()
	ctx := context.Background()

	tests := []struct {
		name                 string
		rawDescription       string
		expectedMerchantName string
		expectedAccount      string
		expectedTVARate      float64
		expectedScript       ScriptType
	}{
		{
			name:                 "Arabic Script Maroc Telecom",
			rawDescription:       "PRLV اتصالات المغرب FACTURE FIBRE PRO 0826",
			expectedMerchantName: "Maroc Telecom (Itissalat Al-Maghrib S.A.)",
			expectedAccount:      "614510",
			expectedTVARate:      0.20,
			expectedScript:       ScriptArabic,
		},
		{
			name:                 "Arabic Transliterated Itissalat",
			rawDescription:       "VIR INST ITISSALAT AL-MAGHRIB ABO FIBRE",
			expectedMerchantName: "Maroc Telecom (Itissalat Al-Maghrib S.A.)",
			expectedAccount:      "614510",
			expectedTVARate:      0.20,
			expectedScript:       ScriptArabicTransliterated,
		},
		{
			name:                 "Acronym IAM",
			rawDescription:       "VIR INST IAM FACTURE 0998234",
			expectedMerchantName: "Maroc Telecom (Itissalat Al-Maghrib S.A.)",
			expectedAccount:      "614510",
			expectedTVARate:      0.20,
			expectedScript:       ScriptAcronym,
		},
		{
			name:                 "Bank Abbreviation Maroc T",
			rawDescription:       "PRLV MAROC T FACT 893420",
			expectedMerchantName: "Maroc Telecom (Itissalat Al-Maghrib S.A.)",
			expectedAccount:      "614510",
			expectedTVARate:      0.20,
			expectedScript:       ScriptBankAbbreviation,
		},
		{
			name:                 "Arabic Script Redal",
			rawDescription:       "PRLV ريضال FACTURE EAU",
			expectedMerchantName: "Redal S.A. (Veolia Maroc)",
			expectedAccount:      "614400",
			expectedTVARate:      0.07,
			expectedScript:       ScriptArabic,
		},
		{
			name:                 "Arabic Script Barid Al Maghrib",
			rawDescription:       "VIR بريد المغرب COURRIER",
			expectedMerchantName: "Barid Al-Maghrib (Poste Maroc)",
			expectedAccount:      "614510",
			expectedTVARate:      0.20,
			expectedScript:       ScriptArabic,
		},
		{
			name:                 "Arabic Script Afriquia",
			rawDescription:       "CARTE أفريقيا STATION RABAT AGDAL",
			expectedMerchantName: "Afriquia SMDC (Akwa Group)",
			expectedAccount:      "612540",
			expectedTVARate:      0.14,
			expectedScript:       ScriptArabic,
		},
		{
			name:                 "Arabic Script Attijariwafa Bank",
			rawDescription:       "VIR التجاري وفا بنك SERVICES",
			expectedMerchantName: "Attijariwafa Bank S.A.",
			expectedAccount:      "614700",
			expectedTVARate:      0.10,
			expectedScript:       ScriptArabic,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rawTxn := RawTransaction{
				TransactionID:   "txn_" + tc.name,
				RawDescription:  tc.rawDescription,
				Amount:          1200.00,
				Currency:        "MAD",
				CashDirection:   CashOutflow,
				TransactionDate: time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC),
			}

			envelope := engine.EnrichTransaction(ctx, "realm_test", rawTxn)

			if envelope.Counterparty.NormalizedName != tc.expectedMerchantName {
				t.Errorf("expected merchant %q, got %q", tc.expectedMerchantName, envelope.Counterparty.NormalizedName)
			}
			if envelope.PCGMAccounting.SuggestedAccount != tc.expectedAccount {
				t.Errorf("expected PCGM account %q, got %q", tc.expectedAccount, envelope.PCGMAccounting.SuggestedAccount)
			}
			if envelope.PCGMAccounting.DefaultTVARate != tc.expectedTVARate {
				t.Errorf("expected TVA rate %f, got %f", tc.expectedTVARate, envelope.PCGMAccounting.DefaultTVARate)
			}
			if envelope.Counterparty.MatchedAlias == nil {
				t.Fatalf("expected matched alias to be populated")
			}
			if envelope.Counterparty.MatchedAlias.ScriptType != tc.expectedScript {
				t.Errorf("expected script %v, got %v", tc.expectedScript, envelope.Counterparty.MatchedAlias.ScriptType)
			}
		})
	}
}

func TestForeignProviderWithholdingAndRunningTotals(t *testing.T) {
	engine := NewMoroccanEnrichmentEngine()
	ctx := context.Background()
	realmID := "tenant_casablanca_consulting"

	// Transaction 1: AWS Invoiced 6,000 MAD in August 2026
	txn1 := RawTransaction{
		TransactionID:   "txn_aws_01",
		RawDescription:  "CARTE 14/08 AWS EMEA SARL LUXEMBOURG US REQ 89402",
		Amount:          6000.00,
		Currency:        "MAD",
		CashDirection:   CashOutflow,
		TransactionDate: time.Date(2026, 8, 14, 9, 30, 0, 0, time.UTC),
	}

	env1 := engine.EnrichTransaction(ctx, realmID, txn1)

	if !env1.Counterparty.IsForeignService {
		t.Fatalf("expected AWS to be classified as foreign service")
	}
	if !env1.TaxAndCompliance.RASApplicable {
		t.Fatalf("expected RAS to be applicable for AWS")
	}
	if env1.TaxAndCompliance.RASRate != 0.10 {
		t.Errorf("expected 10%% RAS rate, got %f", env1.TaxAndCompliance.RASRate)
	}
	if *env1.TaxAndCompliance.RASAccount != "445800" {
		t.Errorf("expected RAS account 445800, got %s", *env1.TaxAndCompliance.RASAccount)
	}
	if env1.TaxAndCompliance.RASAmountMAD != 600.00 {
		t.Errorf("expected 600.00 MAD RAS, got %f", env1.TaxAndCompliance.RASAmountMAD)
	}
	if env1.TaxAndCompliance.NetTransferredMAD != 5400.00 {
		t.Errorf("expected 5400.00 MAD Net Transferred, got %f", env1.TaxAndCompliance.NetTransferredMAD)
	}

	rt1 := env1.TaxAndCompliance.ForeignProviderRunningTotals
	if rt1 == nil {
		t.Fatalf("expected running totals to be populated")
	}
	if rt1.FiscalYear != 2026 || rt1.FiscalMonth != 8 {
		t.Errorf("expected fiscal period 2026-08, got %d-%d", rt1.FiscalYear, rt1.FiscalMonth)
	}
	if rt1.CurrentMonthCumulativeInvoicedMAD != 6000.00 {
		t.Errorf("expected 6000.00 MAD monthly gross, got %f", rt1.CurrentMonthCumulativeInvoicedMAD)
	}
	if rt1.CurrentMonthCumulativeRASMAD != 600.00 {
		t.Errorf("expected 600.00 MAD monthly RAS, got %f", rt1.CurrentMonthCumulativeRASMAD)
	}
	if rt1.DGIFilingDeadline != "2026-09-20" {
		t.Errorf("expected DGI filing deadline 2026-09-20, got %s", rt1.DGIFilingDeadline)
	}

	// Transaction 2: Second AWS transaction in the same month (August 2026) for 4,000 MAD
	txn2 := RawTransaction{
		TransactionID:   "txn_aws_02",
		RawDescription:  "CARTE 20/08 AWS CLOUD HOSTING",
		Amount:          4000.00,
		Currency:        "MAD",
		CashDirection:   CashOutflow,
		TransactionDate: time.Date(2026, 8, 20, 14, 0, 0, 0, time.UTC),
	}

	env2 := engine.EnrichTransaction(ctx, realmID, txn2)
	rt2 := env2.TaxAndCompliance.ForeignProviderRunningTotals
	if rt2 == nil {
		t.Fatalf("expected running totals to be populated for txn 2")
	}
	if rt2.CurrentMonthCumulativeInvoicedMAD != 10000.00 {
		t.Errorf("expected cumulative month gross 10000.00 MAD, got %f", rt2.CurrentMonthCumulativeInvoicedMAD)
	}
	if rt2.CurrentMonthCumulativeRASMAD != 1000.00 {
		t.Errorf("expected cumulative month RAS 1000.00 MAD, got %f", rt2.CurrentMonthCumulativeRASMAD)
	}
	if rt2.CurrentYearCumulativeInvoicedMAD != 10000.00 {
		t.Errorf("expected cumulative year gross 10000.00 MAD, got %f", rt2.CurrentYearCumulativeInvoicedMAD)
	}

	// Transaction 3: AWS transaction in September 2026 for 5,000 MAD
	txn3 := RawTransaction{
		TransactionID:   "txn_aws_03",
		RawDescription:  "CARTE 05/09 AMAZON WEB SERVICES",
		Amount:          5000.00,
		Currency:        "MAD",
		CashDirection:   CashOutflow,
		TransactionDate: time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC),
	}

	env3 := engine.EnrichTransaction(ctx, realmID, txn3)
	rt3 := env3.TaxAndCompliance.ForeignProviderRunningTotals
	if rt3 == nil {
		t.Fatalf("expected running totals for Sept 2026")
	}
	if rt3.FiscalMonth != 9 {
		t.Errorf("expected fiscal month 9, got %d", rt3.FiscalMonth)
	}
	if rt3.CurrentMonthCumulativeInvoicedMAD != 5000.00 {
		t.Errorf("expected September gross 5000.00 MAD, got %f", rt3.CurrentMonthCumulativeInvoicedMAD)
	}
	if rt3.CurrentMonthCumulativeRASMAD != 500.00 {
		t.Errorf("expected September RAS 500.00 MAD, got %f", rt3.CurrentMonthCumulativeRASMAD)
	}
	// Annual cumulative spend should be 10,000 (Aug) + 5,000 (Sept) = 15,000 MAD
	if rt3.CurrentYearCumulativeInvoicedMAD != 15000.00 {
		t.Errorf("expected annual gross 15000.00 MAD, got %f", rt3.CurrentYearCumulativeInvoicedMAD)
	}
	if rt3.CurrentYearCumulativeRASMAD != 1500.00 {
		t.Errorf("expected annual RAS 1500.00 MAD, got %f", rt3.CurrentYearCumulativeRASMAD)
	}
	if rt3.DGIFilingDeadline != "2026-10-20" {
		t.Errorf("expected September DGI filing deadline 2026-10-20, got %s", rt3.DGIFilingDeadline)
	}
}

func TestStatutoryICEExtraction(t *testing.T) {
	engine := NewMoroccanEnrichmentEngine()
	ctx := context.Background()

	rawTxn := RawTransaction{
		TransactionID:   "txn_ice_01",
		RawDescription:  "VIR INST 001523456000089 FACTURE EAU 0426",
		Amount:          1450.00,
		Currency:        "MAD",
		CashDirection:   CashOutflow,
		TransactionDate: time.Date(2026, 8, 14, 9, 30, 0, 0, time.UTC),
	}

	env := engine.EnrichTransaction(ctx, "realm_test", rawTxn)

	if env.Counterparty.Identifiers.ICE == nil || *env.Counterparty.Identifiers.ICE != "001523456000089" {
		t.Errorf("expected extracted ICE 001523456000089, got %v", env.Counterparty.Identifiers.ICE)
	}
	if env.Counterparty.NormalizedName != "Redal S.A. (Veolia Maroc)" {
		t.Errorf("expected Redal, got %s", env.Counterparty.NormalizedName)
	}
	if env.EnrichmentSource != "STATUTORY_ICE_REGISTRY" {
		t.Errorf("expected source STATUTORY_ICE_REGISTRY, got %s", env.EnrichmentSource)
	}
}

func TestMultiTierVATCalculation(t *testing.T) {
	tests := []struct {
		name          string
		ttcAmount     float64
		tvaRate       float64
		expectedNetHT float64
		expectedTVA   float64
	}{
		{
			name:          "7% Water VAT (Redal 1450.00 MAD)",
			ttcAmount:     1450.00,
			tvaRate:       0.07,
			expectedNetHT: 1355.14,
			expectedTVA:   94.86,
		},
		{
			name:          "10% Bank Commission (110.00 MAD)",
			ttcAmount:     110.00,
			tvaRate:       0.10,
			expectedNetHT: 100.00,
			expectedTVA:   10.00,
		},
		{
			name:          "14% Fuel VAT (Afriquia 570.00 MAD)",
			ttcAmount:     570.00,
			tvaRate:       0.14,
			expectedNetHT: 500.00,
			expectedTVA:   70.00,
		},
		{
			name:          "20% Standard Telecom (Maroc Telecom 1000.00 MAD)",
			ttcAmount:     1000.00,
			tvaRate:       0.20,
			expectedNetHT: 833.33,
			expectedTVA:   166.67,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			netHT, tva := CalculateMoroccanVAT(tc.ttcAmount, tc.tvaRate)
			if netHT != tc.expectedNetHT {
				t.Errorf("expected Net HT %f, got %f", tc.expectedNetHT, netHT)
			}
			if tva != tc.expectedTVA {
				t.Errorf("expected TVA %f, got %f", tc.expectedTVA, tva)
			}
			if round2Decimals(netHT+tva) != tc.ttcAmount {
				t.Errorf("Net HT + TVA (%f) != TTC (%f)", netHT+tva, tc.ttcAmount)
			}
		})
	}
}

func TestCashPaymentGuardrails(t *testing.T) {
	engine := NewMoroccanEnrichmentEngine()
	ctx := context.Background()
	sessionID := "session_cash_test"

	// 1. Initial Caisse Balance: 20,000 MAD
	engine.GetCashGuardrails().SetCaisseBalance(sessionID, 20000.00)

	// Outflow 1: 3,000 MAD to Supplier Atlas -> PASS
	txn1 := RawTransaction{
		TransactionID:   "txn_cash_01",
		RawDescription:  "BON DE CAISSE STE ATLAS FOURNITURES",
		Amount:          3000.00,
		Currency:        "MAD",
		CashDirection:   CashOutflow,
		TransactionDate: time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC),
		AccountCode:     "516100",
	}

	env1 := engine.EnrichTransaction(ctx, sessionID, txn1)
	if env1.TaxAndCompliance.StatutoryGuardrails.GuardrailStatus != GuardrailPass {
		t.Errorf("expected PASS for 3,000 MAD cash, got %v", env1.TaxAndCompliance.StatutoryGuardrails.GuardrailStatus)
	}

	// Outflow 2: Additional 2,500 MAD on the same day to Supplier Atlas -> Total 5,500 MAD > 5,000 MAD Daily Limit -> WARNING
	txn2 := RawTransaction{
		TransactionID:   "txn_cash_02",
		RawDescription:  "BON DE CAISSE STE ATLAS FOURNITURES",
		Amount:          2500.00,
		Currency:        "MAD",
		CashDirection:   CashOutflow,
		TransactionDate: time.Date(2026, 8, 14, 15, 0, 0, 0, time.UTC),
		AccountCode:     "516100",
	}

	env2 := engine.EnrichTransaction(ctx, sessionID, txn2)
	if env2.TaxAndCompliance.StatutoryGuardrails.GuardrailStatus != GuardrailWarningDailyLimitExceeded {
		t.Errorf("expected WARNING_DAILY_LIMIT_EXCEEDED, got %v", env2.TaxAndCompliance.StatutoryGuardrails.GuardrailStatus)
	}

	// Outflow 3: Attempting 25,000 MAD outflow when remaining caisse is ~14,500 MAD -> BLOCKED_CAISSE_CREDITRICE
	txn3 := RawTransaction{
		TransactionID:   "txn_cash_03",
		RawDescription:  "BON DE CAISSE ACHAT OUTILLAGE",
		Amount:          25000.00,
		Currency:        "MAD",
		CashDirection:   CashOutflow,
		TransactionDate: time.Date(2026, 8, 14, 16, 0, 0, 0, time.UTC),
		AccountCode:     "516100",
	}

	env3 := engine.EnrichTransaction(ctx, sessionID, txn3)
	if env3.TaxAndCompliance.StatutoryGuardrails.GuardrailStatus != GuardrailBlockedCaisseCreditrice {
		t.Errorf("expected BLOCKED_CAISSE_CREDITRICE, got %v", env3.TaxAndCompliance.StatutoryGuardrails.GuardrailStatus)
	}
}

func TestDocumentCorrelator(t *testing.T) {
	engine := NewMoroccanEnrichmentEngine()
	ctx := context.Background()
	sessionID := "session_doc_test"

	// Register a mock verified receipt in toro_core.documents
	docID := uuid.New()
	engine.GetDocCorrelator().RegisterDocument(&DocumentRecord{
		ID:            docID,
		SessionID:     sessionID,
		DocumentType:  "INVOICE",
		FileName:      "facture_iam_0826.pdf",
		InvoiceNumber: "FAC-IAM-2026-08",
		SupplierICE:   "000054238000045",
		SupplierName:  "Maroc Telecom",
		TotalTTC:      1200.00,
		TotalHT:       1000.00,
		TotalTVA:      200.00,
		InvoiceDate:   time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC),
		S3URL:         "https://s3.usetoro.com/docs/facture_iam_0826.pdf",
	})

	// Transaction matching the invoice within date and amount window
	txn := RawTransaction{
		TransactionID:   "txn_iam_doc",
		RawDescription:  "PRLV IAM FIBRE PRO 0826",
		Amount:          1200.00,
		Currency:        "MAD",
		CashDirection:   CashOutflow,
		TransactionDate: time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC),
	}

	env := engine.EnrichTransaction(ctx, sessionID, txn)

	if !env.DocumentEvidence.HasReceipt {
		t.Fatalf("expected document match to be true")
	}
	if env.DocumentEvidence.ReceiptStatus != ReceiptMatchedHighConfidence {
		t.Errorf("expected MATCHED_HIGH_CONFIDENCE, got %v", env.DocumentEvidence.ReceiptStatus)
	}
	if env.DocumentEvidence.ExtractedInvoiceNumber != "FAC-IAM-2026-08" {
		t.Errorf("expected invoice FAC-IAM-2026-08, got %s", env.DocumentEvidence.ExtractedInvoiceNumber)
	}
	if env.ConfidenceScore != 1.00 {
		t.Errorf("expected 1.00 confidence with document match, got %f", env.ConfidenceScore)
	}
}

func TestEnrichBatch(t *testing.T) {
	engine := NewMoroccanEnrichmentEngine()
	ctx := context.Background()

	req := EnrichmentRequest{
		RequestID: "req_batch_test",
		ClientID:  "client_toro_demo",
		Transactions: []RawTransaction{
			{
				TransactionID:   "txn_1",
				RawDescription:  "PRLV اتصالات المغرب FIBRE",
				Amount:          1000.00,
				Currency:        "MAD",
				CashDirection:   CashOutflow,
				TransactionDate: time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC),
			},
			{
				TransactionID:   "txn_2",
				RawDescription:  "CARTE AWS EMEA SARL",
				Amount:          5000.00,
				Currency:        "MAD",
				CashDirection:   CashOutflow,
				TransactionDate: time.Date(2026, 8, 14, 9, 30, 0, 0, time.UTC),
			},
			{
				TransactionID:   "txn_3",
				RawDescription:  "VIR INST 001523456000089 REDAL SA",
				Amount:          1450.00,
				Currency:        "MAD",
				CashDirection:   CashOutflow,
				TransactionDate: time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC),
			},
		},
	}

	resp := engine.EnrichBatch(ctx, req)

	if len(resp.EnrichedTransactions) != 3 {
		t.Fatalf("expected 3 enriched transactions, got %d", len(resp.EnrichedTransactions))
	}
	if resp.RequestID != "req_batch_test" {
		t.Errorf("expected request id req_batch_test, got %s", resp.RequestID)
	}
}
func BenchmarkEnrichTransaction(b *testing.B) {
	engine := NewMoroccanEnrichmentEngine()
	ctx := context.Background()

	txn := RawTransaction{
		TransactionID:   "bench_txn",
		RawDescription:  "VIR INST 001523456000089 REDAL SA FACTURE EAU 0426",
		Amount:          1450.00,
		Currency:        "MAD",
		CashDirection:   CashOutflow,
		TransactionDate: time.Date(2026, 8, 14, 9, 30, 0, 0, time.UTC),
	}

	for b.Loop() {
		_ = engine.EnrichTransaction(ctx, "session_bench", txn)
	}
}
