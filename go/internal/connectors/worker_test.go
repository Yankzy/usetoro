package connectors

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
)

type mockQBOConnectedSyncer struct {
	calls []string
}

func (m *mockQBOConnectedSyncer) Fetch(_ context.Context, _ string) error {
	return nil
}

func (m *mockQBOConnectedSyncer) SyncCompanyInfo(_ context.Context, _, _ string) error {
	m.calls = append(m.calls, "company_info")
	return nil
}

func (m *mockQBOConnectedSyncer) SyncFullChartOfAccounts(_ context.Context, _, _ string) (int, error) {
	m.calls = append(m.calls, "coa")
	return 0, nil
}

func (m *mockQBOConnectedSyncer) SyncFullCustomers(_ context.Context, _, _ string) (int, error) {
	m.calls = append(m.calls, "customers")
	return 0, nil
}

func (m *mockQBOConnectedSyncer) SyncFullVendors(_ context.Context, _, _ string) (int, error) {
	m.calls = append(m.calls, "vendors")
	return 0, nil
}

func (m *mockQBOConnectedSyncer) SyncFullPurchases(_ context.Context, _, _ string) (int, error) {
	m.calls = append(m.calls, "purchases")
	return 0, nil
}

func (m *mockQBOConnectedSyncer) SyncFullDeposits(_ context.Context, _, _ string) (int, error) {
	m.calls = append(m.calls, "deposits")
	return 0, nil
}

func TestWorker_processQBOConnected_runsDepositsSync(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	mock := &mockQBOConnectedSyncer{}
	mgr := &Manager{
		logger:     logger,
		cfg:        &config.Config{},
		connectors: map[string]Connector{"qbo": mock},
	}
	w := NewWorker(logger, nil, mgr)

	payload := map[string]string{
		"realm_id":  "realm-123",
		"entity_id": "tenant-456",
		"status":    "connected",
	}
	data, err := json.Marshal(payload)
	require.NoError(t, err)

	msg := &nats.Msg{Subject: "qbo.events.connected", Data: data}
	w.processQBOConnected(msg)

	require.Equal(t, []string{"company_info", "coa", "customers", "vendors", "purchases", "deposits"}, mock.calls)
}
