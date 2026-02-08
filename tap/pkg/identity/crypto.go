package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

var ErrInvalidSignature = errors.New("invalid signature")

// KeyPair holds the identity keys
type KeyPair struct {
	Public  ed25519.PublicKey
	Private ed25519.PrivateKey
}

// GenerateKeyPair creates a fresh identity
func GenerateKeyPair() (*KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &KeyPair{Public: pub, Private: priv}, nil
}

// Sign signs a raw byte slice (usually the canonicalized Envelope)
func (k *KeyPair) Sign(data []byte) string {
	signature := ed25519.Sign(k.Private, data)
	return hex.EncodeToString(signature)
}

// Verify checks a signature against a public key
// pubKeyHex should be the raw hex string of the public key
func Verify(pubKeyHex string, data []byte, sigHex string) (bool, error) {
	pubBytes, err := hex.DecodeString(pubKeyHex)
	if err != nil {
		return false, fmt.Errorf("invalid public key hex: %w", err)
	}

	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return false, fmt.Errorf("invalid signature hex: %w", err)
	}

	if len(pubBytes) != ed25519.PublicKeySize {
		return false, fmt.Errorf("invalid public key size")
	}

	return ed25519.Verify(pubBytes, data, sigBytes), nil
}
