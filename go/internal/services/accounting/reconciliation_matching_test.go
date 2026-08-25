package accounting

import "testing"

func TestInferTreasuryItemTypeAndDefaultPolicies(t *testing.T) {
	cases := map[string]string{
		"VIR COMPTE A COMPTE":   "INTERNAL_TRANSFER",
		"REMISE TPE":            "CARD_TPE",
		"CHEQUE 00291":          "CHEQUE_BILL",
		"REGLEMENT FOURNISSEUR": "GENERIC",
	}
	for input, expected := range cases {
		if got := inferTreasuryItemType(input); got != expected {
			t.Fatalf("%q: got %s, want %s", input, got, expected)
		}
	}
}

func TestStableMatchKeyIsOrderSensitiveAndStable(t *testing.T) {
	a := stableMatchKey("bank", "book", "policy")
	b := stableMatchKey("bank", "book", "policy")
	c := stableMatchKey("book", "bank", "policy")
	if a != b || a == c || len(a) != 64 {
		t.Fatalf("unexpected keys: %q %q %q", a, b, c)
	}
}
