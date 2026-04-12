package identity

import (
	"crypto/ed25519"
	"encoding/hex"
	"testing"
)

func TestSignAndVerify(t *testing.T) {
	kp, err := KeyPairFromSeed("agents.test.sign_verify")
	if err != nil {
		t.Fatalf("KeyPairFromSeed failed: %v", err)
	}

	data := []byte("hello world")
	sigHex := kp.Sign(data)

	// Test valid signature
	valid, err := Verify(hex.EncodeToString(kp.Public), data, sigHex)
	if err != nil {
		t.Errorf("Verify failed for valid signature: %v", err)
	}
	if !valid {
		t.Error("Verify returned false for valid signature")
	}

	// Test invalid signature
	invalidSigBytes := make([]byte, hex.DecodedLen(len(sigHex)))
	_, _ = hex.Decode(invalidSigBytes, []byte(sigHex))
	invalidSigBytes[0] ^= 0xff // Flip a bit
	invalidSigHex := hex.EncodeToString(invalidSigBytes)

	valid, err = Verify(hex.EncodeToString(kp.Public), data, invalidSigHex)
	if err != nil {
		t.Errorf("Verify failed for invalid signature: %v", err)
	}
	if valid {
		t.Error("Verify returned true for invalid signature")
	}

	// Test invalid data
	valid, err = Verify(hex.EncodeToString(kp.Public), []byte("wrong data"), sigHex)
	if err != nil {
		t.Errorf("Verify failed for invalid data: %v", err)
	}
	if valid {
		t.Error("Verify returned true for invalid data")
	}
}

func TestVerifyErrors(t *testing.T) {
	// Derive a valid keypair for reference
	kp, _ := KeyPairFromSeed("agents.test.verify_errors")
	data := []byte("test")
	sig := kp.Sign(data)

	tests := []struct {
		name      string
		pubKeyHex string
		sigHex    string
		wantErr   bool
	}{
		{
			name:      "Invalid Public Key Hex",
			pubKeyHex: "not hex",
			sigHex:    sig,
			wantErr:   true,
		},
		{
			name:      "Invalid Signature Hex",
			pubKeyHex: hex.EncodeToString(kp.Public),
			sigHex:    "not hex",
			wantErr:   true,
		},
		{
			name:      "Wrong Public Key Size",
			pubKeyHex: hex.EncodeToString(kp.Public)[:10], // Truncated
			sigHex:    sig,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Verify(tt.pubKeyHex, data, tt.sigHex)
			if (err != nil) != tt.wantErr {
				t.Errorf("Verify() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestKeyPairFromSeed(t *testing.T) {
	t.Run("Deterministic: same seed always yields same DID", func(t *testing.T) {
		kp1, err1 := KeyPairFromSeed("agents.accounting.map_csv")
		kp2, err2 := KeyPairFromSeed("agents.accounting.map_csv")
		if err1 != nil || err2 != nil {
			t.Fatalf("KeyPairFromSeed failed: %v / %v", err1, err2)
		}
		if hex.EncodeToString(kp1.Public) != hex.EncodeToString(kp2.Public) {
			t.Error("Same seed produced different public keys")
		}
	})

	t.Run("Unique: different seeds produce different keys", func(t *testing.T) {
		kp1, _ := KeyPairFromSeed("agents.accounting.map_csv")
		kp2, _ := KeyPairFromSeed("agents.accounting.reconcile_expense")
		if hex.EncodeToString(kp1.Public) == hex.EncodeToString(kp2.Public) {
			t.Error("Different seeds produced identical public keys")
		}
	})

	t.Run("Error on empty seed", func(t *testing.T) {
		_, err := KeyPairFromSeed("")
		if err == nil {
			t.Error("Expected error for empty seed, got nil")
		}
	})

	t.Run("Valid key sizes", func(t *testing.T) {
		kp, err := KeyPairFromSeed("agents.payments.stripe_receipt_processor")
		if err != nil {
			t.Fatalf("KeyPairFromSeed failed: %v", err)
		}
		if len(kp.Public) != ed25519.PublicKeySize {
			t.Errorf("Expected public key size %d, got %d", ed25519.PublicKeySize, len(kp.Public))
		}
		if len(kp.Private) != ed25519.PrivateKeySize {
			t.Errorf("Expected private key size %d, got %d", ed25519.PrivateKeySize, len(kp.Private))
		}
	})

	t.Run("Sign and verify with seeded keypair", func(t *testing.T) {
		kp, _ := KeyPairFromSeed("agents.accounting.approval")
		data := []byte("proof payload")
		sig := kp.Sign(data)
		valid, err := Verify(hex.EncodeToString(kp.Public), data, sig)
		if err != nil {
			t.Fatalf("Verify failed: %v", err)
		}
		if !valid {
			t.Error("Verify returned false for valid seeded signature")
		}
	})
}
