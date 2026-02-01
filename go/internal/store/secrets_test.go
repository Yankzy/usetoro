package store

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
)

// TestSecretRoundTrip tests that a secret can be encrypted, stored (simulated), and decrypted
func TestSecretRoundTrip(t *testing.T) {
	// Generate encryption key
	key := make([]byte, 32)
	_, err := rand.Read(key)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	// Create encryptor
	encryptor, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}

	tests := []struct {
		name            string
		plaintextSecret string
	}{
		{"webhook secret", "whsec_test_secret_123"},
		{"long secret", "whsec_" + string(make([]byte, 100))},
		{"special chars", "whsec_!@#$%^&*()_+-=[]{}|;:',.<>?"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encrypt (simulates what StoreWebhookSecret does)
			encrypted, err := encryptor.Encrypt(tt.plaintextSecret)
			if err != nil {
				t.Fatalf("Failed to encrypt: %v", err)
			}

			// Verify it's base64-encoded
			_, err = base64.StdEncoding.DecodeString(encrypted)
			if err != nil {
				t.Errorf("Encrypted secret is not valid base64: %v", err)
			}

			// Verify it's not plaintext
			if encrypted == tt.plaintextSecret {
				t.Error("Secret was not encrypted!")
			}

			// Decrypt (simulates what GetWebhookSecret does)
			decrypted, err := encryptor.Decrypt(encrypted)
			if err != nil {
				t.Fatalf("Failed to decrypt: %v", err)
			}

			// Verify decrypted matches original
			if decrypted != tt.plaintextSecret {
				t.Errorf("Decrypted secret doesn't match. Got %q, want %q", decrypted, tt.plaintextSecret)
			}
		})
	}
}

// TestEncryptionKeyMismatch verifies that secrets encrypted with one key cannot be decrypted with another
func TestEncryptionKeyMismatch(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	_, err := rand.Read(key1)
	if err != nil {
		t.Fatalf("Failed to generate key1: %v", err)
	}
	_, err = rand.Read(key2)
	if err != nil {
		t.Fatalf("Failed to generate key2: %v", err)
	}

	encryptor1, err := NewEncryptor(key1)
	if err != nil {
		t.Fatalf("Failed to create encryptor1: %v", err)
	}

	encryptor2, err := NewEncryptor(key2)
	if err != nil {
		t.Fatalf("Failed to create encryptor2: %v", err)
	}

	plaintextSecret := "whsec_secret_data"

	// Encrypt with key1
	encrypted, err := encryptor1.Encrypt(plaintextSecret)
	if err != nil {
		t.Fatalf("Failed to encrypt: %v", err)
	}

	// Try to decrypt with key2 (should fail)
	_, err = encryptor2.Decrypt(encrypted)
	if err == nil {
		t.Error("Expected error when decrypting with wrong key, but got nil")
	}
}

// TestDatabaseStorageFormat verifies the format of encrypted secrets for database storage
func TestDatabaseStorageFormat(t *testing.T) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	encryptor, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}

	plaintextSecret := "whsec_test_format"

	// Encrypt multiple times to test nonce uniqueness
	encrypted1, err := encryptor.Encrypt(plaintextSecret)
	if err != nil {
		t.Fatalf("Failed to encrypt 1: %v", err)
	}

	encrypted2, err := encryptor.Encrypt(plaintextSecret)
	if err != nil {
		t.Fatalf("Failed to encrypt 2: %v", err)
	}

	// Verify both are valid base64
	decoded1, err := base64.StdEncoding.DecodeString(encrypted1)
	if err != nil {
		t.Errorf("encrypted1 is not valid base64: %v", err)
	}

	decoded2, err := base64.StdEncoding.DecodeString(encrypted2)
	if err != nil {
		t.Errorf("encrypted2 is not valid base64: %v", err)
	}

	// Verify they are different (due to different nonces)
	if encrypted1 == encrypted2 {
		t.Error("Same plaintext produced same ciphertext (nonce reuse!)")
	}

	// Verify minimum length (nonce + ciphertext + tag)
	// GCM nonce is 12 bytes, tag is 16 bytes, minimum ciphertext is 1 byte
	minLength := 12 + 1 + 16 // 29 bytes
	if len(decoded1) < minLength {
		t.Errorf("Encrypted data too short: got %d bytes, want at least %d", len(decoded1), minLength)
	}
	if len(decoded2) < minLength {
		t.Errorf("Encrypted data too short: got %d bytes, want at least %d", len(decoded2), minLength)
	}

	// Verify both decrypt correctly
	decrypted1, err := encryptor.Decrypt(encrypted1)
	if err != nil {
		t.Fatalf("Failed to decrypt 1: %v", err)
	}
	if decrypted1 != plaintextSecret {
		t.Errorf("Decrypted 1 doesn't match. Got %q, want %q", decrypted1, plaintextSecret)
	}

	decrypted2, err := encryptor.Decrypt(encrypted2)
	if err != nil {
		t.Fatalf("Failed to decrypt 2: %v", err)
	}
	if decrypted2 != plaintextSecret {
		t.Errorf("Decrypted 2 doesn't match. Got %q, want %q", decrypted2, plaintextSecret)
	}
}
