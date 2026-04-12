package identity

import (
	"crypto/ed25519"
	"crypto/sha256"
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


// KeyPairFromSeed derives a deterministic Ed25519 key pair from an arbitrary
// string seed (e.g. an agent's ActivityType). The seed is SHA-256 hashed to
// produce a stable 32-byte Ed25519 seed, so the same input always yields the
// same DID — surviving restarts without accumulating orphaned NATS consumers.
func KeyPairFromSeed(seed string) (*KeyPair, error) {
	if seed == "" {
		return nil, fmt.Errorf("seed must not be empty")
	}
	hashed := sha256.Sum256([]byte(seed))
	priv := ed25519.NewKeyFromSeed(hashed[:])
	return &KeyPair{Public: priv.Public().(ed25519.PublicKey), Private: priv}, nil
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
