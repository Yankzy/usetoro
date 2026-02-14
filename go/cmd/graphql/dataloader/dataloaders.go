package dataloader

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Loaders struct {
	UserLoader *UserLoader
}

func NewLoaders(db *pgxpool.Pool) *Loaders {
	return &Loaders{
		UserLoader: NewUserLoader(db),
	}
}

// Custom context key to avoid collisions
type contextKey string

const loadersKey = contextKey("dataloaders")

// For returns the Loaders from the context
func For(ctx context.Context) *Loaders {
	return ctx.Value(loadersKey).(*Loaders)
}
