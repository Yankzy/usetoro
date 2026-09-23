package workers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type BankCategorizeGoldenVector struct {
	Request                BankCategorizeRequest `json:"request"`
	ExpectedCanonicalBytes string                `json:"expected_canonical_bytes"`
	ExpectedSha256Digest   string                `json:"expected_sha256_digest"`
}

func loadBankCategorizeGoldenVector(t *testing.T) BankCategorizeGoldenVector {
	t.Helper()
	// Find the fixture path relative to this file
	// Assuming running from go/internal/workers, path to ledger is ../../../ledger/tests/fixtures/bank_categorize_golden_vector.json
	path := filepath.Join("..", "..", "..", "ledger", "tests", "fixtures", "bank_categorize_golden_vector.json")
	
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read golden vector fixture: %v", err)
	}

	var gv BankCategorizeGoldenVector
	if err := json.Unmarshal(b, &gv); err != nil {
		t.Fatalf("Failed to unmarshal golden vector fixture: %v", err)
	}
	return gv
}

func TestBankCategorizeRequestValidation(t *testing.T) {
	gv := loadBankCategorizeGoldenVector(t)
	req := gv.Request

	// Valid request
	if err := req.Validate(); err != nil {
		t.Fatalf("Expected valid request, got error: %v", err)
	}

	// Invalid schema
	invalidReq := req
	invalidReq.SchemaVersion = "bookkeeping.ase.bank_categorize.v999"
	if err := invalidReq.Validate(); err == nil {
		t.Error("Expected error for invalid schema version")
	}

	// Duplicate items
	dupReq := req
	dupReq.BankItems = append(dupReq.BankItems, dupReq.BankItems[0])
	if err := dupReq.Validate(); err == nil {
		t.Error("Expected error for duplicate items")
	}

	// Invalid direction
	invalidItemReq := req
	invalidItemReq.BankItems[0].Direction = "INVALID_DIR"
	if err := invalidItemReq.Validate(); err == nil {
		t.Error("Expected error for invalid direction")
	}

	// Residual > original
	invalidAmtReq := req
	invalidAmtReq.BankItems[0].ResidualAmountUnits = 999999999
	invalidAmtReq.BankItems[0].OriginalAmountUnits = 1
	if err := invalidAmtReq.Validate(); err == nil {
		t.Error("Expected error for residual > original")
	}
}

func TestBankCategorizeCanonicalHashing(t *testing.T) {
	gv := loadBankCategorizeGoldenVector(t)
	req := gv.Request

	canonicalBytes, err := ComputeBankRequestPayloadCanonicalBytes(req)
	if err != nil {
		t.Fatalf("ComputeBankRequestPayloadCanonicalBytes failed: %v", err)
	}

	if string(canonicalBytes) != gv.ExpectedCanonicalBytes {
		t.Errorf("Canonical bytes mismatch.\nExpected: %s\nGot:      %s", gv.ExpectedCanonicalBytes, string(canonicalBytes))
	}

	digest, err := ComputeBankRequestPayloadDigest(req)
	if err != nil {
		t.Fatalf("ComputeBankRequestPayloadDigest failed: %v", err)
	}

	if digest != gv.ExpectedSha256Digest {
		t.Errorf("Digest mismatch.\nExpected: %s\nGot:      %s", gv.ExpectedSha256Digest, digest)
	}
}

func TestBankCategorizeOutcomeValidation(t *testing.T) {
	accCode := "6064"
	reason := "Needs clarification"
	
	validClassified := BankCategorizeOutcome{
		BankItemID: "staged:123",
		Status:     "CLASSIFIED",
		AccountCode: &accCode,
	}
	if err := validClassified.Validate(); err != nil {
		t.Errorf("Expected valid classified, got: %v", err)
	}

	invalidClassified := validClassified
	invalidClassified.AccountCode = nil
	if err := invalidClassified.Validate(); err == nil {
		t.Error("Expected error for missing account_code on CLASSIFIED")
	}

	invalidClassified2 := validClassified
	invalidClassified2.HoldReason = &reason
	if err := invalidClassified2.Validate(); err == nil {
		t.Error("Expected error for hold_reason present on CLASSIFIED")
	}

	validHold := BankCategorizeOutcome{
		BankItemID: "staged:456",
		Status:     "HOLD",
		HoldReason: &reason,
	}
	if err := validHold.Validate(); err != nil {
		t.Errorf("Expected valid hold, got: %v", err)
	}

	invalidHold := validHold
	invalidHold.HoldReason = nil
	if err := invalidHold.Validate(); err == nil {
		t.Error("Expected error for missing hold_reason on HOLD")
	}

	invalidHold2 := validHold
	invalidHold2.AccountCode = &accCode
	if err := invalidHold2.Validate(); err == nil {
		t.Error("Expected error for account_code present on HOLD")
	}
}
