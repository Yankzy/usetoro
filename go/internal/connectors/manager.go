package connectors

import (
	"context"
	"log/slog"
)

// Manager handles the lifecycle of connectors (Plaid, QBO).
type Manager struct {
	logger *slog.Logger
}

func NewManager(logger *slog.Logger) *Manager {
	return &Manager{
		logger: logger,
	}
}

// FetchData simulates fetching data from a provider.
func (m *Manager) FetchData(ctx context.Context, provider string, tenantID string) error {
	m.logger.Info("🔌 Fetching data...", "provider", provider, "tenant_id", tenantID)

	// Real implementation would call Plaid/QBO APIs here.
	// And use RateLimiters.

	return nil
}
