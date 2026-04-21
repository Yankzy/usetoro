package quickbooks

import (
	"context"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
)

// AccountPromptContext represents a subset of QuickBooks account data 
// used as context for the AI to make a final account selection.
type AccountPromptContext struct {
	AccountID      string `json:"account_id"` // Maps to erp_id
	Name           string `json:"name"`
	AccountSubType string `json:"account_sub_type,omitempty"`
}

// GetFilteredAccountsForAI queries the local shadow_erp.accounts table to get 
// a filtered subset of available QuickBooks accounts for a specific realm.
// This is used by the AI Agent to make a final account selection.
func GetFilteredAccountsForAI(
	q database.Querier,
	ctx context.Context,
	realmID string,
	macroClass string,
	accountType string,
) ([]AccountPromptContext, error) {
	rows, err := q.GetFilteredAccountsForAI(ctx, database.GetFilteredAccountsForAIParams{
		RealmID:        realmID,
		Classification: pgtype.Text{String: macroClass, Valid: macroClass != ""},
		AccountType:    accountType,
	})
	if err != nil {
		return nil, err
	}

	results := make([]AccountPromptContext, len(rows))
	for i, row := range rows {
		results[i] = AccountPromptContext{
			AccountID:      row.ErpID,
			Name:           row.Name,
			AccountSubType: row.AccountSubType.String,
		}
	}

	return results, nil
}
