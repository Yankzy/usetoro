package store

import (
	"context"
	"fmt"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
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

	err = s.Queries.UpsertQBOTokens(ctx, database.UpsertQBOTokensParams{
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
func (s *Store) GetQBOTokens(ctx context.Context, realmID string) (string, string, time.Time, error) {
	row, err := s.Queries.GetQBOTokens(ctx, realmID)
	if err != nil {
		return "", "", time.Time{}, err
	}

	// Decrypt tokens
	accessToken, err := s.Encryptor.Decrypt(row.AccessToken)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("failed to decrypt access token: %w", err)
	}

	refreshToken, err := s.Encryptor.Decrypt(row.RefreshToken)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("failed to decrypt refresh token: %w", err)
	}

	return accessToken, refreshToken, row.ExpiresAt.Time, nil
}

// GetQBOConnection returns the basic connection info (no secrets) for an entity.
func (s *Store) GetQBOConnection(ctx context.Context, entityID string) (*database.ToroCoreQboConnection, error) {
	entityUUID := pgtype.UUID{}
	if err := entityUUID.Scan(entityID); err != nil {
		return nil, fmt.Errorf("invalid entity UUID: %w", err)
	}

	row, err := s.Queries.GetQBOConnection(ctx, entityUUID)
	if err != nil {
		return nil, err
	}

	// We return the raw row which maps to the model
	return &row, nil
}
