package accounting

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp"
	"github.com/jackc/pgx/v5/pgtype"
)

func logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type mockTransactionRepository struct{}

func (m *mockTransactionRepository) GetOrCreateSystemSession(ctx context.Context, realmID pgtype.Text) (pgtype.UUID, error) {
	return pgtype.UUID{}, nil
}

func (m *mockTransactionRepository) GetProposedTransactionByValues(ctx context.Context, arg database.GetProposedTransactionByValuesParams) (database.FignodeStagingTransaction, error) {
	return database.FignodeStagingTransaction{}, errors.New("not found")
}
func (m *mockTransactionRepository) CreateProposedTransaction(ctx context.Context, arg database.CreateProposedTransactionParams) (database.FignodeStagingTransaction, error) {
	return database.FignodeStagingTransaction{}, nil
}
func (m *mockTransactionRepository) UpdateProposedTransactionSyncStatus(ctx context.Context, arg database.UpdateProposedTransactionSyncStatusParams) error {
	return nil
}
func (m *mockTransactionRepository) GetVendorByERPID(ctx context.Context, arg database.GetVendorByERPIDParams) (database.ShadowErpVendor, error) {
	return database.ShadowErpVendor{}, errors.New("not found")
}
func (m *mockTransactionRepository) GetAccountByERPID(ctx context.Context, arg database.GetAccountByERPIDParams) (database.ShadowErpAccount, error) {
	return database.ShadowErpAccount{}, errors.New("not found")
}
func (m *mockTransactionRepository) GetVendorByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpVendor, error) {
	return database.ShadowErpVendor{}, errors.New("not found")
}
func (m *mockTransactionRepository) GetAccountByID(ctx context.Context, id pgtype.UUID) (database.ShadowErpAccount, error) {
	return database.ShadowErpAccount{}, errors.New("not found")
}
func (m *mockTransactionRepository) GetUnifiedTransactions(ctx context.Context, realmID string) ([]database.GetUnifiedTransactionsRow, error) {
	return nil, nil
}

type mockProviderFactory struct {
	provider *erp.Provider
}

func (m *mockProviderFactory) GetProviderForTenant(ctx context.Context, tenantID string) (*erp.Provider, error) {
	return m.provider, nil
}
func (m *mockProviderFactory) GetProviderForRealm(ctx context.Context, erpSystem, realmID string) (*erp.Provider, error) {
	return m.provider, nil
}

func newSvc(factory erp.ProviderFactory) *TransactionService {
	return NewTransactionService(logger(), &mockTransactionRepository{}, nil, nil, factory, nil, nil, "")
}

func TestPostExpense_MissingRealmID(t *testing.T) {
	svc := newSvc(&mockProviderFactory{})
	_, err := svc.postExpense(context.Background(), erp.ExpenseInput{
		Description: "Lunch", Amount: 20, Paid: true, AccountHint: "acc-1",
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "realmID is required")
}

func TestPostExpense_Success(t *testing.T) {
	var gotInput erp.ExpenseInput
	factory := &mockProviderFactory{
		provider: &erp.Provider{
			PostExpense: func(ctx context.Context, input erp.ExpenseInput) (*erp.PostedExpense, error) {
				gotInput = input
				return &erp.PostedExpense{ERPEntityID: "123", EntityType: "Purchase"}, nil
			},
		},
	}
	svc := newSvc(factory)

	res, err := svc.PostExpense(context.Background(), erp.ExpenseInput{
		RealmID: "r1", Description: "Lunch", Amount: 20, Paid: true, AccountHint: "acc-1", VendorHint: "vnd-1",
	})
	require.NoError(t, err)

	// Since PostExpense is now async, it returns the generated ID from the local database
	// Mock returns the zero value for UUID
	assert.Equal(t, "00000000-0000-0000-0000-000000000000", res.ERPEntityID)

	// Ensure that we didn't actually call the ERP provider during sync PostExpense
	assert.Empty(t, gotInput.AccountHint)
}
