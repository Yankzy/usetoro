package accounting

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Yankzy/usetoro/internal/database"
	quickbooks "github.com/Yankzy/usetoro/qbo"
	"github.com/jackc/pgx/v5/pgtype"
)

// ─── Mock implementations ───────────────────────────────────────────────────

// mockPurchaseCreator satisfies purchaseCreator.
type mockPurchaseCreator struct {
	result *quickbooks.Purchase
	err    error
	called bool
	got    *quickbooks.Purchase
}

func (m *mockPurchaseCreator) CreatePurchase(p *quickbooks.Purchase) (*quickbooks.Purchase, error) {
	m.called = true
	m.got = p
	if m.err != nil {
		return nil, m.err
	}
	if m.result != nil {
		return m.result, nil
	}
	return &quickbooks.Purchase{Id: "purchase-default"}, nil
}

// mockBillCreator satisfies billCreator.
type mockBillCreator struct {
	result *quickbooks.Bill
	err    error
	called bool
	got    *quickbooks.Bill
}

func (m *mockBillCreator) CreateBill(b *quickbooks.Bill) (*quickbooks.Bill, error) {
	m.called = true
	m.got = b
	if m.err != nil {
		return nil, m.err
	}
	if m.result != nil {
		return m.result, nil
	}
	return &quickbooks.Bill{Id: "bill-default"}, nil
}

// failClientFn always returns an error from the QBO client resolver.
func failClientFn(msg string) QBOClientFn {
	return func(ctx context.Context, realmID string) (*quickbooks.Client, error) {
		return nil, errors.New(msg)
	}
}

// logger returns a discard logger to keep test output clean.
func logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mockTransactionRepository satisfies TransactionRepository for testing
type mockTransactionRepository struct{}

func (m *mockTransactionRepository) GetProposedTransactionByValues(ctx context.Context, arg database.GetProposedTransactionByValuesParams) (database.ShadowErpProposedTransaction, error) {
	return database.ShadowErpProposedTransaction{}, errors.New("not found")
}

func (m *mockTransactionRepository) CreateProposedTransaction(ctx context.Context, arg database.CreateProposedTransactionParams) (database.ShadowErpProposedTransaction, error) {
	return database.ShadowErpProposedTransaction{}, nil
}

func (m *mockTransactionRepository) UpdateProposedTransactionSyncStatus(ctx context.Context, arg database.UpdateProposedTransactionSyncStatusParams) error {
	return nil
}

func (m *mockTransactionRepository) GetVendorByQBOID(ctx context.Context, arg database.GetVendorByQBOIDParams) (database.ShadowErpVendor, error) {
	return database.ShadowErpVendor{}, errors.New("not found")
}

func (m *mockTransactionRepository) GetAccountByQBOID(ctx context.Context, arg database.GetAccountByQBOIDParams) (database.ShadowErpAccount, error) {
	return database.ShadowErpAccount{}, errors.New("not found")
}

func (m *mockTransactionRepository) GetVendorByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpVendor, error) {
	return database.ShadowErpVendor{}, errors.New("not found")
}

func (m *mockTransactionRepository) GetAccountByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpAccount, error) {
	return database.ShadowErpAccount{}, errors.New("not found")
}

// newSvc is a convenience constructor for TransactionService with no AI infra.
func newSvc(clientFn QBOClientFn) *TransactionService {
	return NewTransactionService(logger(), &mockTransactionRepository{}, nil, nil, clientFn, nil)
}

// callWithMocks calls the internal postExpense with the given mocks.
func callWithMocks(
	svc *TransactionService,
	input ExpenseInput,
	pc purchaseCreator,
	bc billCreator,
) (*PostedExpense, error) {
	return svc.postExpense(context.Background(), input, pc, bc)
}

// ─── Constructor ────────────────────────────────────────────────────────────

func TestNewTransactionService_IsNonNil(t *testing.T) {
	svc := NewTransactionService(logger(), &mockTransactionRepository{}, nil, nil, failClientFn("unused"), nil)
	assert.NotNil(t, svc)
}

// ─── Validation ─────────────────────────────────────────────────────────────

func TestPostExpense_MissingRealmID(t *testing.T) {
	svc := newSvc(failClientFn("should not be called"))
	_, err := callWithMocks(svc, ExpenseInput{
		Description: "Lunch",
		Amount:      20,
		Paid:        true,
		AccountHint: "acc-1",
	}, &mockPurchaseCreator{}, &mockBillCreator{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "realmID is required")
}

func TestPostExpense_NoDescriptionAndNoAccountHint(t *testing.T) {
	svc := newSvc(failClientFn("should not be called"))
	_, err := callWithMocks(svc, ExpenseInput{
		RealmID: "r1",
		Amount:  20,
		Paid:    true,
	}, &mockPurchaseCreator{}, &mockBillCreator{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "description or AccountHint is required")
}

func TestPostExpense_MissingAccountHintAndNoCoAMapper(t *testing.T) {
	// No AI infra (coaMapper=nil) and no AccountHint → "account is required"
	svc := newSvc(failClientFn("should not be called"))
	_, err := callWithMocks(svc, ExpenseInput{
		RealmID:     "r1",
		Description: "Hotel", // description present but no mapper to resolve it
		Amount:      200,
		Paid:        true,
	}, &mockPurchaseCreator{}, &mockBillCreator{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account is required but could not be resolved")
}

func TestPostExpense_BillMissingVendorHintAndNoResolver(t *testing.T) {
	svc := newSvc(failClientFn("should not be called"))
	_, err := callWithMocks(svc, ExpenseInput{
		RealmID:     "r1",
		Description: "Electricity",
		Amount:      150,
		Paid:        false,
		AccountHint: "acc-utilities",
		// VendorHint omitted, entityResolver nil
	}, &mockPurchaseCreator{}, &mockBillCreator{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vendor required for Bill")
}

// ─── Purchase path ──────────────────────────────────────────────────────────

func TestPostExpense_PaidCreatesPurchase(t *testing.T) {
	pc := &mockPurchaseCreator{result: &quickbooks.Purchase{Id: "p-001"}}
	bc := &mockBillCreator{}
	svc := newSvc(failClientFn("should not be called"))

	result, err := callWithMocks(svc, ExpenseInput{
		RealmID:     "realm-1",
		Description: "Coffee",
		Amount:      5.00,
		Paid:        true,
		AccountHint: "acc-meals",
		VendorHint:  "vendor-cafe",
	}, pc, bc)

	require.NoError(t, err)
	assert.True(t, pc.called)
	assert.False(t, bc.called)
	assert.Equal(t, "Purchase", result.EntityType)
	assert.Equal(t, "p-001", result.QBOEntityID)
	assert.Equal(t, "acc-meals", result.AccountQBOID)
	assert.Equal(t, "vendor-cafe", result.VendorQBOID)
	assert.InDelta(t, 5.00, result.Amount, 1e-9)
}

func TestPostExpense_PurchaseHasCorrectPaymentTypeAndRefs(t *testing.T) {
	pc := &mockPurchaseCreator{}
	svc := newSvc(failClientFn("unused"))

	_, err := callWithMocks(svc, ExpenseInput{
		RealmID:     "r1",
		Amount:      99.99,
		Paid:        true,
		AccountHint: "acc-travel",
		VendorHint:  "vendor-delta",
	}, pc, &mockBillCreator{})

	require.NoError(t, err)
	require.NotNil(t, pc.got)
	assert.Equal(t, "Cash", pc.got.PaymentType)
	assert.Equal(t, "acc-travel", pc.got.AccountRef.Value)
	assert.Equal(t, "vendor-delta", pc.got.EntityRef.Value)
	assert.Equal(t, "Vendor", pc.got.EntityRef.Type)
	assert.Equal(t, "AccountBasedExpenseLineDetail", pc.got.Line[0].DetailType)
	assert.Equal(t, "acc-travel", pc.got.Line[0].AccountBasedExpenseLineDetail.AccountRef.Value)
}

func TestPostExpense_PurchaseAmountFormattedCorrectly(t *testing.T) {
	pc := &mockPurchaseCreator{}
	svc := newSvc(failClientFn("unused"))

	_, err := callWithMocks(svc, ExpenseInput{
		RealmID:     "r1",
		Amount:      1234.567, // will be truncated to 2 decimal places
		Paid:        true,
		AccountHint: "a1",
	}, pc, &mockBillCreator{})

	require.NoError(t, err)
	assert.Equal(t, "1234.57", pc.got.Line[0].Amount.String())
}

func TestPostExpense_PurchaseWithNoVendorHintLeaveEntityRefEmpty(t *testing.T) {
	pc := &mockPurchaseCreator{}
	svc := newSvc(failClientFn("unused"))

	_, err := callWithMocks(svc, ExpenseInput{
		RealmID:     "r1",
		Amount:      10,
		Paid:        true,
		AccountHint: "a1",
		// VendorHint deliberately empty
	}, pc, &mockBillCreator{})

	require.NoError(t, err)
	assert.Empty(t, pc.got.EntityRef.Value, "EntityRef should not be set when no vendor")
}

func TestPostExpense_ExplicitTxnDateIsPreserved(t *testing.T) {
	txnDate := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	pc := &mockPurchaseCreator{}
	svc := newSvc(failClientFn("unused"))

	_, err := callWithMocks(svc, ExpenseInput{
		RealmID:     "r1",
		Amount:      10,
		Paid:        true,
		AccountHint: "a1",
		TxnDate:     txnDate,
	}, pc, &mockBillCreator{})

	require.NoError(t, err)
	assert.Equal(t, txnDate, pc.got.TxnDate.Time)
}

func TestPostExpense_ZeroTxnDateDefaultsToToday(t *testing.T) {
	pc := &mockPurchaseCreator{}
	before := time.Now().Truncate(time.Second)
	svc := newSvc(failClientFn("unused"))

	_, err := callWithMocks(svc, ExpenseInput{
		RealmID:     "r1",
		Amount:      10,
		Paid:        true,
		AccountHint: "a1",
		// TxnDate zero
	}, pc, &mockBillCreator{})

	after := time.Now().Add(time.Second)
	require.NoError(t, err)
	assert.True(t, !pc.got.TxnDate.Time.Before(before) && !pc.got.TxnDate.Time.After(after),
		"TxnDate should be approximately now")
}

func TestPostExpense_DescriptionSetAsPrivateNote(t *testing.T) {
	pc := &mockPurchaseCreator{}
	svc := newSvc(failClientFn("unused"))

	_, err := callWithMocks(svc, ExpenseInput{
		RealmID:     "r1",
		Description: "Team lunch at Nobu",
		Amount:      250,
		Paid:        true,
		AccountHint: "a1",
	}, pc, &mockBillCreator{})

	require.NoError(t, err)
	assert.Equal(t, "Team lunch at Nobu", pc.got.PrivateNote)
	assert.Equal(t, "Team lunch at Nobu", pc.got.Line[0].Description)
}

func TestPostExpense_CreatePurchaseErrorPropagates(t *testing.T) {
	pc := &mockPurchaseCreator{err: errors.New("QBO 429 rate limit")}
	svc := newSvc(failClientFn("unused"))

	_, err := callWithMocks(svc, ExpenseInput{
		RealmID: "r1", Amount: 10, Paid: true, AccountHint: "a1",
	}, pc, &mockBillCreator{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create Purchase")
	assert.Contains(t, err.Error(), "QBO 429 rate limit")
}

// ─── Bill path ───────────────────────────────────────────────────────────────

func TestPostExpense_UnpaidCreatesBill(t *testing.T) {
	bc := &mockBillCreator{result: &quickbooks.Bill{Id: "b-999"}}
	pc := &mockPurchaseCreator{}
	svc := newSvc(failClientFn("unused"))

	result, err := callWithMocks(svc, ExpenseInput{
		RealmID:     "realm-x",
		Description: "Rent",
		Amount:      3000.00,
		Paid:        false,
		AccountHint: "acc-rent",
		VendorHint:  "vendor-landlord",
	}, pc, bc)

	require.NoError(t, err)
	assert.True(t, bc.called)
	assert.False(t, pc.called)
	assert.Equal(t, "Bill", result.EntityType)
	assert.Equal(t, "b-999", result.QBOEntityID)
	assert.Equal(t, "vendor-landlord", result.VendorQBOID)
}

func TestPostExpense_BillHasCorrectVendorRefAndAccountRef(t *testing.T) {
	bc := &mockBillCreator{}
	svc := newSvc(failClientFn("unused"))

	_, err := callWithMocks(svc, ExpenseInput{
		RealmID: "r1", Amount: 500, Paid: false,
		AccountHint: "acc-utilities", VendorHint: "vendor-electric",
	}, &mockPurchaseCreator{}, bc)

	require.NoError(t, err)
	require.NotNil(t, bc.got)
	assert.Equal(t, "vendor-electric", bc.got.VendorRef.Value)
	assert.Equal(t, "acc-utilities", bc.got.Line[0].AccountBasedExpenseLineDetail.AccountRef.Value)
	assert.Equal(t, "AccountBasedExpenseLineDetail", bc.got.Line[0].DetailType)
}

func TestPostExpense_CreateBillErrorPropagates(t *testing.T) {
	bc := &mockBillCreator{err: errors.New("QBO validation error")}
	svc := newSvc(failClientFn("unused"))

	_, err := callWithMocks(svc, ExpenseInput{
		RealmID: "r1", Amount: 50, Paid: false,
		AccountHint: "a1", VendorHint: "v1",
	}, &mockPurchaseCreator{}, bc)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create Bill")
	assert.Contains(t, err.Error(), "QBO validation error")
}

// ─── clientFn error path (real PostExpense, no overrides) ───────────────────

func TestPostExpense_ClientFnErrorPropagates(t *testing.T) {
	svc := newSvc(failClientFn("auth token expired"))

	// Call the PUBLIC PostExpense — this hits clientFn which returns error
	_, err := svc.PostExpense(context.Background(), ExpenseInput{
		RealmID:     "r1",
		Amount:      100,
		Paid:        true,
		AccountHint: "acc-1",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get QBO client")
	assert.Contains(t, err.Error(), "auth token expired")
}

// ─── AccountHint bypasses CoAMapper ─────────────────────────────────────────

func TestPostExpense_AccountHintBypassesMapper(t *testing.T) {
	// Even if coaMapper were present, AccountHint should skip it.
	// Here we confirm no panic occurs with nil mapper when hint is given.
	pc := &mockPurchaseCreator{}
	svc := NewTransactionService(logger(), &mockTransactionRepository{}, nil, nil, failClientFn("unused"), nil)

	_, err := callWithMocks(svc, ExpenseInput{
		RealmID:     "r1",
		AccountHint: "acc-forced",
		Amount:      40,
		Paid:        true,
	}, pc, &mockBillCreator{})

	require.NoError(t, err)
	assert.Equal(t, "acc-forced", pc.got.AccountRef.Value)
}

// ─── PostedExpense fields ────────────────────────────────────────────────────

func TestPostExpense_ResultContainsCorrectFields(t *testing.T) {
	pc := &mockPurchaseCreator{result: &quickbooks.Purchase{Id: "p-xyz"}}
	svc := newSvc(failClientFn("unused"))

	result, err := callWithMocks(svc, ExpenseInput{
		RealmID:     "r1",
		Amount:      77.77,
		Paid:        true,
		AccountHint: "acc-A",
		VendorHint:  "vnd-B",
	}, pc, &mockBillCreator{})

	require.NoError(t, err)
	assert.Equal(t, "p-xyz", result.QBOEntityID)
	assert.Equal(t, "Purchase", result.EntityType)
	assert.Equal(t, "acc-A", result.AccountQBOID)
	assert.Equal(t, "vnd-B", result.VendorQBOID)
	assert.InDelta(t, 77.77, result.Amount, 1e-9)
}

// ─── fmt.Sprintf amount coverage ────────────────────────────────────────────

func TestPostExpense_AmountFormattingEdgeCases(t *testing.T) {
	cases := []struct {
		amount   float64
		expected string
	}{
		{0, "0.00"},
		{0.001, "0.00"},
		{0.005, "0.01"}, // IEEE 754: sprintf("%.2f", 0.005) → "0.01"
		{0.006, "0.01"},
		{100, "100.00"},
		{9999.99, "9999.99"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("amount=%.3f", tc.amount), func(t *testing.T) {
			pc := &mockPurchaseCreator{}
			svc := newSvc(failClientFn("unused"))
			_, err := callWithMocks(svc, ExpenseInput{
				RealmID: "r1", Amount: tc.amount, Paid: true, AccountHint: "a1",
			}, pc, &mockBillCreator{})
			require.NoError(t, err)
			assert.Equal(t, tc.expected, pc.got.Line[0].Amount.String())
		})
	}
}
