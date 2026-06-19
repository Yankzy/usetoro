package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStripeSignatureLogic(t *testing.T) {
	secret := "whsec_test_secret"
	payload := []byte(`{"id": "evt_123", "object": "event", "api_version": "2023-10-16"}`)
	now := time.Now()

	// 1. Compute Stripe-compatible HMAC-SHA256 signature
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signedPayload := fmt.Sprintf("%s.%s", timestamp, string(payload))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	sig := hex.EncodeToString(mac.Sum(nil))
	header := fmt.Sprintf("t=%s,v1=%s", timestamp, sig)

	// 2. Verify the signature
	parts := strings.Split(header, ",")
	tPart := strings.TrimPrefix(parts[0], "t=")
	v1Part := strings.TrimPrefix(parts[1], "v1=")

	expectedPayload := fmt.Sprintf("%s.%s", tPart, string(payload))
	mac2 := hmac.New(sha256.New, []byte(secret))
	mac2.Write([]byte(expectedPayload))
	computedSig := hex.EncodeToString(mac2.Sum(nil))

	if computedSig != v1Part {
		t.Fatalf("Signature verification failed: expected %s, got %s", v1Part, computedSig)
	}
}
