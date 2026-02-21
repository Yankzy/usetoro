package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"testing"
)

func TestCreateDID(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	did := CreateDID(pub)
	expectedPrefix := "did:toro:"
	if len(did) <= len(expectedPrefix) {
		t.Errorf("DID is too short: %s", did)
	}
	if did[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("DID prefix mismatch. Got %s, want %s...", did, expectedPrefix)
	}

	pubHex := did[len(expectedPrefix):]
	if pubHex != hex.EncodeToString(pub) {
		t.Errorf("DID public key part mismatch. Got %s, want %s", pubHex, hex.EncodeToString(pub))
	}
}

func TestPubKeyFromDID(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	did := CreateDID(pub)
	extractedPubHex, err := PubKeyFromDID(did)
	if err != nil {
		t.Fatalf("PubKeyFromDID failed: %v", err)
	}

	if extractedPubHex != hex.EncodeToString(pub) {
		t.Errorf("Extracted public key mismatch. Got %s, want %s", extractedPubHex, hex.EncodeToString(pub))
	}

	// Test invalid DID format
	_, err = PubKeyFromDID("invalid:did:format")
	if err == nil {
		t.Error("PubKeyFromDID expected error for invalid DID format, got nil")
	}
}
