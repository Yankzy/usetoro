package crypto

import (
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	key := "12345678901234561234567890123456" // 32 bytes
	text := "my_secret_token_123"

	cipher, err := Encrypt(key, text)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	plain, err := Decrypt(key, cipher)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if plain != text {
		t.Errorf("Expected %s, got %s", text, plain)
	}
}
