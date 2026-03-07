package erp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Factory Resolvers. These functions will be set during application wiring (in main.go).
// This prevents circular dependencies where the erp package needs to import every single adapter.
var (
	ResolveQBOAdapter func(ctx context.Context, realmID string) (*Provider, error)
	// ResolveNetSuiteAdapter func(ctx context.Context, tenantID string) (*Provider, error)
)

type defaultProviderFactory struct {
	logger *slog.Logger
	db     *pgxpool.Pool
}

// NewProviderFactory creates a new instance of the ProviderFactory.
func NewProviderFactory(logger *slog.Logger, db *pgxpool.Pool) ProviderFactory {
	return &defaultProviderFactory{
		logger: logger,
		db:     db,
	}
}

// GetProviderForTenant looks up the tenant's current ERP connection and returns the adapter.
// Right now our connections are linked via RealmID at the entity level.
func (f *defaultProviderFactory) GetProviderForTenant(ctx context.Context, entityID string) (*Provider, error) {
	// Query the erp_connections table to find the erp_system for this entity.
	// Since Toro is multi-tenant but entities have one ERP, we look it up.
	type connection struct {
		ERPSystem string
		RealmID   string
	}

	var conn connection
	query := `SELECT erp_system, realm_id FROM toro_core.erp_connections WHERE entity_id = $1 LIMIT 1`
	err := f.db.QueryRow(ctx, query, entityID).Scan(&conn.ERPSystem, &conn.RealmID)
	if err != nil {
		return nil, fmt.Errorf("failed to find ERP connection for entity %s: %w", entityID, err)
	}

	return f.GetProviderForRealm(ctx, conn.ERPSystem, conn.RealmID)
}

// GetProviderForRealm returns the specific ERP provider for background workers like CDC that iterate over realms.
func (f *defaultProviderFactory) GetProviderForRealm(ctx context.Context, erpSystem, realmID string) (*Provider, error) {
	switch erpSystem {
	case "quickbooks_online", "qbo":
		if ResolveQBOAdapter == nil {
			return nil, fmt.Errorf("QBO adapter resolver not wired in main.go")
		}
		return ResolveQBOAdapter(ctx, realmID)
	case "netsuite":
		return nil, fmt.Errorf("netsuite adapter not implemented yet")
	default:
		return nil, fmt.Errorf("unsupported ERP system: %s", erpSystem)
	}
}
