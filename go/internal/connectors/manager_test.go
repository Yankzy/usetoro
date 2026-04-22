package connectors

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/stretchr/testify/assert"
)

// MockConnector stub
type MockConnector struct {
	FetchFunc func(ctx context.Context, tenantID string) error
}

func (m *MockConnector) Fetch(ctx context.Context, tenantID string) error {
	if m.FetchFunc != nil {
		return m.FetchFunc(ctx, tenantID)
	}
	return nil
}

func TestManager_FetchData_Routing(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Mock config
	cfg := config.Config{
		QBOClientID:     "test-client-id",
		QBOClientSecret: "test-client-secret",
		QBOIsProduction: false,
	}

	// Pass nil for store and nats; we'll mock the connector so it's not used
	mgr := NewManager(logger, &cfg, nil, nil)
	mgr.connectors["qbo"] = &MockConnector{}

	t.Run("Routes to QBO", func(t *testing.T) {
		err := mgr.FetchData(context.Background(), "qbo", "tenant-123")
		// Since it's currently a stub that returns nil, we check for no error
		assert.NoError(t, err)
	})

	t.Run("Fails on Unknown Provider", func(t *testing.T) {
		err := mgr.FetchData(context.Background(), "unknown", "tenant-123")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported provider")
	})
}
