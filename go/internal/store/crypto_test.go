package store

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func TestNewEncryptor_ValidKey(t *testing.T) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if enc == nil {
		t.Fatal("Expected non-nil encryptor")
	}
}

func TestNewEncryptor_InvalidKey(t *testing.T) {
	tests := []struct {
		name    string
		keySize int
	}{
		{"16 bytes", 16},
		{"24 bytes", 24},
		{"0 bytes", 0},
		{"64 bytes", 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := make([]byte, tt.keySize)
			_, err := NewEncryptor(key)
			if err == nil {
				t.Error("Expected error for invalid key size")
			}
			if !strings.Contains(err.Error(), "32 bytes") {
				t.Errorf("Expected error about key size, got: %v", err)
			}
		})
	}
}

func TestEncryptor_EncryptDecrypt(t *testing.T) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}

	tests := []struct {
		name      string
		plaintext string
	}{
		{"simple string", "hello world"},
		{"webhook secret", "whsec_1234567890abcdef"},
		{"special chars", "special!@#$%^&*()_+-=[]{}|;:',.<>?"},
		{"unicode", "你好世界 🌍"},
		{"long string", strings.Repeat("a", 1000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ciphertext, err := enc.Encrypt(tt.plaintext)
			if err != nil {
				t.Fatalf("Encrypt failed: %v", err)
			}

			// Verify it's base64 encoded
			_, err = base64.StdEncoding.DecodeString(ciphertext)
			if err != nil {
				t.Errorf("Ciphertext is not valid base64: %v", err)
			}

			// Decrypt and verify
			decrypted, err := enc.Decrypt(ciphertext)
			if err != nil {
				t.Fatalf("Decrypt failed: %v", err)
			}

			if decrypted != tt.plaintext {
				t.Errorf("Decrypted text doesn't match. Got %q, want %q", decrypted, tt.plaintext)
			}
		})
	}
}

func TestEncryptor_DifferentNonces(t *testing.T) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}

	plaintext := "same plaintext"

	// Encrypt the same plaintext multiple times
	ciphertexts := make([]string, 10)
	for i := 0; i < 10; i++ {
		ct, err := enc.Encrypt(plaintext)
		if err != nil {
			t.Fatalf("Encrypt failed: %v", err)
		}
		ciphertexts[i] = ct
	}

	// Verify all ciphertexts are different (different nonces)
	for i := 0; i < len(ciphertexts); i++ {
		for j := i + 1; j < len(ciphertexts); j++ {
			if ciphertexts[i] == ciphertexts[j] {
				t.Errorf("Same ciphertext generated twice: %q", ciphertexts[i])
			}
		}
	}

	// Verify all decrypt to the same plaintext
	for i, ct := range ciphertexts {
		pt, err := enc.Decrypt(ct)
		if err != nil {
			t.Fatalf("Decrypt failed for ciphertext %d: %v", i, err)
		}
		if pt != plaintext {
			t.Errorf("Decrypted text doesn't match for ciphertext %d. Got %q, want %q", i, pt, plaintext)
		}
	}
}

func TestEncryptor_InvalidCiphertext(t *testing.T) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}

	tests := []struct {
		name       string
		ciphertext string
	}{
		{"empty string", ""},
		{"invalid base64", "not-valid-base64!"},
		{"too short", base64.StdEncoding.EncodeToString([]byte("short"))},
		{"random data", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 100)))},
		{"corrupted data", func() string {
			ct, _ := enc.Encrypt("test")
			decoded, _ := base64.StdEncoding.DecodeString(ct)
			// Corrupt the last byte (authentication tag)
			decoded[len(decoded)-1] ^= 0xFF
			return base64.StdEncoding.EncodeToString(decoded)
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := enc.Decrypt(tt.ciphertext)
			if err == nil {
				t.Error("Expected error for invalid ciphertext")
			}
		})
	}
}

func TestEncryptor_WrongKey(t *testing.T) {
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

	enc1, err := NewEncryptor(key1)
	if err != nil {
		t.Fatalf("Failed to create encryptor1: %v", err)
	}

	enc2, err := NewEncryptor(key2)
	if err != nil {
		t.Fatalf("Failed to create encryptor2: %v", err)
	}

	plaintext := "secret data"
	ciphertext, err := enc1.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// Try to decrypt with wrong key
	_, err = enc2.Decrypt(ciphertext)
	if err == nil {
		t.Error("Expected error when decrypting with wrong key")
	}
}

func TestEncryptor_EmptyPlaintext(t *testing.T) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("Failed to create encryptor: %v", err)
	}

	_, err = enc.Encrypt("")
	if err == nil {
		t.Error("Expected error for empty plaintext")
	}
}
