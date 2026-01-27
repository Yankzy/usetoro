package api

import (
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v76/webhook"
)

func TestStripeSignatureLogic(t *testing.T) {
	secret := "whsec_test_secret"
	payload := []byte(`{"id": "evt_123", "object": "event", "api_version": "2023-10-16"}`)
	now := time.Now()

	// 1. Compute Signature using Library
	sigIdx := webhook.ComputeSignature(now, payload, secret)
	sig := hex.EncodeToString(sigIdx)
	header := fmt.Sprintf("t=%d,v1=%s", now.Unix(), sig)

	// 2. Verify using Library
	event, err := webhook.ConstructEvent(payload, header, secret)
	if err != nil {
		t.Fatalf("ConstructEvent failed with library computed signature: %v\nHeader: %s", err, header)
	}
	if event.ID != "evt_123" {
		t.Errorf("Expected event ID evt_123, got %s", event.ID)
	}
}
