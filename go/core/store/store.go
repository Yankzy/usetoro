// Package store implements the data access layer, handling business logic interactions
// with the database. It manages transaction boundaries, tenant isolation, and Query execution.
package store

import (
	"context"
	"fmt"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store provides access to the database and query execution methods.
// It holds the connection pool and the base Queries instance.
type Store struct {
	Pool    *pgxpool.Pool
	Queries *database.Queries
}

// NewStore creates a new Store instance backed by the given connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		Pool:    pool,
		Queries: database.New(pool),
	}
}

// ExecTx runs a callback function within a secure, tenant-isolated transaction.
// It enforces Row Level Security (RLS) by setting the `app.current_tenant` configuration parameter
// before executing any business logic.
func (s *Store) ExecTx(ctx context.Context, tenantID string, fn func(*database.Queries) error) error {
	// 1. Start a database transaction
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
	// Defer rollback in case of panic or error
	defer tx.Rollback(ctx)

	// 2. SET THE TENANT CONTEXT (The Critical Step)
	// `SET LOCAL` ensures this variable only exists for the duration of this specific transaction.
	// It is safe to use with connection pooling.
	_, err = tx.Exec(ctx, "SET LOCAL app.current_tenant = $1", tenantID)
	if err != nil {
		return fmt.Errorf("failed to set tenant context: %w", err)
	}

	// 3. Initialize sqlc queries with this transaction
	qTx := s.Queries.WithTx(tx)

	// 4. Execute the Business Logic
	if err := fn(qTx); err != nil {
		return err
	}

	// 5. Commit the transaction
	return tx.Commit(ctx)
}
