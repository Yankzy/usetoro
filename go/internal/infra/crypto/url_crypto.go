package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
)

// EncryptTrackingData encrypts a target URL and a prospect ID into a URL-safe Base64 string.
// The resulting string can be safely used in URL paths.
func EncryptTrackingData(targetURL string, prospectID uuid.UUID, keyStr string) (string, error) {
	key := []byte(keyStr)
	if len(key) != 32 {
		return "", errors.New("AES key must be 32 bytes")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	// Payload format: target_url|prospect_id
	payload := fmt.Sprintf("%s|%s", targetURL, prospectID.String())
	plaintext := []byte(payload)

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

// DecryptTrackingData decrypts a URL-safe Base64 string back into a target URL and a prospect ID.
func DecryptTrackingData(cryptoText string, keyStr string) (string, uuid.UUID, error) {
	key := []byte(keyStr)
	if len(key) != 32 {
		return "", uuid.Nil, errors.New("AES key must be 32 bytes")
	}

	ciphertext, err := base64.RawURLEncoding.DecodeString(cryptoText)
	if err != nil {
		return "", uuid.Nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", uuid.Nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", uuid.Nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", uuid.Nil, errors.New("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", uuid.Nil, err
	}

	// Payload format: target_url|prospect_id
	parts := strings.SplitN(string(plaintext), "|", 2)
	if len(parts) != 2 {
		return "", uuid.Nil, errors.New("invalid payload format")
	}

	targetURL := parts[0]
	prospectID, err := uuid.Parse(parts[1])
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("invalid prospect id: %w", err)
	}

	return targetURL, prospectID, nil
}
