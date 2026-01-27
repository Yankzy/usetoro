// Package store implements the data access layer.
package store

import (
	"context"
	"fmt"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/dgraph-io/ristretto"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store provides access to the database, cache, and query execution methods.
type Store struct {
	Pool    *pgxpool.Pool
	Queries *database.Queries
	Cache   *ristretto.Cache
}

// NewStore creates a new Store instance.
func NewStore(pool *pgxpool.Pool, cache *ristretto.Cache) *Store {
	return &Store{
		Pool:    pool,
		Queries: database.New(pool),
		Cache:   cache,
	}
}

// ExecTx runs a callback function within a secure, tenant-isolated transaction.
func (s *Store) ExecTx(ctx context.Context, tenantID string, fn func(*database.Queries) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, "SET LOCAL app.current_tenant = $1", tenantID)
	if err != nil {
		return fmt.Errorf("failed to set tenant context: %w", err)
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
