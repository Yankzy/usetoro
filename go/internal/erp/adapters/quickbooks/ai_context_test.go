package quickbooks

import (
	"context"
	"testing"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
)

// mockQuerier is a manual mock implementation of the database.Querier interface.
// We only implement the method we need for this test.
type mockQuerier struct {
	database.Querier
	getFilteredAccountsForAIFunc func(ctx context.Context, arg database.GetFilteredAccountsForAIParams) ([]database.GetFilteredAccountsForAIRow, error)
}

func (m *mockQuerier) GetFilteredAccountsForAI(ctx context.Context, arg database.GetFilteredAccountsForAIParams) ([]database.GetFilteredAccountsForAIRow, error) {
	if m.getFilteredAccountsForAIFunc != nil {
		return m.getFilteredAccountsForAIFunc(ctx, arg)
	}
	return nil, nil
}

func TestGetFilteredAccountsForAI(t *testing.T) {
	ctx := context.Background()
	realmID := "12345"
	macroClass := "Expense"
	accountType := "Bank"

	t.Run("successfully maps database rows to AccountPromptContext", func(t *testing.T) {
		mockRows := []database.GetFilteredAccountsForAIRow{
			{
				ErpID: "101",
				Name:  "Test Account 1",
				AccountSubType: pgtype.Text{
					String: "Checking",
					Valid:  true,
				},
			},
			{
				ErpID: "102",
				Name:  "Test Account 2",
				AccountSubType: pgtype.Text{
					Valid: false, // Simulate null
				},
			},
		}

		mockQ := &mockQuerier{
			getFilteredAccountsForAIFunc: func(ctx context.Context, arg database.GetFilteredAccountsForAIParams) ([]database.GetFilteredAccountsForAIRow, error) {
				assert.Equal(t, realmID, arg.RealmID)
				assert.Equal(t, macroClass, arg.Classification.String)
				assert.Equal(t, accountType, arg.AccountType)
				return mockRows, nil
			},
		}

		results, err := GetFilteredAccountsForAI(mockQ, ctx, realmID, macroClass, accountType)

		assert.NoError(t, err)
		assert.Len(t, results, 2)
		
		assert.Equal(t, "101", results[0].AccountID)
		assert.Equal(t, "Test Account 1", results[0].Name)
		assert.Equal(t, "Checking", results[0].AccountSubType)

		assert.Equal(t, "102", results[1].AccountID)
		assert.Equal(t, "Test Account 2", results[1].Name)
		assert.Equal(t, "", results[1].AccountSubType)
	})

	t.Run("handles database error", func(t *testing.T) {
		mockQ := &mockQuerier{
			getFilteredAccountsForAIFunc: func(ctx context.Context, arg database.GetFilteredAccountsForAIParams) ([]database.GetFilteredAccountsForAIRow, error) {
				return nil, assert.AnError
			},
		}

		results, err := GetFilteredAccountsForAI(mockQ, ctx, realmID, macroClass, accountType)

		assert.Error(t, err)
		assert.Nil(t, results)
		assert.Equal(t, assert.AnError, err)
	})
}
