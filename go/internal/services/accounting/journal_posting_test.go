package accounting

import (
	"testing"
	"time"
)

func TestCanonicalBankPieceReferenceIsDeterministic(t *testing.T) {
	date := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
	got := canonicalBankPieceReference(date, "12345678-90ab-cdef-1234-567890abcdef")
	if got != "BR-20260703-1234567890AB" {
		t.Fatalf("unexpected piece reference %q", got)
	}
}

func TestPostingNumericPreservesFourDecimalPrecision(t *testing.T) {
	numeric, err := postingNumeric("123.4567")
	if err != nil {
		t.Fatal(err)
	}
	got, err := numericText(numeric)
	if err != nil || got != "123.4567" {
		t.Fatalf("expected exact numeric, got %q (%v)", got, err)
	}
}
