package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

var (
	// ErrInvalidKey is returned when the encryption key is not 32 bytes.
	ErrInvalidKey = errors.New("encryption key must be exactly 32 bytes")
	// ErrInvalidCiphertext is returned when the ciphertext is malformed or corrupted.
	ErrInvalidCiphertext = errors.New("invalid or corrupted ciphertext")
)

// Encryptor provides AES-256-GCM encryption and decryption for sensitive data.
// It uses a 256-bit key and generates a unique 12-byte nonce for each encryption.
type Encryptor struct {
	aead cipher.AEAD
}

// NewEncryptor creates a new Encryptor with the provided 32-byte key.
// The key should be cryptographically random and stored securely.
func NewEncryptor(key []byte) (*Encryptor, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: got %d bytes", ErrInvalidKey, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	return &Encryptor{aead: aead}, nil
}

// Encrypt encrypts plaintext using AES-256-GCM and returns base64-encoded ciphertext.
// The format is: base64(nonce + ciphertext + tag)
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", errors.New("plaintext cannot be empty")
	}

	// Generate a unique nonce for this encryption
	nonce := make([]byte, e.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt and append the authentication tag
	ciphertext := e.aead.Seal(nonce, nonce, []byte(plaintext), nil)

	// Encode to base64 for storage
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts base64-encoded ciphertext back to plaintext.
// Returns an error if the ciphertext is invalid or has been tampered with.
func (e *Encryptor) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", errors.New("ciphertext cannot be empty")
	}

	// Decode from base64
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("%w: invalid base64 encoding", ErrInvalidCiphertext)
	}

	nonceSize := e.aead.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("%w: ciphertext too short", ErrInvalidCiphertext)
	}

	// Extract nonce and encrypted data
	nonce, encryptedData := data[:nonceSize], data[nonceSize:]

	// Decrypt and verify authentication tag
	plaintext, err := e.aead.Open(nil, nonce, encryptedData, nil)
	if err != nil {
		return "", fmt.Errorf("%w: decryption failed", ErrInvalidCiphertext)
	}

	return string(plaintext), nil
}
