package identity

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"strings"
)

const DIDPrefix = "did:toro:"

// DIDFromPubKey creates a distributed ID from an Ed25519 public key.
// Format: did:toro:<hex_encoded_pub_key>
func DIDFromPubKey(pub ed25519.PublicKey) string {
	return fmt.Sprintf("%s%s", DIDPrefix, hex.EncodeToString(pub))
}

// PubKeyFromDID extracts the raw public key bytes from a DID string.
// This allows any agent to verify a signature just by looking at the sender's DID.
func PubKeyFromDID(did string) (string, error) {
	if !strings.HasPrefix(did, DIDPrefix) {
		return "", fmt.Errorf("invalid did format")
	}
	return strings.TrimPrefix(did, DIDPrefix), nil
}
