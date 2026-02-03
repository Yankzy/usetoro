package connectors

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/config"
)

// Manager handles the lifecycle of connectors (Plaid, QBO).
type Manager struct {
	logger     *slog.Logger
	cfg        config.Config
	connectors map[string]Connector
}

func NewManager(logger *slog.Logger, cfg config.Config) *Manager {
	m := &Manager{
		logger:     logger,
		cfg:        cfg,
		connectors: make(map[string]Connector),
	}

	// Register connectors (Factory Pattern)
	m.connectors["qbo"] = NewQBOConnector(logger, cfg)
	// m.connectors["plaid"] = NewPlaidConnector(logger, cfg)

	return m
}

// FetchData routes the request to the appropriate connector.
func (m *Manager) FetchData(ctx context.Context, provider string, tenantID string) error {
	m.logger.Info("🔌 Manager routing fetch request", "provider", provider, "tenant_id", tenantID)

	connector, ok := m.connectors[provider]
	if !ok {
		return fmt.Errorf("unsupported provider: %s", provider)
	}

	return connector.Fetch(ctx, tenantID)
}
