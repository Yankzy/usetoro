package store

import (
	"context"
	"fmt" // Added for fmt.Errorf
	"time"

	"github.com/Yankzy/usetoro/internal/database"
)

// GetWebhookSecret returns the decrypted webhook secret for a connection.
// It uses a Cache-Aside pattern (Cache -> DB -> Decrypt -> Cache).
func (s *Store) GetWebhookSecret(ctx context.Context, connID string) (string, error) {
	cacheKey := "secret:" + connID

	// L1: Check Ristretto Cache (stores decrypted secrets)
	if val, found := s.Cache.Get(cacheKey); found {
		return val.(string), nil
	}

	// L2: Check Database (Cold Path) - retrieves encrypted secret
	encryptedSecret, err := s.Queries.GetWebhookSecret(ctx, connID)
	if err != nil {
		return "", err
	}

	// L3: Decrypt the secret
	decryptedSecret, err := s.Encryptor.Decrypt(encryptedSecret)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt webhook secret: %w", err)
	}

	// Cache the decrypted secret for 5 minutes
	s.Cache.SetWithTTL(cacheKey, decryptedSecret, 1, 5*time.Minute)
	return decryptedSecret, nil
}

// StoreWebhookSecret encrypts and stores a webhook secret for a connection.
// It invalidates the cache entry to ensure the next read fetches the new secret.
func (s *Store) StoreWebhookSecret(ctx context.Context, connID, plaintextSecret string) error {
	// Encrypt the secret before storing
	encryptedSecret, err := s.Encryptor.Encrypt(plaintextSecret)
	if err != nil {
		return fmt.Errorf("failed to encrypt webhook secret: %w", err)
	}

	// Store in database
	err = s.Queries.UpsertWebhookSecret(ctx, database.UpsertWebhookSecretParams{
		ConnectionID:  connID,
		WebhookSecret: encryptedSecret,
	})
	if err != nil {
		return fmt.Errorf("failed to store webhook secret: %w", err)
	}

	// Invalidate cache so next read gets the new secret
	cacheKey := "secret:" + connID
	s.Cache.Del(cacheKey)

	return nil
}
