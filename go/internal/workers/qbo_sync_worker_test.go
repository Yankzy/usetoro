package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
	quickbooks "github.com/Yankzy/usetoro/internal/erp/adapters/quickbooks/sdk"
)

// ─── mocks ───────────────────────────────────────────────────────────────────

type qboSyncMockStore struct {
	GetReadyFunc    func(context.Context, pgtype.UUID) ([]database.FignodeStagingTransaction, error)
	MarkSyncedFunc  func(context.Context, database.MarkStagingTransactionSyncedParams) error
	MarkFailedFunc  func(context.Context, database.MarkStagingTransactionFailedParams) error
	GetSessionFunc  func(context.Context, pgtype.UUID) (database.GetCleanupSessionRow, error)
	GetAccountFunc  func(context.Context, pgtype.UUID) (database.ShadowErpAccount, error)
	GetVendorFunc   func(context.Context, pgtype.UUID) (database.ShadowErpVendor, error)
	GetCustomerFunc func(context.Context, pgtype.UUID) (database.ShadowErpCustomer, error)
	MarkTransferHoldFunc func(context.Context, pgtype.UUID) error
	GetAccountsByRealmFunc func(context.Context, string) ([]database.ShadowErpAccount, error)
}

func (m *qboSyncMockStore) GetStagingTransactionsReadyForQBO(ctx context.Context, sessionID pgtype.UUID) ([]database.FignodeStagingTransaction, error) {
	return m.GetReadyFunc(ctx, sessionID)
}
func (m *qboSyncMockStore) MarkStagingTransactionSynced(ctx context.Context, p database.MarkStagingTransactionSyncedParams) error {
	return m.MarkSyncedFunc(ctx, p)
}
func (m *qboSyncMockStore) MarkStagingTransactionFailed(ctx context.Context, p database.MarkStagingTransactionFailedParams) error {
	return m.MarkFailedFunc(ctx, p)
}
func (m *qboSyncMockStore) GetCleanupSession(ctx context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
	if m.GetSessionFunc != nil {
		return m.GetSessionFunc(ctx, id)
	}
	return database.GetCleanupSessionRow{}, pgx.ErrNoRows
}
func (m *qboSyncMockStore) GetAccountByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpAccount, error) {
	return m.GetAccountFunc(ctx, id)
}
func (m *qboSyncMockStore) GetVendorByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpVendor, error) {
	return m.GetVendorFunc(ctx, id)
}
func (m *qboSyncMockStore) GetCustomerByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpCustomer, error) {
	return m.GetCustomerFunc(ctx, id)
}
func (m *qboSyncMockStore) MarkStagingTransactionTransferHold(ctx context.Context, id pgtype.UUID) error {
	if m.MarkTransferHoldFunc != nil {
		return m.MarkTransferHoldFunc(ctx, id)
	}
	return nil
}
func (m *qboSyncMockStore) GetAccountsByRealm(ctx context.Context, realmID string) ([]database.ShadowErpAccount, error) {
	if m.GetAccountsByRealmFunc != nil {
		return m.GetAccountsByRealmFunc(ctx, realmID)
	}
	return []database.ShadowErpAccount{}, nil
}

type qboSyncMockConnector struct {
	BatchFunc func(context.Context, string, []quickbooks.BatchItemRequest) ([]quickbooks.BatchItemResponse, error)
}

func (m *qboSyncMockConnector) BatchCreateStagingTransactions(ctx context.Context, realmID string, items []quickbooks.BatchItemRequest) ([]quickbooks.BatchItemResponse, error) {
	return m.BatchFunc(ctx, realmID, items)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func qboSyncTestLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func makeUUID(s string) pgtype.UUID {
	var u pgtype.UUID
	_ = u.Scan(s)
	return u
}

func makeText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

func newQboTestWorker(store QboSyncStore, connector QboSyncConnector) *QboSyncWorker {
	return &QboSyncWorker{
		logger:    qboSyncTestLogger(),
		cfg:       nil,
		nc:        nil,
		db:        store,
		connector: connector,
	}
}

// =============================================================================
// parseAmount
// =============================================================================

func TestParseAmount(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    float64
		wantErr bool
	}{
		{"positive integer", "100", 100, false},
		{"decimal", "99.95", 99.95, false},
		{"with commas", "1,234.56", 1234.56, false},
		{"negative", "-50.00", -50, false},
		{"spaces trimmed", "  42  ", 42, false},
		{"empty string", "", 0, true},
		{"only spaces", "   ", 0, true},
		{"not a number", "abc", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAmount(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// =============================================================================
// parseTxnDate
// =============================================================================

func TestParseTxnDate(t *testing.T) {
	t.Run("valid date", func(t *testing.T) {
		d := pgtype.Date{Time: time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC), Valid: true}
		got := parseTxnDate(d)
		if got.IsZero() {
			t.Error("expected non-zero date")
		}
		if got.Time.Year() != 2026 {
			t.Errorf("year = %d, want 2026", got.Time.Year())
		}
	})

	t.Run("null date defaults to now", func(t *testing.T) {
		before := time.Now()
		got := parseTxnDate(pgtype.Date{Valid: false})
		after := time.Now()
		if got.Time.Before(before.Add(-time.Second)) || got.Time.After(after.Add(time.Second)) {
			t.Error("null date should default to time.Now()")
		}
	})
}

// =============================================================================
// formatLineDescription
// =============================================================================

func TestFormatLineDescription(t *testing.T) {
	t.Run("valid reasoning", func(t *testing.T) {
		got := formatLineDescription(makeText("Matches past Starbucks purchases"))
		if got != "AI Reasoning: Matches past Starbucks purchases" {
			t.Errorf("got %q, want %q", got, "Matches past Starbucks purchases")
		}
	})
	t.Run("null reasoning returns empty", func(t *testing.T) {
		if got := formatLineDescription(pgtype.Text{Valid: false}); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})
	t.Run("empty reasoning returns empty", func(t *testing.T) {
		if got := formatLineDescription(makeText("")); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})
}

// =============================================================================
// isOAuthRevoked
// =============================================================================

func TestIsOAuthRevoked(t *testing.T) {
	t.Run("contains invalid_grant", func(t *testing.T) {
		if !isOAuthRevoked(errors.New("oauth2: invalid_grant token expired")) {
			t.Error("should detect invalid_grant")
		}
	})
	t.Run("other error", func(t *testing.T) {
		if isOAuthRevoked(errors.New("network timeout")) {
			t.Error("should not match unrelated error")
		}
	})
	t.Run("nil error", func(t *testing.T) {
		if isOAuthRevoked(nil) {
			t.Error("nil should be false")
		}
	})
}

// =============================================================================
// extractRealmID
// =============================================================================

func TestExtractSessionAndRealm(t *testing.T) {
	t.Run("session_id lookup", func(t *testing.T) {
		store := &qboSyncMockStore{
			GetSessionFunc: func(_ context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
				return database.GetCleanupSessionRow{RealmID: makeText("session-realm")}, nil
			},
		}
		w := newQboTestWorker(store, nil)
		data, _ := json.Marshal(map[string]string{"session_id": "550e8400-e29b-41d4-a716-446655440000"})
		_, session, err := w.extractSessionAndRealm(context.Background(), data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if session.RealmID.String != "session-realm" {
			t.Errorf("got %q, want session-realm", session.RealmID.String)
		}
	})

	t.Run("session_id lookup fails", func(t *testing.T) {
		store := &qboSyncMockStore{
			GetSessionFunc: func(_ context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
				return database.GetCleanupSessionRow{}, errors.New("not found")
			},
		}
		w := newQboTestWorker(store, nil)
		data, _ := json.Marshal(map[string]string{"session_id": "550e8400-e29b-41d4-a716-446655440000"})
		_, _, err := w.extractSessionAndRealm(context.Background(), data)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("no session_id", func(t *testing.T) {
		w := newQboTestWorker(nil, nil)
		data, _ := json.Marshal(map[string]string{"other": "field"})
		_, _, err := w.extractSessionAndRealm(context.Background(), data)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		w := newQboTestWorker(nil, nil)
		_, _, err := w.extractSessionAndRealm(context.Background(), []byte("not json"))
		if err == nil {
			t.Fatal("expected error")
		}
	})
}
// =============================================================================
// resolveEntityERPID
// =============================================================================

func TestResolveEntityERPID(t *testing.T) {
	vid := makeUUID("550e8400-e29b-41d4-a716-446655440000")
	cid := makeUUID("660e8400-e29b-41d4-a716-446655440001")

	t.Run("outflow uses vendor", func(t *testing.T) {
		store := &qboSyncMockStore{
			GetVendorFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpVendor, error) {
				return database.ShadowErpVendor{ErpID: "vendor-erp-1"}, nil
			},
		}
		w := newQboTestWorker(store, nil)
		row := database.FignodeStagingTransaction{
			CashDirection:     makeText("OUTFLOW"),
			PredictedVendorID: vid,
		}
		got, err := w.resolveEntityERPID(context.Background(), "r", row)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "vendor-erp-1" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("outflow respects override", func(t *testing.T) {
		override := makeUUID("770e8400-e29b-41d4-a716-446655440002")
		store := &qboSyncMockStore{
			GetVendorFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpVendor, error) {
				return database.ShadowErpVendor{ErpID: "override-erp"}, nil
			},
		}
		w := newQboTestWorker(store, nil)
		row := database.FignodeStagingTransaction{
			CashDirection:     makeText("OUTFLOW"),
			PredictedVendorID: vid,
			OverrideVendorID:  override,
		}
		got, err := w.resolveEntityERPID(context.Background(), "r", row)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "override-erp" {
			t.Errorf("got %q, want override-erp", got)
		}
	})

	t.Run("outflow no vendor returns error", func(t *testing.T) {
		w := newQboTestWorker(nil, nil)
		row := database.FignodeStagingTransaction{CashDirection: makeText("OUTFLOW")}
		_, err := w.resolveEntityERPID(context.Background(), "r", row)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("inflow uses customer", func(t *testing.T) {
		store := &qboSyncMockStore{
			GetCustomerFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpCustomer, error) {
				return database.ShadowErpCustomer{ErpID: "customer-erp-1"}, nil
			},
		}
		w := newQboTestWorker(store, nil)
		row := database.FignodeStagingTransaction{
			CashDirection:       makeText("INFLOW"),
			PredictedCustomerID: cid,
		}
		got, err := w.resolveEntityERPID(context.Background(), "r", row)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "customer-erp-1" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("inflow no customer returns error", func(t *testing.T) {
		w := newQboTestWorker(nil, nil)
		row := database.FignodeStagingTransaction{CashDirection: makeText("INFLOW")}
		_, err := w.resolveEntityERPID(context.Background(), "r", row)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("vendor has no ERP ID returns error", func(t *testing.T) {
		store := &qboSyncMockStore{
			GetVendorFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpVendor, error) {
				return database.ShadowErpVendor{ErpID: "", DisplayName: "NoERP Inc"}, nil
			},
		}
		w := newQboTestWorker(store, nil)
		row := database.FignodeStagingTransaction{
			CashDirection:     makeText("OUTFLOW"),
			PredictedVendorID: vid,
		}
		_, err := w.resolveEntityERPID(context.Background(), "r", row)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

// =============================================================================
// resolveAccountERPID
// =============================================================================

func TestResolveAccountERPID(t *testing.T) {
	aid := makeUUID("550e8400-e29b-41d4-a716-446655440000")

	t.Run("predicted account", func(t *testing.T) {
		store := &qboSyncMockStore{
			GetAccountFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpAccount, error) {
				return database.ShadowErpAccount{ErpID: "acct-erp-1"}, nil
			},
		}
		w := newQboTestWorker(store, nil)
		row := database.FignodeStagingTransaction{PredictedAccountID: aid}
		got, err := w.resolveAccountERPID(context.Background(), "r", row)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ErpID != "acct-erp-1" {
			t.Errorf("got %q", got.ErpID)
		}
	})

	t.Run("override takes precedence", func(t *testing.T) {
		override := makeUUID("770e8400-e29b-41d4-a716-446655440002")
		store := &qboSyncMockStore{
			GetAccountFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpAccount, error) {
				return database.ShadowErpAccount{ErpID: "override-acct-erp"}, nil
			},
		}
		w := newQboTestWorker(store, nil)
		row := database.FignodeStagingTransaction{
			PredictedAccountID: aid,
			OverrideAccountID:  override,
		}
		got, err := w.resolveAccountERPID(context.Background(), "r", row)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ErpID != "override-acct-erp" {
			t.Errorf("got %q, want override-acct-erp", got.ErpID)
		}
	})

	t.Run("no account returns error", func(t *testing.T) {
		w := newQboTestWorker(nil, nil)
		row := database.FignodeStagingTransaction{}
		_, err := w.resolveAccountERPID(context.Background(), "r", row)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("account has no ERP ID returns error", func(t *testing.T) {
		store := &qboSyncMockStore{
			GetAccountFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpAccount, error) {
				return database.ShadowErpAccount{ErpID: "", Name: "NoERP Acct"}, nil
			},
		}
		w := newQboTestWorker(store, nil)
		row := database.FignodeStagingTransaction{PredictedAccountID: aid}
		_, err := w.resolveAccountERPID(context.Background(), "r", row)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

// =============================================================================
// hydrateBatchItems
// =============================================================================

func TestHydrateBatchItems(t *testing.T) {
	sid := makeUUID("550e8400-e29b-41d4-a716-446655440000")
	baid := makeUUID("660e8400-e29b-41d4-a716-446655440001")
	vid := makeUUID("770e8400-e29b-41d4-a716-446655440002")
	cid := makeUUID("880e8400-e29b-41d4-a716-446655440003")
	aid := makeUUID("990e8400-e29b-41d4-a716-446655440004")

	baseStore := &qboSyncMockStore{
		GetSessionFunc: func(_ context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
			return database.GetCleanupSessionRow{BankAccountID: baid}, nil
		},
		GetAccountFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpAccount, error) {
			return database.ShadowErpAccount{ErpID: "acct-erp"}, nil
		},
		GetVendorFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpVendor, error) {
			return database.ShadowErpVendor{ErpID: "vendor-erp"}, nil
		},
		GetCustomerFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpCustomer, error) {
			return database.ShadowErpCustomer{ErpID: "customer-erp"}, nil
		},
	}

	t.Run("inflow produces deposit", func(t *testing.T) {
		w := newQboTestWorker(baseStore, nil)
		row := database.FignodeStagingTransaction{
			ID:                  makeUUID("00000000-0000-0000-0000-000000000001"),
			SessionID:           sid,
			CashDirection:       makeText("INFLOW"),
			PredictedCustomerID: cid,
			PredictedAccountID:  aid,
			RawAmount:           "100.00",
			ParsedDate:          pgtype.Date{Time: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), Valid: true},
			AiReasoning:         makeText("monthly stripe payout"),
		}
		items, itemMap, failures := w.hydrateBatchItems(context.Background(), "r", []database.FignodeStagingTransaction{row}, "acct-erp", false)
		if len(failures) > 0 {
			t.Fatalf("unexpected hydration failures: %v", failures)
		}
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		item := items[0]
		if item.Entity != "Deposit" {
			t.Errorf("entity = %q, want Deposit", item.Entity)
		}
		deposit, ok := item.Payload.(quickbooks.Deposit)
		if !ok {
			t.Fatal("payload is not Deposit")
		}
		if deposit.PrivateNote == "" {
			t.Error("PrivateNote should not be empty")
		}
		if len(deposit.Line) != 1 {
			t.Fatalf("expected 1 line, got %d", len(deposit.Line))
		}
		if deposit.Line[0].Description != "AI Reasoning: monthly stripe payout" {
			t.Errorf("line description = %q", deposit.Line[0].Description)
		}
		if deposit.Line[0].DepositLineDetail == nil {
			t.Fatal("DepositLineDetail is nil")
		}
		mapping := itemMap[item.BId]
		if mapping.EntityType != "Deposit" {
			t.Errorf("mapping entity = %q", mapping.EntityType)
		}
	})

	t.Run("outflow produces purchase", func(t *testing.T) {
		w := newQboTestWorker(baseStore, nil)
		row := database.FignodeStagingTransaction{
			ID:                 makeUUID("00000000-0000-0000-0000-000000000002"),
			SessionID:          sid,
			CashDirection:      makeText("OUTFLOW"),
			PredictedVendorID:  vid,
			PredictedAccountID: aid,
			RawAmount:          "-50.00",
			ParsedDate:         pgtype.Date{Time: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), Valid: true},
			AiReasoning:        makeText("uber ride expense"),
		}
		items, itemMap, failures := w.hydrateBatchItems(context.Background(), "r", []database.FignodeStagingTransaction{row}, "acct-erp", false)
		if len(failures) > 0 {
			t.Fatalf("unexpected hydration failures: %v", failures)
		}
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		item := items[0]
		if item.Entity != "Purchase" {
			t.Errorf("entity = %q, want Purchase", item.Entity)
		}
		purchase, ok := item.Payload.(quickbooks.Purchase)
		if !ok {
			t.Fatal("payload is not Purchase")
		}
		if purchase.PrivateNote == "" {
			t.Error("PrivateNote should not be empty")
		}
		if purchase.PaymentType != "Cash" {
			t.Errorf("PaymentType = %q, want Cash", purchase.PaymentType)
		}
		if len(purchase.Line) != 1 {
			t.Fatalf("expected 1 line, got %d", len(purchase.Line))
		}
		if purchase.Line[0].Description != "AI Reasoning: uber ride expense" {
			t.Errorf("line description = %q", purchase.Line[0].Description)
		}
		mapping := itemMap[item.BId]
		if mapping.EntityType != "Purchase" {
			t.Errorf("mapping entity = %q", mapping.EntityType)
		}
	})

	t.Run("negative outflow amount uses absolute value", func(t *testing.T) {
		w := newQboTestWorker(baseStore, nil)
		row := database.FignodeStagingTransaction{
			ID:                 makeUUID("00000000-0000-0000-0000-000000000003"),
			SessionID:          sid,
			CashDirection:      makeText("OUTFLOW"),
			PredictedVendorID:  vid,
			PredictedAccountID: aid,
			RawAmount:          "-99.99",
		}
		items, _, failures := w.hydrateBatchItems(context.Background(), "r", []database.FignodeStagingTransaction{row}, "acct-erp", false)
		if len(failures) > 0 {
			t.Fatalf("unexpected failures: %v", failures)
		}
		purchase := items[0].Payload.(quickbooks.Purchase)
		if purchase.TotalAmt.String() != "99.99" {
			t.Errorf("TotalAmt = %s, want 99.99", purchase.TotalAmt)
		}
		if purchase.Line[0].Amount.String() != "99.99" {
			t.Errorf("Line.Amount = %s, want 99.99", purchase.Line[0].Amount)
		}
	})

	t.Run("payment account failure marks all rows", func(t *testing.T) {
		store := &qboSyncMockStore{
			GetSessionFunc: func(_ context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
				return database.GetCleanupSessionRow{}, errors.New("no session")
			},
		}
		w := newQboTestWorker(store, nil)
		row := database.FignodeStagingTransaction{
			ID:           makeUUID("00000000-0000-0000-0000-000000000001"),
			SessionID:    sid,
			CashDirection: makeText("INFLOW"),
		}
		items, _, failures := w.hydrateBatchItems(context.Background(), "r", []database.FignodeStagingTransaction{row}, "acct-erp", false)
		if len(items) != 0 {
			t.Errorf("expected 0 items, got %d", len(items))
		}
		if len(failures) != 1 {
			t.Errorf("expected 1 failure, got %d", len(failures))
		}
	})

	t.Run("missing cash_direction fails", func(t *testing.T) {
		w := newQboTestWorker(baseStore, nil)
		row := database.FignodeStagingTransaction{
			ID:        makeUUID("00000000-0000-0000-0000-000000000001"),
			SessionID: sid,
		}
		_, _, failures := w.hydrateBatchItems(context.Background(), "r", []database.FignodeStagingTransaction{row}, "acct-erp", false)
		if _, ok := failures[row.ID]; !ok {
			t.Error("expected missing cash_direction failure")
		}
	})

	t.Run("bad entity reference fails", func(t *testing.T) {
		w := newQboTestWorker(baseStore, nil)
		row := database.FignodeStagingTransaction{
			ID:            makeUUID("00000000-0000-0000-0000-000000000001"),
			SessionID:     sid,
			CashDirection: makeText("OUTFLOW"),
		}
		_, _, failures := w.hydrateBatchItems(context.Background(), "r", []database.FignodeStagingTransaction{row}, "acct-erp", false)
		if _, ok := failures[row.ID]; !ok {
			t.Error("expected entity resolution failure")
		}
	})

	t.Run("bad amount fails", func(t *testing.T) {
		w := newQboTestWorker(baseStore, nil)
		row := database.FignodeStagingTransaction{
			ID:                  makeUUID("00000000-0000-0000-0000-000000000001"),
			SessionID:           sid,
			CashDirection:       makeText("INFLOW"),
			PredictedCustomerID: cid,
			PredictedAccountID:  aid,
			RawAmount:           "not-a-number",
		}
		_, _, failures := w.hydrateBatchItems(context.Background(), "r", []database.FignodeStagingTransaction{row}, "acct-erp", false)
		if _, ok := failures[row.ID]; !ok {
			t.Error("expected amount parse failure")
		}
	})

	t.Run("unknown cash_direction fails", func(t *testing.T) {
		w := newQboTestWorker(baseStore, nil)
		row := database.FignodeStagingTransaction{
			ID:                  makeUUID("00000000-0000-0000-0000-000000000001"),
			SessionID:           sid,
			CashDirection:       makeText("SIDEWAYS"),
			PredictedCustomerID: cid,
			PredictedAccountID:  aid,
			RawAmount:           "50.00",
		}
		_, _, failures := w.hydrateBatchItems(context.Background(), "r", []database.FignodeStagingTransaction{row}, "acct-erp", false)
		if _, ok := failures[row.ID]; !ok {
			t.Error("expected unknown cash_direction failure")
		}
	})

	t.Run("null ai_reasoning gets empty description", func(t *testing.T) {
		w := newQboTestWorker(baseStore, nil)
		row := database.FignodeStagingTransaction{
			ID:                  makeUUID("00000000-0000-0000-0000-000000000001"),
			SessionID:           sid,
			CashDirection:       makeText("INFLOW"),
			PredictedCustomerID: cid,
			PredictedAccountID:  aid,
			RawAmount:           "100.00",
			AiReasoning:         pgtype.Text{Valid: false},
		}
		items, _, failures := w.hydrateBatchItems(context.Background(), "r", []database.FignodeStagingTransaction{row}, "acct-erp", false)
		if len(failures) > 0 {
			t.Fatalf("unexpected failures: %v", failures)
		}
		deposit := items[0].Payload.(quickbooks.Deposit)
		if deposit.Line[0].Description != "" {
			t.Errorf("description should be empty, got %q", deposit.Line[0].Description)
		}
	})

	t.Run("TRANSFER_HOLD intercepts OUTFLOW matching credit card name", func(t *testing.T) {
		holdCalled := false
		store := &qboSyncMockStore{
			GetReadyFunc: baseStore.GetReadyFunc,
			GetSessionFunc: baseStore.GetSessionFunc,
			GetAccountFunc: baseStore.GetAccountFunc,
			GetVendorFunc: baseStore.GetVendorFunc,
			GetCustomerFunc: baseStore.GetCustomerFunc,
			GetAccountsByRealmFunc: func(ctx context.Context, realmID string) ([]database.ShadowErpAccount, error) {
				return []database.ShadowErpAccount{
					{Name: "Amex Corporate", AccountType: "Credit Card"},
				}, nil
			},
			MarkTransferHoldFunc: func(ctx context.Context, id pgtype.UUID) error {
				holdCalled = true
				return nil
			},
		}
		w := newQboTestWorker(store, nil)
		row := database.FignodeStagingTransaction{
			ID:                 makeUUID("00000000-0000-0000-0000-000000000020"),
			SessionID:          makeUUID("550e8400-e29b-41d4-a716-446655440000"),
			CashDirection:      makeText("OUTFLOW"),
			PredictedVendorID:  makeUUID("770e8400-e29b-41d4-a716-446655440002"),
			PredictedAccountID: makeUUID("990e8400-e29b-41d4-a716-446655440004"),
			RawAmount:          "500.00",
			RawDescription:     makeText("Payment to amex corporate account"),
		}
		items, _, failures := w.hydrateBatchItems(context.Background(), "r", []database.FignodeStagingTransaction{row}, "acct-erp", false)
		if len(failures) > 0 {
			t.Fatalf("unexpected failures, got %v", failures)
		}
		if len(items) != 0 {
			t.Fatalf("expected 0 items (intercepted), got %d", len(items))
		}
		if !holdCalled {
			t.Errorf("expected MarkStagingTransactionTransferHold to be called")
		}
	})

	t.Run("Deposit Guardrail blocks Accounts Receivable", func(t *testing.T) {
		store := &qboSyncMockStore{
			GetReadyFunc: baseStore.GetReadyFunc,
			GetSessionFunc: baseStore.GetSessionFunc,
			GetVendorFunc: baseStore.GetVendorFunc,
			GetCustomerFunc: baseStore.GetCustomerFunc,
			GetAccountFunc: func(ctx context.Context, id pgtype.UUID) (database.ShadowErpAccount, error) {
				return database.ShadowErpAccount{ErpID: "ar-123", AccountType: "Accounts Receivable", AccountSubType: pgtype.Text{String: "AccountsReceivable", Valid: true}}, nil
			},
		}
		w := newQboTestWorker(store, nil)
		row := database.FignodeStagingTransaction{
			ID:                 makeUUID("00000000-0000-0000-0000-000000000021"),
			SessionID:          makeUUID("550e8400-e29b-41d4-a716-446655440000"),
			CashDirection:      makeText("INFLOW"),
			PredictedCustomerID: makeUUID("880e8400-e29b-41d4-a716-446655440003"),
			PredictedAccountID: makeUUID("990e8400-e29b-41d4-a716-446655440004"),
			RawAmount:          "100.00",
		}
		items, _, failures := w.hydrateBatchItems(context.Background(), "r", []database.FignodeStagingTransaction{row}, "acct-erp", false)
		if len(items) != 0 {
			t.Fatalf("expected 0 items, got %d", len(items))
		}
		if msg, ok := failures[row.ID]; !ok || "Direct deposits cannot hit AR. Please map to an Income account." != msg {
			t.Errorf("expected AR guardrail failure message, got %v", failures)
		}
	})

	t.Run("Deposit Guardrail blocks Retained Earnings", func(t *testing.T) {
		store := &qboSyncMockStore{
			GetReadyFunc: baseStore.GetReadyFunc,
			GetSessionFunc: baseStore.GetSessionFunc,
			GetVendorFunc: baseStore.GetVendorFunc,
			GetCustomerFunc: baseStore.GetCustomerFunc,
			GetAccountFunc: func(ctx context.Context, id pgtype.UUID) (database.ShadowErpAccount, error) {
				return database.ShadowErpAccount{ErpID: "eq-123", AccountType: "Equity", AccountSubType: pgtype.Text{String: "RetainedEarnings", Valid: true}}, nil
			},
		}
		w := newQboTestWorker(store, nil)
		row := database.FignodeStagingTransaction{
			ID:                 makeUUID("00000000-0000-0000-0000-000000000022"),
			SessionID:          makeUUID("550e8400-e29b-41d4-a716-446655440000"),
			CashDirection:      makeText("INFLOW"),
			PredictedCustomerID: makeUUID("880e8400-e29b-41d4-a716-446655440003"),
			PredictedAccountID: makeUUID("990e8400-e29b-41d4-a716-446655440004"),
			RawAmount:          "100.00",
		}
		items, _, failures := w.hydrateBatchItems(context.Background(), "r", []database.FignodeStagingTransaction{row}, "acct-erp", false)
		if len(items) != 0 {
			t.Fatalf("expected 0 items, got %d", len(items))
		}
		if msg, ok := failures[row.ID]; !ok || "System Block: Intuit prohibits direct equity entries to Retained Earnings." != msg {
			t.Errorf("expected RetainedEarnings guardrail failure message, got %v", failures)
		}
	})
}

// =============================================================================
// processBatchResponses
// =============================================================================

func TestProcessBatchResponses(t *testing.T) {
	txnID := makeUUID("00000000-0000-0000-0000-000000000001")

	t.Run("successful deposit", func(t *testing.T) {
		var syncedID pgtype.UUID
		var syncedErpID pgtype.Text
		var failedCount int

		store := &qboSyncMockStore{
			MarkSyncedFunc: func(_ context.Context, p database.MarkStagingTransactionSyncedParams) error {
				syncedID = p.ID
				syncedErpID = p.ErpTransactionID
				return nil
			},
			MarkFailedFunc: func(_ context.Context, p database.MarkStagingTransactionFailedParams) error {
				failedCount++
				return nil
			},
		}
		w := newQboTestWorker(store, nil)
		itemMap := map[string]batchIDMapping{"bid-1": {TxnID: txnID, EntityType: "Deposit"}}
		responses := []quickbooks.BatchItemResponse{
			{BId: "bid-1", Deposit: &quickbooks.Deposit{Id: "dep-123", SyncToken: "0"}},
		}
		w.processBatchResponses(context.Background(), responses, itemMap, "r")
		if syncedID != txnID {
			t.Error("MarkSynced not called with correct ID")
		}
		if syncedErpID.String != "dep-123" {
			t.Errorf("erp ID = %q, want dep-123", syncedErpID.String)
		}
		if failedCount > 0 {
			t.Error("MarkFailed should not have been called")
		}
	})

	t.Run("successful purchase", func(t *testing.T) {
		var syncedErpID pgtype.Text
		store := &qboSyncMockStore{
			MarkSyncedFunc: func(_ context.Context, p database.MarkStagingTransactionSyncedParams) error {
				syncedErpID = p.ErpTransactionID
				return nil
			},
			MarkFailedFunc: func(_ context.Context, p database.MarkStagingTransactionFailedParams) error { return nil },
		}
		w := newQboTestWorker(store, nil)
		itemMap := map[string]batchIDMapping{"bid-2": {TxnID: txnID, EntityType: "Purchase"}}
		responses := []quickbooks.BatchItemResponse{
			{BId: "bid-2", Purchase: &quickbooks.Purchase{Id: "pur-456", SyncToken: "0"}},
		}
		w.processBatchResponses(context.Background(), responses, itemMap, "r")
		if syncedErpID.String != "pur-456" {
			t.Errorf("erp ID = %q, want pur-456", syncedErpID.String)
		}
	})

	t.Run("batch fault marks failed", func(t *testing.T) {
		var failedID pgtype.UUID
		var failedMsg pgtype.Text
		store := &qboSyncMockStore{
			MarkSyncedFunc: func(_ context.Context, p database.MarkStagingTransactionSyncedParams) error { return nil },
			MarkFailedFunc: func(_ context.Context, p database.MarkStagingTransactionFailedParams) error {
				failedID = p.ID
				failedMsg = p.ErrorMessage
				return nil
			},
		}
		w := newQboTestWorker(store, nil)
		itemMap := map[string]batchIDMapping{"bid-3": {TxnID: txnID, EntityType: "Deposit"}}
		responses := []quickbooks.BatchItemResponse{
			{BId: "bid-3", Fault: &quickbooks.Fault{Error: []quickbooks.ErrorDetail{{Message: "locked period"}}}},
		}
		w.processBatchResponses(context.Background(), responses, itemMap, "r")
		if failedID != txnID {
			t.Error("MarkFailed not called with correct ID")
		}
		if failedMsg.String != "locked period" {
			t.Errorf("error message = %q", failedMsg.String)
		}
	})

	t.Run("unrecognized bId skipped", func(t *testing.T) {
		var synced, failed int
		store := &qboSyncMockStore{
			MarkSyncedFunc: func(_ context.Context, p database.MarkStagingTransactionSyncedParams) error { synced++; return nil },
			MarkFailedFunc: func(_ context.Context, p database.MarkStagingTransactionFailedParams) error { failed++; return nil },
		}
		w := newQboTestWorker(store, nil)
		responses := []quickbooks.BatchItemResponse{
			{BId: "unknown-bid", Deposit: &quickbooks.Deposit{Id: "dep-1"}},
		}
		w.processBatchResponses(context.Background(), responses, map[string]batchIDMapping{}, "r")
		if synced > 0 || failed > 0 {
			t.Error("should not call DB for unrecognized bId")
		}
	})

	t.Run("empty entity ID marks failed", func(t *testing.T) {
		var failedMsg pgtype.Text
		store := &qboSyncMockStore{
			MarkSyncedFunc: func(_ context.Context, p database.MarkStagingTransactionSyncedParams) error { return nil },
			MarkFailedFunc: func(_ context.Context, p database.MarkStagingTransactionFailedParams) error {
				failedMsg = p.ErrorMessage
				return nil
			},
		}
		w := newQboTestWorker(store, nil)
		itemMap := map[string]batchIDMapping{"bid-4": {TxnID: txnID, EntityType: "Deposit"}}
		responses := []quickbooks.BatchItemResponse{
			{BId: "bid-4", Deposit: &quickbooks.Deposit{Id: ""}},
		}
		w.processBatchResponses(context.Background(), responses, itemMap, "r")
		if !strings.Contains(failedMsg.String, "no entity ID") {
			t.Errorf("error = %q, want 'no entity ID'", failedMsg.String)
		}
	})

	t.Run("nil deposit/purchase marks failed", func(t *testing.T) {
		var failedMsg pgtype.Text
		store := &qboSyncMockStore{
			MarkSyncedFunc: func(_ context.Context, p database.MarkStagingTransactionSyncedParams) error { return nil },
			MarkFailedFunc: func(_ context.Context, p database.MarkStagingTransactionFailedParams) error {
				failedMsg = p.ErrorMessage
				return nil
			},
		}
		w := newQboTestWorker(store, nil)
		itemMap := map[string]batchIDMapping{"bid-5": {TxnID: txnID, EntityType: "Purchase"}}
		responses := []quickbooks.BatchItemResponse{
			{BId: "bid-5"},
		}
		w.processBatchResponses(context.Background(), responses, itemMap, "r")
		if !strings.Contains(failedMsg.String, "no entity ID") {
			t.Errorf("error = %q", failedMsg.String)
		}
	})
}

// =============================================================================
// Handle
// =============================================================================

func TestHandle_PoisonPill(t *testing.T) {
	w := newQboTestWorker(nil, nil)
	msg := &nats.Msg{Subject: "worker.inbox.qbo_sync", Data: []byte(`{}`)}
	meta, _ := msg.Metadata()
	if meta == nil {
		t.Skip("cannot set NumDelivered on nil metadata outside real NATS subscription")
	}
	meta.NumDelivered = 4
	err := w.Handle(context.Background(), msg)
	if err != nil {
		t.Errorf("poison pill should return nil, got %v", err)
	}
}

func TestHandle_NoRealmID(t *testing.T) {
	w := newQboTestWorker(nil, nil)
	msg := &nats.Msg{Subject: "worker.inbox.qbo_sync", Data: []byte(`{}`)}
	err := w.Handle(context.Background(), msg)
	if err != nil {
		t.Errorf("missing realm_id should return nil (ACK), got %v", err)
	}
}

func TestHandle_InvalidJSON(t *testing.T) {
	w := newQboTestWorker(nil, nil)
	msg := &nats.Msg{Subject: "worker.inbox.qbo_sync", Data: []byte(`not json`)}
	err := w.Handle(context.Background(), msg)
	if err != nil {
		t.Errorf("invalid JSON should return nil (ACK), got %v", err)
	}
}

func TestHandle_NoReadyRows(t *testing.T) {
	store := &qboSyncMockStore{
		GetReadyFunc: func(_ context.Context, sessionID pgtype.UUID) ([]database.FignodeStagingTransaction, error) {
			return nil, nil
		},
	}
	w := newQboTestWorker(store, nil)
	payload, _ := json.Marshal(map[string]string{"session_id": "550e8400-e29b-41d4-a716-446655440000"})
	msg := &nats.Msg{Subject: "worker.inbox.qbo_sync", Data: payload}
	err := w.Handle(context.Background(), msg)
	if err != nil {
		t.Errorf("no rows should return nil, got %v", err)
	}
}

func TestHandle_FetchError(t *testing.T) {
	store := &qboSyncMockStore{
		GetReadyFunc: func(_ context.Context, sessionID pgtype.UUID) ([]database.FignodeStagingTransaction, error) {
			return nil, errors.New("db down")
		},
	}
	w := newQboTestWorker(store, nil)
	payload, _ := json.Marshal(map[string]string{"session_id": "550e8400-e29b-41d4-a716-446655440000"})
	msg := &nats.Msg{Subject: "worker.inbox.qbo_sync", Data: payload}
	err := w.Handle(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error to trigger NAK")
	}
}

func TestHandle_AllHydrationFailures(t *testing.T) {
	var failedIDs []pgtype.UUID
	store := &qboSyncMockStore{
		GetReadyFunc: func(_ context.Context, sessionID pgtype.UUID) ([]database.FignodeStagingTransaction, error) {
			return []database.FignodeStagingTransaction{
				{
					ID:                  makeUUID("00000000-0000-0000-0000-000000000001"),
					CashDirection:       makeText("INFLOW"),
					RawAmount:           "100",
					PredictedAccountID:  makeUUID("00000000-0000-0000-0000-000000000010"),
					PredictedCustomerID: makeUUID("00000000-0000-0000-0000-000000000020"),
				},
			}, nil
		},
		GetSessionFunc: func(_ context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
			return database.GetCleanupSessionRow{}, errors.New("no session")
		},
		MarkFailedFunc: func(_ context.Context, p database.MarkStagingTransactionFailedParams) error {
			failedIDs = append(failedIDs, p.ID)
			return nil
		},
	}
	w := newQboTestWorker(store, nil)
	payload, _ := json.Marshal(map[string]string{"session_id": "550e8400-e29b-41d4-a716-446655440000"})
	msg := &nats.Msg{Subject: "worker.inbox.qbo_sync", Data: payload}
	err := w.Handle(context.Background(), msg)
	if err != nil {
		t.Errorf("all hydration failures should return nil, got %v", err)
	}
	if len(failedIDs) != 1 {
		t.Errorf("expected 1 marked failed, got %d", len(failedIDs))
	}
}

func TestHandle_BatchPushFatal(t *testing.T) {
	sid := makeUUID("550e8400-e29b-41d4-a716-446655440000")
	baid := makeUUID("660e8400-e29b-41d4-a716-446655440001")
	cid := makeUUID("770e8400-e29b-41d4-a716-446655440002")
	aid := makeUUID("880e8400-e29b-41d4-a716-446655440003")

	var failedIDs []pgtype.UUID
	store := &qboSyncMockStore{
		GetReadyFunc: func(_ context.Context, sessionID pgtype.UUID) ([]database.FignodeStagingTransaction, error) {
			return []database.FignodeStagingTransaction{
				{ID: makeUUID("00000000-0000-0000-0000-000000000001"), SessionID: sid, CashDirection: makeText("INFLOW"), PredictedCustomerID: cid, PredictedAccountID: aid, RawAmount: "100"},
			}, nil
		},
		GetSessionFunc: func(_ context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
			return database.GetCleanupSessionRow{BankAccountID: baid}, nil
		},
		GetAccountFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpAccount, error) {
			return database.ShadowErpAccount{ErpID: "acct-erp"}, nil
		},
		GetCustomerFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpCustomer, error) {
			return database.ShadowErpCustomer{ErpID: "cust-erp"}, nil
		},
		MarkFailedFunc: func(_ context.Context, p database.MarkStagingTransactionFailedParams) error {
			failedIDs = append(failedIDs, p.ID)
			return nil
		},
	}
	conn := &qboSyncMockConnector{
		BatchFunc: func(_ context.Context, realmID string, items []quickbooks.BatchItemRequest) ([]quickbooks.BatchItemResponse, error) {
			return nil, errors.New("network timeout")
		},
	}
	w := newQboTestWorker(store, conn)
	payload, _ := json.Marshal(map[string]string{"session_id": "550e8400-e29b-41d4-a716-446655440000"})
	msg := &nats.Msg{Subject: "worker.inbox.qbo_sync", Data: payload}
	err := w.Handle(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error to trigger NAK")
	}
	if len(failedIDs) != 1 {
		t.Errorf("expected 1 marked failed, got %d", len(failedIDs))
	}
}

func TestHandle_OAuthRevoked(t *testing.T) {
	sid := makeUUID("550e8400-e29b-41d4-a716-446655440000")
	baid := makeUUID("660e8400-e29b-41d4-a716-446655440001")
	cid := makeUUID("770e8400-e29b-41d4-a716-446655440002")
	aid := makeUUID("880e8400-e29b-41d4-a716-446655440003")

	store := &qboSyncMockStore{
		GetReadyFunc: func(_ context.Context, sessionID pgtype.UUID) ([]database.FignodeStagingTransaction, error) {
			return []database.FignodeStagingTransaction{
				{ID: makeUUID("00000000-0000-0000-0000-000000000001"), SessionID: sid, CashDirection: makeText("INFLOW"), PredictedCustomerID: cid, PredictedAccountID: aid, RawAmount: "100"},
			}, nil
		},
		GetSessionFunc: func(_ context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
			return database.GetCleanupSessionRow{BankAccountID: baid}, nil
		},
		GetAccountFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpAccount, error) {
			return database.ShadowErpAccount{ErpID: "acct-erp"}, nil
		},
		GetCustomerFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpCustomer, error) {
			return database.ShadowErpCustomer{ErpID: "cust-erp"}, nil
		},
		MarkFailedFunc: func(_ context.Context, p database.MarkStagingTransactionFailedParams) error { return nil },
	}
	conn := &qboSyncMockConnector{
		BatchFunc: func(_ context.Context, realmID string, items []quickbooks.BatchItemRequest) ([]quickbooks.BatchItemResponse, error) {
			return nil, fmt.Errorf("oauth2: invalid_grant token expired")
		},
	}
	w := newQboTestWorker(store, conn)
	payload, _ := json.Marshal(map[string]string{"session_id": "550e8400-e29b-41d4-a716-446655440000"})
	msg := &nats.Msg{Subject: "worker.inbox.qbo_sync", Data: payload}
	err := w.Handle(context.Background(), msg)
	if err != nil {
		t.Errorf("OAuth revoked should return nil (stop retries), got %v", err)
	}
}

func TestHandle_FullSuccess(t *testing.T) {
	sid := makeUUID("550e8400-e29b-41d4-a716-446655440000")
	baid := makeUUID("660e8400-e29b-41d4-a716-446655440001")
	vid := makeUUID("770e8400-e29b-41d4-a716-446655440002")
	cid := makeUUID("880e8400-e29b-41d4-a716-446655440003")
	aid := makeUUID("990e8400-e29b-41d4-a716-446655440004")

	var syncedCount int
	store := &qboSyncMockStore{
		GetReadyFunc: func(_ context.Context, sessionID pgtype.UUID) ([]database.FignodeStagingTransaction, error) {
			return []database.FignodeStagingTransaction{
				{ID: makeUUID("00000000-0000-0000-0000-000000000001"), SessionID: sid, CashDirection: makeText("INFLOW"), PredictedCustomerID: cid, PredictedAccountID: aid, RawAmount: "100"},
				{ID: makeUUID("00000000-0000-0000-0000-000000000002"), SessionID: sid, CashDirection: makeText("OUTFLOW"), PredictedVendorID: vid, PredictedAccountID: aid, RawAmount: "50"},
			}, nil
		},
		GetSessionFunc: func(_ context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
			return database.GetCleanupSessionRow{BankAccountID: baid}, nil
		},
		GetAccountFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpAccount, error) {
			return database.ShadowErpAccount{ErpID: "acct-erp"}, nil
		},
		GetVendorFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpVendor, error) {
			return database.ShadowErpVendor{ErpID: "vendor-erp"}, nil
		},
		GetCustomerFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpCustomer, error) {
			return database.ShadowErpCustomer{ErpID: "cust-erp"}, nil
		},
		MarkSyncedFunc: func(_ context.Context, p database.MarkStagingTransactionSyncedParams) error {
			syncedCount++
			return nil
		},
		MarkFailedFunc: func(_ context.Context, p database.MarkStagingTransactionFailedParams) error { return nil },
	}
	conn := &qboSyncMockConnector{
		BatchFunc: func(_ context.Context, realmID string, items []quickbooks.BatchItemRequest) ([]quickbooks.BatchItemResponse, error) {
			var responses []quickbooks.BatchItemResponse
			for _, item := range items {
				switch item.Entity {
				case "Deposit":
					responses = append(responses, quickbooks.BatchItemResponse{BId: item.BId, Deposit: &quickbooks.Deposit{Id: fmt.Sprintf("dep-%s", item.BId), SyncToken: "0"}})
				case "Purchase":
					responses = append(responses, quickbooks.BatchItemResponse{BId: item.BId, Purchase: &quickbooks.Purchase{Id: fmt.Sprintf("pur-%s", item.BId), SyncToken: "0"}})
				}
			}
			return responses, nil
		},
	}
	w := newQboTestWorker(store, conn)
	payload, _ := json.Marshal(map[string]string{"session_id": "550e8400-e29b-41d4-a716-446655440000"})
	msg := &nats.Msg{Subject: "worker.inbox.qbo_sync", Data: payload}
	err := w.Handle(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if syncedCount != 2 {
		t.Errorf("expected 2 synced, got %d", syncedCount)
	}
}

func TestHandle_PartialBatchFailure(t *testing.T) {
	sid := makeUUID("550e8400-e29b-41d4-a716-446655440000")
	baid := makeUUID("660e8400-e29b-41d4-a716-446655440001")
	cid := makeUUID("880e8400-e29b-41d4-a716-446655440003")
	aid := makeUUID("990e8400-e29b-41d4-a716-446655440004")

	var synced, failed int
	store := &qboSyncMockStore{
		GetReadyFunc: func(_ context.Context, sessionID pgtype.UUID) ([]database.FignodeStagingTransaction, error) {
			return []database.FignodeStagingTransaction{
				{ID: makeUUID("00000000-0000-0000-0000-000000000001"), SessionID: sid, CashDirection: makeText("INFLOW"), PredictedCustomerID: cid, PredictedAccountID: aid, RawAmount: "100"},
			}, nil
		},
		GetSessionFunc: func(_ context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
			return database.GetCleanupSessionRow{BankAccountID: baid}, nil
		},
		GetAccountFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpAccount, error) {
			return database.ShadowErpAccount{ErpID: "acct-erp"}, nil
		},
		GetCustomerFunc: func(_ context.Context, id pgtype.UUID) (database.ShadowErpCustomer, error) {
			return database.ShadowErpCustomer{ErpID: "cust-erp"}, nil
		},
		MarkSyncedFunc: func(_ context.Context, p database.MarkStagingTransactionSyncedParams) error { synced++; return nil },
		MarkFailedFunc: func(_ context.Context, p database.MarkStagingTransactionFailedParams) error { failed++; return nil },
	}
	conn := &qboSyncMockConnector{
		BatchFunc: func(_ context.Context, realmID string, items []quickbooks.BatchItemRequest) ([]quickbooks.BatchItemResponse, error) {
			return []quickbooks.BatchItemResponse{
				{BId: items[0].BId, Fault: &quickbooks.Fault{Error: []quickbooks.ErrorDetail{{Message: "locked period"}}}},
			}, nil
		},
	}
	w := newQboTestWorker(store, conn)
	payload, _ := json.Marshal(map[string]string{"session_id": "550e8400-e29b-41d4-a716-446655440000"})
	msg := &nats.Msg{Subject: "worker.inbox.qbo_sync", Data: payload}
	err := w.Handle(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if synced > 0 {
		t.Error("no items should be synced")
	}
	if failed != 1 {
		t.Errorf("expected 1 failed, got %d", failed)
	}
}
