// Package store implements the data access layer.
package store

import (
	"context"
	"fmt"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/dgraph-io/ristretto"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store provides access to the database, cache, query execution methods, and encryption.
type Store struct {
	Pool      *pgxpool.Pool
	Queries   *database.Queries
	Cache     *ristretto.Cache
	Encryptor *Encryptor
}

// NewStore creates a new Store instance with encryption support.
// The encryptionKey must be exactly 32 bytes for AES-256.
func NewStore(pool *pgxpool.Pool, cache *ristretto.Cache, encryptionKey []byte) (*Store, error) {
	encryptor, err := NewEncryptor(encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize encryptor: %w", err)
	}

	return &Store{
		Pool:      pool,
		Queries:   database.New(pool),
		Cache:     cache,
		Encryptor: encryptor,
	}, nil
}

// ExecTx runs a callback function within a secure, entity-isolated transaction.
func (s *Store) ExecTx(ctx context.Context, entityID string, fn func(*database.Queries) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, "SET LOCAL app.current_entity = $1", entityID)
	if err != nil {
		return fmt.Errorf("failed to set entity context: %w", err)
	}

	qTx := s.Queries.WithTx(tx)

	if err := fn(qTx); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// Ping checks the connection to the database.
func (s *Store) Ping(ctx context.Context) error {
	return s.Pool.Ping(ctx)
}
