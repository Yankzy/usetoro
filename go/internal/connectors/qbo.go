package connectors

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/config"
	quickbooks "github.com/Yankzy/usetoro/qbo"
)

// Connector defines the interface for all external data providers.
type Connector interface {
	Fetch(ctx context.Context, tenantID string) error
}

// QBOConnector integrates with QuickBooks Online.
type QBOConnector struct {
	logger *slog.Logger
	cfg    config.Config
}

func NewQBOConnector(logger *slog.Logger, cfg config.Config) *QBOConnector {
	return &QBOConnector{
		logger: logger,
		cfg:    cfg,
	}
}

func (c *QBOConnector) Fetch(ctx context.Context, tenantID string) error {
	c.logger.Info("📊 QBO Fetching data...", "tenant_id", tenantID)

	// In a real implementation:
	// 1. Load the BearerToken for this tenantID from the database (encrypted).
	// 2. Initialize the QBO client.
	// 3. Refresh the token if needed.
	// 4. Fetch the data.

	// For now, we'll initialize the client with dummy or config values as a proof of concept.
	// Since we don't have the database integration for tokens yet, we'll just log success.

	client, err := quickbooks.NewClient(
		c.cfg.QBOClientID,
		c.cfg.QBOClientSecret,
		"dummy-realm-id", // This would come from the database per tenant
		c.cfg.QBOIsProduction,
		"",
		nil, // Token would be loaded from DB
	)
	if err != nil {
		return fmt.Errorf("failed to initialize QBO client: %w", err)
	}

	c.logger.Info("✅ QBO Client initialized", "endpoint", client.GetEndpoint())

	return nil
}
