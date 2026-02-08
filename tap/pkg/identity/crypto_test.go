package identity

import (
	"crypto/ed25519"
	"encoding/hex"
	"testing"
)

func TestGenerateKeyPair(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}
	if kp == nil {
		t.Fatal("GenerateKeyPair returned nil KeyPair")
	}
	if len(kp.Public) != ed25519.PublicKeySize {
		t.Errorf("Expected public key size %d, got %d", ed25519.PublicKeySize, len(kp.Public))
	}
	if len(kp.Private) != ed25519.PrivateKeySize {
		t.Errorf("Expected private key size %d, got %d", ed25519.PrivateKeySize, len(kp.Private))
	}
}

func TestSignAndVerify(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
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
	// Generate a valid keypair for reference
	kp, _ := GenerateKeyPair()
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
