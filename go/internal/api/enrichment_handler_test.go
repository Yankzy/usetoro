package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/internal/services/enrichment"
)

func TestHandleMoroccanEnrichmentAPI(t *testing.T) {
	h := &Handler{}

	reqBody := enrichment.EnrichmentRequest{
		RequestID: "req_api_test_01",
		ClientID:  "tenant_rabat_tech",
		Transactions: []enrichment.RawTransaction{
			{
				TransactionID:   "txn_iam_01",
				RawDescription:  "PRLV اتصالات المغرب FIBRE PRO 0826",
				Amount:          1000.00,
				Currency:        "MAD",
				CashDirection:   enrichment.CashOutflow,
				TransactionDate: time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC),
			},
			{
				TransactionID:   "txn_aws_01",
				RawDescription:  "CARTE 14/08 AWS EMEA SARL LUXEMBOURG US REQ 89402",
				Amount:          6000.00,
				Currency:        "MAD",
				CashDirection:   enrichment.CashOutflow,
				TransactionDate: time.Date(2026, 8, 14, 9, 30, 0, 0, time.UTC),
			},
		},
	}

	payloadBytes, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/enrichment/morocco", bytes.NewReader(payloadBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleMoroccanEnrichment(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp enrichment.EnrichmentResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.EnrichedTransactions) != 2 {
		t.Fatalf("expected 2 enriched transactions, got %d", len(resp.EnrichedTransactions))
	}

	// Verify IAM transaction
	iamTxn := resp.EnrichedTransactions[0]
	if iamTxn.Counterparty.NormalizedName != "Maroc Telecom (Itissalat Al-Maghrib S.A.)" {
		t.Errorf("expected Maroc Telecom, got %s", iamTxn.Counterparty.NormalizedName)
	}
	if iamTxn.PCGMAccounting.SuggestedAccount != "614510" {
		t.Errorf("expected account 614510, got %s", iamTxn.PCGMAccounting.SuggestedAccount)
	}
	if iamTxn.PCGMAccounting.DefaultTVARate != 0.20 {
		t.Errorf("expected 20%% TVA, got %f", iamTxn.PCGMAccounting.DefaultTVARate)
	}

	// Verify AWS transaction
	awsTxn := resp.EnrichedTransactions[1]
	if !awsTxn.Counterparty.IsForeignService {
		t.Errorf("expected foreign service true for AWS")
	}
	if !awsTxn.TaxAndCompliance.RASApplicable {
		t.Errorf("expected RAS applicable true for AWS")
	}
	if awsTxn.TaxAndCompliance.RASAmountMAD != 600.00 {
		t.Errorf("expected 600.00 MAD RAS, got %f", awsTxn.TaxAndCompliance.RASAmountMAD)
	}
	if awsTxn.TaxAndCompliance.ForeignProviderRunningTotals == nil {
		t.Fatalf("expected running totals to be populated")
	}
	if awsTxn.TaxAndCompliance.ForeignProviderRunningTotals.CurrentMonthCumulativeInvoicedMAD < 6000.00 {
		t.Errorf("expected monthly cumulative gross >= 6000.00 MAD, got %f", awsTxn.TaxAndCompliance.ForeignProviderRunningTotals.CurrentMonthCumulativeInvoicedMAD)
	}
}

func TestHandleGetForeignProviderRunningTotalsAPI(t *testing.T) {
	h := &Handler{}

	// Query with missing params -> 400
	reqBad := httptest.NewRequest(http.MethodGet, "/api/v1/enrichment/morocco/foreign-totals", nil)
	recBad := httptest.NewRecorder()
	h.HandleGetForeignProviderRunningTotals(recBad, reqBad)
	if recBad.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for missing params, got %d", recBad.Code)
	}

	// Query with valid params
	awsProviderID := "00000000-0000-0000-0005-000000000001"
	reqGood := httptest.NewRequest(http.MethodGet, "/api/v1/enrichment/morocco/foreign-totals?realm_id=tenant_rabat_tech&provider_id="+awsProviderID, nil)
	recGood := httptest.NewRecorder()
	h.HandleGetForeignProviderRunningTotals(recGood, reqGood)

	if recGood.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d: %s", recGood.Code, recGood.Body.String())
	}

	var totals enrichment.ForeignProviderRunningTotals
	if err := json.NewDecoder(recGood.Body).Decode(&totals); err != nil {
		t.Fatalf("failed to decode totals response: %v", err)
	}

	if totals.FiscalYear != 2026 {
		t.Errorf("expected fiscal year 2026, got %d", totals.FiscalYear)
	}
}
