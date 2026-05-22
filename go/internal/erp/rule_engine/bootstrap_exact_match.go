package ruleEngine

import (
	"context"
	"fmt"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
)

// bootstrapExactMatch generates Priority 100 rules for vendors/customers where
// 100% of historical transactions from a specific source account map to exactly
// one target account. These are the highest-confidence rules and take precedence
// over all other generated rules.
func (b *Bootstrapper) bootstrapExactMatch(ctx context.Context, realmID string) ([]CreateRuleRequest, error) {
	var rules []CreateRuleRequest

	outflowRules, err := b.bootstrapExactMatchOutflow(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("exact match outflow: %w", err)
	}
	rules = append(rules, outflowRules...)

	inflowRules, err := b.bootstrapExactMatchInflow(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("exact match inflow: %w", err)
	}
	rules = append(rules, inflowRules...)

	return rules, nil
}

func (b *Bootstrapper) bootstrapExactMatchOutflow(ctx context.Context, realmID string) ([]CreateRuleRequest, error) {
	rows, err := b.q.GetStrictConsensus(ctx, database.GetStrictConsensusParams{
		RealmID:       realmID,
		MinUsageCount: int64(b.minUsageCount),
	})
	if err != nil {
		return nil, fmt.Errorf("GetStrictConsensus: %w", err)
	}

	var rules []CreateRuleRequest
	for _, row := range rows {
		if !row.EntityID.Valid || row.EntityID.String == "" {
			continue
		}
		erpID := row.EntityID.String

		vendor, err := b.q.GetVendorByERPID(ctx, database.GetVendorByERPIDParams{
			RealmID: realmID,
			ErpID:   erpID,
		})
		if err != nil || vendor.DisplayName == "" {
			b.logger.Warn("ExactMatch: vendor not found", "erp_id", erpID, "error", err)
			continue
		}

		targetAccountErpID, ok := row.TargetAccountID.(string)
		if !ok || targetAccountErpID == "" {
			continue
		}

		account, err := b.q.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{
			RealmID: realmID,
			ErpID:   targetAccountErpID,
		})
		if err != nil {
			b.logger.Warn("ExactMatch: account not found", "erp_id", targetAccountErpID, "error", err)
			continue
		}

		sourceAccount, err := b.q.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{
			RealmID: realmID,
			ErpID:   row.SourceAccountID,
		})
		if err != nil {
			b.logger.Warn("ExactMatch: source account not found", "erp_id", row.SourceAccountID, "error", err)
			continue
		}

		var entityUUID pgtype.UUID
		_ = entityUUID.Scan(vendor.ID)

		rules = append(rules, CreateRuleRequest{
			RealmID:  realmID,
			Name:     fmt.Sprintf("Exact: %s (via %s)", vendor.DisplayName, sourceAccount.Name),
			Logic:    LogicAnd,
			Priority: 100,
			Active:   true,
			Direction:      Outflow,
			TargetEntityID: entityUUID,
			Allocations:    []Allocation{{AccountID: account.ID, Percentage: 100.0}},
			RequiresReview: false,
			Conditions: []RuleConditionRequest{
				{Field: FieldVendor, Operator: OpContainsCS, Value: vendor.DisplayName},
				{Field: FieldSourceAccount, Operator: OpEqualsCS, Value: sourceAccount.Name},
			},
		})

		b.logger.Info("ExactMatch: 100% consensus rule",
			"vendor", vendor.DisplayName,
			"account", account.Name,
			"source", sourceAccount.Name,
			"usage", row.UsageCount,
		)
	}
	return rules, nil
}

func (b *Bootstrapper) bootstrapExactMatchInflow(ctx context.Context, realmID string) ([]CreateRuleRequest, error) {
	rows, err := b.q.GetStrictDepositConsensus(ctx, database.GetStrictDepositConsensusParams{
		RealmID:       realmID,
		MinUsageCount: int64(b.minUsageCount),
	})
	if err != nil {
		return nil, fmt.Errorf("GetStrictDepositConsensus: %w", err)
	}

	var rules []CreateRuleRequest
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
			b.logger.Warn("ExactMatch inflow: customer not found", "erp_id", customerErpID, "error", err)
			continue
		}

		incomeAccountErpID, ok := row.IncomeAccountID.(string)
		if !ok || incomeAccountErpID == "" {
			b.logger.Warn("ExactMatch inflow: income_account_id is missing or not a string, skipping", "erp_id", customerErpID)
			continue
		}
		incomeAccount, err := b.q.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{
			RealmID: realmID,
			ErpID:   incomeAccountErpID,
		})
		if err != nil {
			b.logger.Warn("ExactMatch inflow: income account not found", "erp_id", incomeAccountErpID, "error", err)
			continue
		}

		bankAccount, err := b.q.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{
			RealmID: realmID,
			ErpID:   row.BankAccountID,
		})
		if err != nil {
			b.logger.Warn("ExactMatch inflow: bank account not found", "erp_id", row.BankAccountID, "error", err)
			continue
		}

		var entityUUID pgtype.UUID
		_ = entityUUID.Scan(customer.ID)

		rules = append(rules, CreateRuleRequest{
			RealmID:  realmID,
			Name:     fmt.Sprintf("Exact Income: %s (to %s)", customer.DisplayName, bankAccount.Name),
			Logic:    LogicAnd,
			Priority: 100,
			Active:   true,
			Direction:      Inflow,
			TargetEntityID: entityUUID,
			Allocations:    []Allocation{{AccountID: incomeAccount.ID, Percentage: 100.0}},
			RequiresReview: false,
			Conditions: []RuleConditionRequest{
				{Field: FieldCustomer, Operator: OpContainsCS, Value: customer.DisplayName},
				{Field: FieldSourceAccount, Operator: OpEqualsCS, Value: bankAccount.Name},
			},
		})

		b.logger.Info("ExactMatch inflow: 100% consensus rule",
			"customer", customer.DisplayName,
			"income_account", incomeAccount.Name,
			"bank", bankAccount.Name,
			"usage", row.UsageCount,
		)
	}
	return rules, nil
}
