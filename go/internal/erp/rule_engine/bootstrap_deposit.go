package ruleEngine

import (
	"context"
	"fmt"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
)

// RunRuleEngineForDeposits orchestrates the rule generation for incoming deposits.
// It is called from RunRuleEngine after the Purchase pipeline completes.
func (b *Bootstrapper) RunRuleEngineForDeposits(ctx context.Context, realmID string) error {
	b.logger.Info("Starting Deposit rule bootstrapping", "realm_id", realmID)

	// Legacy deposit-based bootstrapping (direct DepositLineDetail with Entity)
	flaggedCustomers, err := b.GenerateDepositSplitReviewRules(ctx, realmID)
	if err != nil {
		return fmt.Errorf("failed generating deposit split rules: %w", err)
	}

	if err := b.GenerateDepositRulesFromHistory(ctx, realmID, flaggedCustomers); err != nil {
		return fmt.Errorf("failed generating 1-to-1 deposit rules: %w", err)
	}

	b.logger.Info("Successfully completed Deposit rule bootstrapping", "realm_id", realmID)
	return nil
}

// GenerateDepositSplitReviewRules flags complex customers (e.g., Stripe payouts
// that mix income with processing fees) for manual CPA review.
func (b *Bootstrapper) GenerateDepositSplitReviewRules(ctx context.Context, realmID string) (map[string]bool, error) {
	rows, err := b.q.GetHistoricalDepositSplitters(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("GetHistoricalDepositSplitters: %w", err)
	}

	flaggedCustomers := make(map[string]bool)

	for _, row := range rows {
		customerErpID := row.CustomerID
		if customerErpID == "" {
			continue
		}

		customer, err := b.q.GetCustomerByERPID(ctx, database.GetCustomerByERPIDParams{
			RealmID: realmID,
			ErpID:   customerErpID,
		})
		if err != nil || customer.DisplayName == "" {
			b.logger.Warn("DepositSplitter: customer not found, skipping",
				"erp_id", customerErpID, "error", err)
			continue
		}

		var entityUUID pgtype.UUID
		_ = entityUUID.Scan(customer.ID)

		if err := b.ruleCreator.CreateRule(ctx, CreateRuleRequest{
			RealmID:        realmID,
			Name:           fmt.Sprintf("Auto-Generated Income Split: %s", customer.DisplayName),
			Logic:          LogicAnd,
			Priority:       5,
			Active:         true,
			Direction:      Inflow, // 🚨 Strict boundary for Deposit rules
			TargetEntityID: entityUUID,
			Allocations:    []Allocation{},
			RequiresReview: true,
			Conditions: []RuleConditionRequest{
				{
					Field:    FieldCustomer,
					Operator: OpContainsCS,
					Value:    customer.DisplayName,
				},
			},
		}); err != nil {
			b.logger.Error("DepositSplitter: failed to create rule",
				"customer", customer.DisplayName, "error", err)
			continue
		}

		flaggedCustomers[customerErpID] = true
		b.logger.Info("DepositSplitter: created review rule",
			"customer", customer.DisplayName,
			"total_txns", row.TotalTxns,
			"split_count", row.SplitCount,
		)
	}

	return flaggedCustomers, nil
}

// GenerateDepositRulesFromHistory creates 100% allocation rules for standard
// income mapped securely to BOTH the Customer and the receiving Bank Account.
func (b *Bootstrapper) GenerateDepositRulesFromHistory(ctx context.Context, realmID string, flaggedCustomers map[string]bool) error {
	rows, err := b.q.GetHistoricalDepositConsensus(ctx, database.GetHistoricalDepositConsensusParams{
		RealmID:       realmID,
		TargetRank:    int32(b.targetRank),
		MinUsageCount: int64(b.minUsageCount),
	})

	if err != nil {
		return fmt.Errorf("GetHistoricalDepositConsensus: %w", err)
	}

	for _, row := range rows {
		customerErpID := row.CustomerID
		if customerErpID == "" {
			continue
		}

		if flaggedCustomers[customerErpID] {
			continue
		}

		incomeAccountErpID, ok := row.IncomeAccountID.(string)
		if !ok || incomeAccountErpID == "" {
			b.logger.Warn("DepositConsensus: income_account_id is missing or not a string, skipping", "erp_id", customerErpID)
			continue
		}

		customer, err := b.q.GetCustomerByERPID(ctx, database.GetCustomerByERPIDParams{
			RealmID: realmID,
			ErpID:   customerErpID,
		})
		if err != nil || customer.DisplayName == "" {
			b.logger.Warn("DepositConsensus: customer not found, skipping",
				"erp_id", customerErpID, "error", err)
			continue
		}

		// Resolve target income account UUID from its QBO ERP ID (The Credit Side)
		incomeAccount, err := b.q.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{
			RealmID: realmID,
			ErpID:   incomeAccountErpID,
		})
		if err != nil {
			b.logger.Warn("DepositConsensus: income account not found, skipping",
				"customer", customer.DisplayName, "account_erp_id", incomeAccountErpID, "error", err)
			continue
		}

		// Resolve the bank account UUID receiving the funds (The Debit Side)
		bankAccount, err := b.q.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{
			RealmID: realmID,
			ErpID:   row.BankAccountID,
		})
		if err != nil {
			b.logger.Warn("DepositConsensus: bank account not found, skipping",
				"customer", customer.DisplayName, "bank_erp_id", row.BankAccountID, "error", err)
			continue
		}

		var entityUUID pgtype.UUID
		_ = entityUUID.Scan(customer.ID)

		allocations := []Allocation{{AccountID: incomeAccount.ID, Percentage: 100.0}}

		if err := b.ruleCreator.CreateRule(ctx, CreateRuleRequest{
			RealmID:        realmID,
			Name:           fmt.Sprintf("Auto-Generated Income: %s (to %s)", customer.DisplayName, bankAccount.Name),
			Logic:          LogicAnd,
			Priority:       10,
			Active:         true,
			Direction:      Inflow, // 🚨 Strict boundary for Deposit rules
			TargetEntityID: entityUUID,
			Allocations:    allocations,
			RequiresReview: false,
			Conditions: []RuleConditionRequest{
				{
					Field:    FieldCustomer,
					Operator: OpContainsCS,
					Value:    customer.DisplayName,
				},
				{
					// 🚨 Contextual lock: Rule only fires if deposited to this specific bank
					Field:    FieldSourceAccount,
					Operator: OpEqualsCS,
					Value:    bankAccount.Name,
				},
			},
		}); err != nil {
			b.logger.Error("DepositConsensus: failed to create rule",
				"customer", customer.DisplayName, "error", err)
			continue
		}

		b.logger.Info("DepositConsensus: created double-entry income rule",
			"customer", customer.DisplayName,
			"income_account", incomeAccount.Name,
			"bank_account", bankAccount.Name,
			"usage_count", row.UsageCount,
		)
	}

	return nil
}

