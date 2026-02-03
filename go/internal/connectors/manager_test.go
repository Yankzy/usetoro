package connectors

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestManager_FetchData_Routing(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Mock config
	cfg := config.Config{
		QBOClientID:     "test-client-id",
		QBOClientSecret: "test-client-secret",
		QBOIsProduction: false,
	}

	mgr := NewManager(logger, cfg)

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
