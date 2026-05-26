package quickbooks

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Yankzy/usetoro/internal/database"
	sdk "github.com/Yankzy/usetoro/internal/erp/adapters/quickbooks/sdk"
)

var testLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

// --- Pure function tests ---

func TestParseV2Amount(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
		wantErr  bool
	}{
		{"150.00", 150.00, false},
		{"-250.50", -250.50, false},
		{"1,234.56", 1234.56, false},
		{"", 0, true},
		{"  -99.99  ", -99.99, false},
	}

	for _, tt := range tests {
		result, err := parseV2Amount(tt.input)
		if tt.wantErr && err == nil {
			t.Errorf("parseV2Amount(%q): expected error, got %f", tt.input, result)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("parseV2Amount(%q): unexpected error: %v", tt.input, err)
		}
		if !tt.wantErr && math.Abs(result-tt.expected) > 0.001 {
			t.Errorf("parseV2Amount(%q): expected %f, got %f", tt.input, tt.expected, result)
		}
	}
}

func TestFormatV2LineDescription(t *testing.T) {
	tests := []struct {
		input    pgtype.Text
		expected string
	}{
		{pgtype.Text{String: "Test reasoning", Valid: true}, "AI Reasoning: Test reasoning"},
		{pgtype.Text{String: "", Valid: true}, ""},
		{pgtype.Text{Valid: false}, ""},
	}

	for _, tt := range tests {
		result := formatV2LineDescription(tt.input)
		if result != tt.expected {
			t.Errorf("formatV2LineDescription(%v): expected %q, got %q", tt.input, tt.expected, result)
		}
	}
}

func TestConstructV2Purchase(t *testing.T) {
	p := constructV2Purchase(
		150.00,
		sdk.Date{},
		"payment-123",
		"vendor-456",
		"account-789",
		pgtype.Text{String: "Office supplies", Valid: true},
		"EXPENSE",
		false,
		false,
	)

	if p.PaymentType != "Cash" {
		t.Errorf("expected Cash, got %s", p.PaymentType)
	}
	if p.Credit {
		t.Error("expected Credit=false")
	}
	if p.AccountRef.Value != "payment-123" {
		t.Errorf("expected payment-123, got %s", p.AccountRef.Value)
	}
	if p.EntityRef.Value != "vendor-456" {
		t.Errorf("expected vendor-456, got %s", p.EntityRef.Value)
	}
	expectedAmt := strconv.FormatFloat(150.00, 'f', 2, 64)
	if p.TotalAmt.String() != expectedAmt {
		t.Errorf("expected %s, got %s", expectedAmt, p.TotalAmt.String())
	}
	if len(p.Line) != 1 {
		t.Fatalf("expected 1 line, got %d", len(p.Line))
	}
	if p.Line[0].AccountBasedExpenseLineDetail.AccountRef.Value != "account-789" {
		t.Errorf("expected account-789 in line, got %s", p.Line[0].AccountBasedExpenseLineDetail.AccountRef.Value)
	}
}

func TestConstructV2Purchase_CreditCard(t *testing.T) {
	p := constructV2Purchase(
		200.00,
		sdk.Date{},
		"payment-123",
		"vendor-456",
		"account-789",
		pgtype.Text{},
		"EXPENSE",
		true,
		false,
	)

	if p.PaymentType != "CreditCard" {
		t.Errorf("expected CreditCard, got %s", p.PaymentType)
	}
}

func TestConstructV2Deposit(t *testing.T) {
	d := constructV2Deposit(
		500.00,
		sdk.Date{},
		"payment-123",
		"customer-456",
		"account-789",
		pgtype.Text{String: "Consulting revenue", Valid: true},
		"REVENUE",
	)

	if d.DepositToAccountRef == nil {
		t.Fatal("expected non-nil DepositToAccountRef")
	}
	if d.DepositToAccountRef.Value != "payment-123" {
		t.Errorf("expected payment-123, got %s", d.DepositToAccountRef.Value)
	}
	expectedAmt := strconv.FormatFloat(500.00, 'f', 2, 64)
	if d.TotalAmt.String() != expectedAmt {
		t.Errorf("expected %s, got %s", expectedAmt, d.TotalAmt.String())
	}
	if len(d.Line) != 1 {
		t.Fatalf("expected 1 line, got %d", len(d.Line))
	}
	if d.Line[0].DepositLineDetail == nil {
		t.Fatal("expected non-nil DepositLineDetail")
	}
	if d.Line[0].DepositLineDetail.Entity.Value != "customer-456" {
		t.Errorf("expected customer-456, got %s", d.Line[0].DepositLineDetail.Entity.Value)
	}
}

func TestConstructV2Transfer(t *testing.T) {
	tr := constructV2Transfer(
		1000.00,
		sdk.Date{},
		"from-account-123",
		"to-account-456",
		pgtype.Text{String: "Internal transfer", Valid: true},
	)

	expectedAmt := strconv.FormatFloat(1000.00, 'f', 2, 64)
	if tr.Amount.String() != expectedAmt {
		t.Errorf("expected %s, got %s", expectedAmt, tr.Amount.String())
	}
	if tr.FromAccountRef.Value != "from-account-123" {
		t.Errorf("expected from-account-123, got %s", tr.FromAccountRef.Value)
	}
	if tr.ToAccountRef.Value != "to-account-456" {
		t.Errorf("expected to-account-456, got %s", tr.ToAccountRef.Value)
	}
	if tr.PrivateNote == "" {
		t.Error("expected non-empty PrivateNote")
	}
}

// --- Mock types for pipeline tests ---

type mockV2Store struct {
	getCleanupSessionFn         func(ctx context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error)
	getStagingTransactionsFn    func(ctx context.Context, sessionID pgtype.UUID) ([]database.FignodeStagingTransaction, error)
	getAccountByIDFn            func(ctx context.Context, id pgtype.UUID) (database.ShadowErpAccount, error)
	getVendorByIDFn             func(ctx context.Context, id pgtype.UUID) (database.ShadowErpVendor, error)
	getCustomerByIDFn           func(ctx context.Context, id pgtype.UUID) (database.ShadowErpCustomer, error)
	getAccountsByRealmFn        func(ctx context.Context, realmID string) ([]database.ShadowErpAccount, error)
}

func (m *mockV2Store) GetCleanupSession(ctx context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
	if m.getCleanupSessionFn != nil {
		return m.getCleanupSessionFn(ctx, id)
	}
	return database.GetCleanupSessionRow{}, nil
}

func (m *mockV2Store) GetStagingTransactionsReadyForQBO(ctx context.Context, sessionID pgtype.UUID) ([]database.FignodeStagingTransaction, error) {
	if m.getStagingTransactionsFn != nil {
		return m.getStagingTransactionsFn(ctx, sessionID)
	}
	return nil, nil
}

func (m *mockV2Store) GetAccountByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpAccount, error) {
	if m.getAccountByIDFn != nil {
		return m.getAccountByIDFn(ctx, id)
	}
	return database.ShadowErpAccount{}, nil
}

func (m *mockV2Store) GetVendorByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpVendor, error) {
	if m.getVendorByIDFn != nil {
		return m.getVendorByIDFn(ctx, id)
	}
	return database.ShadowErpVendor{}, nil
}

func (m *mockV2Store) GetCustomerByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpCustomer, error) {
	if m.getCustomerByIDFn != nil {
		return m.getCustomerByIDFn(ctx, id)
	}
	return database.ShadowErpCustomer{}, nil
}

func (m *mockV2Store) GetAccountsByRealm(ctx context.Context, realmID string) ([]database.ShadowErpAccount, error) {
	if m.getAccountsByRealmFn != nil {
		return m.getAccountsByRealmFn(ctx, realmID)
	}
	return nil, nil
}

type mockV2Connector struct {
	batchCreateFn func(ctx context.Context, realmID string, items []sdk.BatchItemRequest) ([]sdk.BatchItemResponse, error)
}

func (m *mockV2Connector) BatchCreateStagingTransactions(ctx context.Context, realmID string, items []sdk.BatchItemRequest) ([]sdk.BatchItemResponse, error) {
	if m.batchCreateFn != nil {
		return m.batchCreateFn(ctx, realmID, items)
	}
	return nil, nil
}

type mockLLMForOrch struct {
	generateJSONFn func(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error
}

func (m *mockLLMForOrch) GenerateJSON(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error {
	if m.generateJSONFn != nil {
		return m.generateJSONFn(ctx, systemPrompt, userPrompt, output)
	}
	return nil
}

// --- Pipeline tests ---

func TestOrchestrator_EmptyRows(t *testing.T) {
	store := &mockV2Store{
		getCleanupSessionFn: func(ctx context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
			return database.GetCleanupSessionRow{
				RealmID: pgtype.Text{String: "realm-1", Valid: true},
			}, nil
		},
		getStagingTransactionsFn: func(ctx context.Context, sessionID pgtype.UUID) ([]database.FignodeStagingTransaction, error) {
			return nil, nil
		},
	}

	conn := &mockV2Connector{}
	llm := &mockLLMForOrch{}

	orch := NewV2Orchestrator(testLogger, store, nil, conn, llm)

	var sessionID pgtype.UUID
	sessionID.Scan("00000000-0000-0000-0000-000000000001")

	err := orch.Run(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOrchestrator_MissingBankAccount(t *testing.T) {
	store := &mockV2Store{
		getCleanupSessionFn: func(ctx context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
			return database.GetCleanupSessionRow{
				RealmID:       pgtype.Text{String: "realm-1", Valid: true},
				BankAccountID: pgtype.UUID{Valid: false},
			}, nil
		},
		getStagingTransactionsFn: func(ctx context.Context, sessionID pgtype.UUID) ([]database.FignodeStagingTransaction, error) {
			return []database.FignodeStagingTransaction{
				{ID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}},
			}, nil
		},
	}

	orch := NewV2Orchestrator(testLogger, store, nil, nil, &mockLLMForOrch{})

	var sessionID pgtype.UUID
	sessionID.Scan("00000000-0000-0000-0000-000000000001")

	err := orch.Run(context.Background(), sessionID)
	if err == nil {
		t.Fatal("expected error for missing bank account")
	}
}

func TestOrchestrator_BankAccountWithoutErpID(t *testing.T) {
	store := &mockV2Store{
		getCleanupSessionFn: func(ctx context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
			return database.GetCleanupSessionRow{
				RealmID:       pgtype.Text{String: "realm-1", Valid: true},
				BankAccountID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
			}, nil
		},
		getStagingTransactionsFn: func(ctx context.Context, sessionID pgtype.UUID) ([]database.FignodeStagingTransaction, error) {
			return []database.FignodeStagingTransaction{
				{ID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}},
			}, nil
		},
		getAccountByIDFn: func(ctx context.Context, id pgtype.UUID) (database.ShadowErpAccount, error) {
			return database.ShadowErpAccount{Name: "Test Account", ErpID: ""}, nil
		},
	}

	orch := NewV2Orchestrator(testLogger, store, nil, nil, &mockLLMForOrch{})

	var sessionID pgtype.UUID
	sessionID.Scan("00000000-0000-0000-0000-000000000001")

	err := orch.Run(context.Background(), sessionID)
	if err == nil {
		t.Fatal("expected error for account without ERP ID")
	}
}

func TestOrchestrator_SessionLookupFailure(t *testing.T) {
	store := &mockV2Store{
		getCleanupSessionFn: func(ctx context.Context, id pgtype.UUID) (database.GetCleanupSessionRow, error) {
			return database.GetCleanupSessionRow{}, errors.New("session not found")
		},
	}

	orch := NewV2Orchestrator(testLogger, store, nil, nil, &mockLLMForOrch{})

	var sessionID pgtype.UUID
	sessionID.Scan("00000000-0000-0000-0000-000000000001")

	err := orch.Run(context.Background(), sessionID)
	if err == nil {
		t.Fatal("expected error for session lookup failure")
	}
}

func TestResolveEntityERPID_Vendor(t *testing.T) {
	store := &mockV2Store{
		getVendorByIDFn: func(ctx context.Context, id pgtype.UUID) (database.ShadowErpVendor, error) {
			return database.ShadowErpVendor{
				ErpID:       "qbo-vendor-123",
				DisplayName: "Test Vendor",
			}, nil
		},
	}

	orch := NewV2Orchestrator(testLogger, store, nil, nil, nil)

	row := database.FignodeStagingTransaction{
		PredictedVendorID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
	}

	erpID, err := orch.resolveEntityERPID(context.Background(), row)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if erpID != "qbo-vendor-123" {
		t.Errorf("expected qbo-vendor-123, got %s", erpID)
	}
}

func TestResolveEntityERPID_VendorWithoutErpID(t *testing.T) {
	store := &mockV2Store{
		getVendorByIDFn: func(ctx context.Context, id pgtype.UUID) (database.ShadowErpVendor, error) {
			return database.ShadowErpVendor{
				ErpID:       "",
				DisplayName: "Test Vendor",
			}, nil
		},
	}

	orch := NewV2Orchestrator(testLogger, store, nil, nil, nil)

	row := database.FignodeStagingTransaction{
		PredictedVendorID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
	}

	_, err := orch.resolveEntityERPID(context.Background(), row)
	if err == nil {
		t.Fatal("expected error for vendor without ERP ID")
	}
}

func TestResolveEntityERPID_NoEntity(t *testing.T) {
	store := &mockV2Store{}

	orch := NewV2Orchestrator(testLogger, store, nil, nil, nil)

	row := database.FignodeStagingTransaction{}

	_, err := orch.resolveEntityERPID(context.Background(), row)
	if err == nil {
		t.Fatal("expected error for row with no entity")
	}
}

func TestResolveAccountERPID_Override(t *testing.T) {
	store := &mockV2Store{
		getAccountByIDFn: func(ctx context.Context, id pgtype.UUID) (database.ShadowErpAccount, error) {
			return database.ShadowErpAccount{
				ErpID: "qbo-acct-override",
				Name:  "Override Account",
			}, nil
		},
	}

	orch := NewV2Orchestrator(testLogger, store, nil, nil, nil)

	row := database.FignodeStagingTransaction{
		PredictedAccountID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
		OverrideAccountID:  pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
	}

	acct, err := orch.resolveAccountERPID(context.Background(), row)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if acct.ErpID != "qbo-acct-override" {
		t.Errorf("expected qbo-acct-override, got %s", acct.ErpID)
	}
}
