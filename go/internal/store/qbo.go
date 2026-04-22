package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// SaveQBOTokens encrypts and stores the OAuth2 tokens for a QBO connection.
func (s *Store) SaveQBOTokens(ctx context.Context, entityID, realmID, accessToken, refreshToken string, expiresAt time.Time) error {
	// Encrypt sensitive tokens
	encryptedAccess, err := s.Encryptor.Encrypt(accessToken)
	if err != nil {
		return fmt.Errorf("failed to encrypt access token: %w", err)
	}

	encryptedRefresh, err := s.Encryptor.Encrypt(refreshToken)
	if err != nil {
		return fmt.Errorf("failed to encrypt refresh token: %w", err)
	}

	// Store in database
	entityUUID := pgtype.UUID{}
	if err := entityUUID.Scan(entityID); err != nil {
		return fmt.Errorf("invalid entity UUID: %w", err)
	}

	err = s.Queries.UpsertERPTokens(ctx, database.UpsertERPTokensParams{
		ErpSystem:    "quickbooks_online",
		RealmID:      realmID,
		AccessToken:  encryptedAccess,
		RefreshToken: encryptedRefresh,
		ExpiresAt:    pgtype.Timestamptz{Time: expiresAt, Valid: true},
		EntityID:     entityUUID,
	})
	if err != nil {
		return fmt.Errorf("failed to store QBO tokens: %w", err)
	}

	return nil
}

// GetQBOTokens retrieves and decrypts the OAuth2 tokens for a QBO connection.
// Returns accessToken, refreshToken, expiresAt, entityID, error.
func (s *Store) GetQBOTokens(ctx context.Context, realmID string) (string, string, time.Time, string, error) {
	row, err := s.Queries.GetERPTokens(ctx, database.GetERPTokensParams{
		ErpSystem: "quickbooks_online",
		RealmID:   realmID,
	})
	if err != nil {
		return "", "", time.Time{}, "", err
	}

	accessToken, err := s.Encryptor.Decrypt(row.AccessToken)
	if err != nil {
		// Backward-compat: older code paths accidentally persisted plaintext tokens. Only
		// treat it as plaintext if it doesn't look like our base64 ciphertext.
		if errors.Is(err, ErrInvalidCiphertext) && looksLikePlaintextToken(row.AccessToken) {
			accessToken = row.AccessToken
		} else {
			return "", "", time.Time{}, "", fmt.Errorf("failed to decrypt access token: %w", err)
		}
	}

	refreshToken, err := s.Encryptor.Decrypt(row.RefreshToken)
	if err != nil {
		if errors.Is(err, ErrInvalidCiphertext) && looksLikePlaintextToken(row.RefreshToken) {
			refreshToken = row.RefreshToken
		} else {
			return "", "", time.Time{}, "", fmt.Errorf("failed to decrypt refresh token: %w", err)
		}
	}

	entityID := uuid.UUID(row.EntityID.Bytes).String()
	return accessToken, refreshToken, row.ExpiresAt.Time, entityID, nil
}

func looksLikePlaintextToken(s string) bool {
	// Our Encryptor stores standard base64 (may include + / and usually ends with padding),
	// while OAuth tokens (Intuit/QBO) are typically JWT/base64url-ish and contain '.' '-' '_'.
	// This heuristic avoids silently "decrypting" ciphertext with a wrong key.
	return strings.ContainsAny(s, ".-_")
}

// GetQBOConnection returns the basic connection info (no secrets) for an entity.
func (s *Store) GetQBOConnection(ctx context.Context, entityID string) (*database.ToroCoreErpConnection, error) {
	entityUUID := pgtype.UUID{}
	if err := entityUUID.Scan(entityID); err != nil {
		return nil, fmt.Errorf("invalid entity UUID: %w", err)
	}

	row, err := s.Queries.GetERPConnection(ctx, entityUUID)
	if err != nil {
		return nil, err
	}

	// We return the raw row which maps to the model
	return &row, nil
}
