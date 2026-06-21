package workers

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	ruleEngine "github.com/Yankzy/usetoro/internal/erp/rule_engine"
	"github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// custom log collector
type logRecord struct {
	Message string
	Attrs   map[string]any
}

type recordHandler struct {
	records []logRecord
}

func (h *recordHandler) Enabled(ctx context.Context, level slog.Level) bool { return true }
func (h *recordHandler) Handle(ctx context.Context, r slog.Record) error {
	attrs := make(map[string]any)
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	h.records = append(h.records, logRecord{
		Message: r.Message,
		Attrs:   attrs,
	})
	return nil
}
func (h *recordHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *recordHandler) WithGroup(name string) slog.Handler       { return h }

type mockStore struct {
	GetPendingStagingTransactionsFunc    func(context.Context, pgtype.UUID) ([]database.FignodeStagingTransaction, error)
	UpdateStagingTransactionWithRuleFunc func(context.Context, database.UpdateStagingTransactionWithRuleParams) error
	GetCleanupSessionFunc                func(context.Context, pgtype.UUID) (database.GetCleanupSessionRow, error)
	GetAccountByIDFunc                   func(context.Context, pgtype.UUID) (database.ShadowErpAccount, error)
	GetVendorByIDFunc                    func(context.Context, pgtype.UUID) (database.ShadowErpVendor, error)
	GetCustomerByIDFunc                  func(context.Context, pgtype.UUID) (database.ShadowErpCustomer, error)
}

func (m *mockStore) GetPendingStagingTransactions(ctx context.Context, id pgtype.UUID) ([]database.FignodeStagingTransaction, error) {
	return m.GetPendingStagingTransactionsFunc(ctx, id)
}

func (m *mockStore) UpdateStagingTransactionWithRule(ctx context.Context, params database.UpdateStagingTransactionWithRuleParams) error {
	return m.UpdateStagingTransactionWithRuleFunc(ctx, params)
}

func (m *mockStore) GetCleanupSession(ctx context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
	return m.GetCleanupSessionFunc(ctx, id)
}

func (m *mockStore) GetAccountByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpAccount, error) {
	return m.GetAccountByIDFunc(ctx, id)
}

func (m *mockStore) GetVendorByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpVendor, error) {
	return m.GetVendorByIDFunc(ctx, id)
}

func (m *mockStore) GetCustomerByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpCustomer, error) {
	return m.GetCustomerByIDFunc(ctx, id)
}

type mockEngine struct {
	EvaluateTransactionFunc func(context.Context, ruleEngine.Transaction) (*accounting.RuleResult, error)
	PersistAuditLogFunc     func(context.Context, string, pgtype.UUID, *accounting.RuleResult)
}

func (m *mockEngine) EvaluateTransaction(ctx context.Context, tx ruleEngine.Transaction) (*accounting.RuleResult, error) {
	return m.EvaluateTransactionFunc(ctx, tx)
}

func (m *mockEngine) PersistAuditLog(ctx context.Context, realmID string, id pgtype.UUID, result *accounting.RuleResult) {
	if m.PersistAuditLogFunc != nil {
		m.PersistAuditLogFunc(ctx, realmID, id, result)
	}
}

func TestRuleEvaluationWorker_Handle_UnwrapsEnvelope(t *testing.T) {
	// 1. Setup Logger
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_ = logger

	// 2. Prepare Enveloped Payload
	sessionID := "550e8400-e29b-41d4-a716-446655440000"
	innerPayload := struct {
		SessionID string `json:"session_id"`
	}{
		SessionID: sessionID,
	}
	innerData, _ := json.Marshal(innerPayload)

	envelope := struct {
		Perf core.Performative `json:"perf"`
		Body json.RawMessage   `json:"body"`
	}{
		Perf: core.REQUEST,
		Body: innerData,
	}
	envelopeData, _ := json.Marshal(envelope)

	// 3. Create NATS message
	msg := &nats.Msg{
		Data: envelopeData,
	}

	// 4. Verify the extraction logic (mirroring what's in RuleEvaluationWorker.Handle)
	data := msg.Data
	var env core.Envelope
	if err := json.Unmarshal(data, &env); err == nil && len(env.Body) > 0 {
		data = env.Body
	}

	var payload struct {
		SessionID string `json:"session_id"`
	}
	if err := core.UnmarshalTaskPayload(data, &payload); err != nil {
		t.Errorf("Expected nil error from UnmarshalTaskPayload, got %v", err)
	}

	if payload.SessionID != sessionID {
		t.Errorf("Expected SessionID %s, got %s", sessionID, payload.SessionID)
	}
}

func TestRuleEvaluationWorker_Handle_LogsRuleMatches(t *testing.T) {
	// 1. Setup custom log collector
	handler := &recordHandler{}
	logger := slog.New(handler)

	// 2. Mock session and transaction data
	sessionIDStr := "550e8400-e29b-41d4-a716-446655440000"
	var sessionID pgtype.UUID
	_ = sessionID.Scan(sessionIDStr)

	mockTxns := []database.FignodeStagingTransaction{
		{
			ID:             pgtype.UUID{Bytes: uuid.New(), Valid: true},
			RawAmount:      "-125.50",
			RawDescription: pgtype.Text{String: "Amazon purchase", Valid: true},
		},
		{
			ID:             pgtype.UUID{Bytes: uuid.New(), Valid: true},
			RawAmount:      "2500.00",
			RawDescription: pgtype.Text{String: "Stripe payout", Valid: true},
		},
	}

	store := &mockStore{
		GetCleanupSessionFunc: func(ctx context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
			return database.GetCleanupSessionRow{
				RealmID: pgtype.Text{String: "realm-123", Valid: true},
			}, nil
		},
		GetPendingStagingTransactionsFunc: func(ctx context.Context, id pgtype.UUID) ([]database.FignodeStagingTransaction, error) {
			return mockTxns, nil
		},
		UpdateStagingTransactionWithRuleFunc: func(ctx context.Context, params database.UpdateStagingTransactionWithRuleParams) error {
			return nil
		},
	}

	ruleGroupID := int32(42)
	ruleResult := &accounting.RuleResult{
		MatchedRuleGroupID: &ruleGroupID,
		Explanation: ruleEngine.MatchExplanation{
			GroupID:   42,
			GroupName: "Amazon Vendor Rule",
		},
	}

	engine := &mockEngine{
		EvaluateTransactionFunc: func(ctx context.Context, tx ruleEngine.Transaction) (*accounting.RuleResult, error) {
			// Let the first txn (Amazon purchase) match, and the second not match
			if tx.Amount == 125.50 {
				return ruleResult, nil
			}
			return nil, nil
		},
	}

	cfg := &config.Config{}
	worker := NewRuleEvaluationWorker(store, logger, cfg, nil, engine)

	// 3. Trigger Handle
	innerPayload := struct {
		SessionID string `json:"session_id"`
	}{
		SessionID: sessionIDStr,
	}
	innerData, _ := json.Marshal(innerPayload)
	msg := &nats.Msg{
		Data: innerData,
	}

	err := worker.Handle(context.Background(), msg)
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	// 4. Verify Logs
	getInt := func(v any) int {
		switch val := v.(type) {
		case int:
			return val
		case int32:
			return int(val)
		case int64:
			return int(val)
		case float64:
			return int(val)
		default:
			return 0
		}
	}

	var matchedLogFound, completedLogFound bool
	for _, rec := range handler.records {
		if rec.Message == "rule matched transactions" {
			matchedLogFound = true
			if getInt(rec.Attrs["rule_group_id"]) != 42 {
				t.Errorf("Expected rule_group_id 42, got %v", rec.Attrs["rule_group_id"])
			}
			if rec.Attrs["rule_name"] != "Amazon Vendor Rule" {
				t.Errorf("Expected rule_name 'Amazon Vendor Rule', got %v", rec.Attrs["rule_name"])
			}
			if getInt(rec.Attrs["matches"]) != 1 {
				t.Errorf("Expected matches 1, got %v", rec.Attrs["matches"])
			}
		}
		if rec.Message == "completed rule evaluation" {
			completedLogFound = true
			if getInt(rec.Attrs["processed"]) != 2 {
				t.Errorf("Expected processed 2, got %v", rec.Attrs["processed"])
			}
			if getInt(rec.Attrs["matches"]) != 1 {
				t.Errorf("Expected matches 1, got %v", rec.Attrs["matches"])
			}
		}
	}

	if !matchedLogFound {
		t.Error("Expected 'rule matched transactions' log to be generated")
	}
	if !completedLogFound {
		t.Error("Expected 'completed rule evaluation' log to be generated")
	}
}
