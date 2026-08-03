package ase

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Yankzy/usetoro/internal/database"
)

// ProviderDependencies encapsulates backend services available to ContextProviders.
type ProviderDependencies struct {
	DB          *database.Queries
	DBPool      *pgxpool.Pool
	Logger      *slog.Logger
	VectorStore *VectorStore
}

// ContextProvider defines the contract for resolving dynamic context for an individual ASENode.
type ContextProvider interface {
	Name() string
	Resolve(ctx context.Context, node *AutonomousSemanticEngineNode, config map[string]any, deps ProviderDependencies) (any, error)
}
