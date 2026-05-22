package ruleEngine

import (
	"context"
	"fmt"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
)

// bootstrapReviewFlags generates catch-all RequiresReview rules for vendors
// and customers with extreme variance across accounts, amounts, and descriptions.
// These entities defy the other analyzers' pattern detection and are best
// parked for CPA manual review.
func (b *Bootstrapper) bootstrapReviewFlags(ctx context.Context, realmID string) ([]CreateRuleRequest, error) {
	var rules []CreateRuleRequest

	outflowRules, err := b.bootstrapReviewFlagsOutflow(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("review flags outflow: %w", err)
	}
	rules = append(rules, outflowRules...)

	inflowRules, err := b.bootstrapReviewFlagsInflow(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("review flags inflow: %w", err)
	}
	rules = append(rules, inflowRules...)

	return rules, nil
}

func (b *Bootstrapper) bootstrapReviewFlagsOutflow(ctx context.Context, realmID string) ([]CreateRuleRequest, error) {
	rows, err := b.q.GetHighEntropyVendors(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("GetHighEntropyVendors: %w", err)
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
			continue
		}

		var entityUUID pgtype.UUID
		_ = entityUUID.Scan(vendor.ID)

		rules = append(rules, CreateRuleRequest{
			RealmID:  realmID,
			Name:     fmt.Sprintf("Review: %s (high variance)", vendor.DisplayName),
			Logic:    LogicAnd,
			Priority: 1, // Lowest priority — catch-all
			Active:   true,
			Direction:      Outflow,
			TargetEntityID: entityUUID,
			Allocations:    []Allocation{},
			RequiresReview: true,
			Conditions: []RuleConditionRequest{
				{Field: FieldVendor, Operator: OpContainsCS, Value: vendor.DisplayName},
			},
		})

		b.logger.Info("ReviewFlags: chaotic vendor parked for review",
			"vendor", vendor.DisplayName,
			"distinct_accounts", row.DistinctAccounts,
			"total_txns", row.TotalTxns,
		)
	}
	return rules, nil
}

func (b *Bootstrapper) bootstrapReviewFlagsInflow(ctx context.Context, realmID string) ([]CreateRuleRequest, error) {
	rows, err := b.q.GetHighEntropyCustomers(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("GetHighEntropyCustomers: %w", err)
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
			continue
		}

		var entityUUID pgtype.UUID
		_ = entityUUID.Scan(customer.ID)

		rules = append(rules, CreateRuleRequest{
			RealmID:  realmID,
			Name:     fmt.Sprintf("Review Income: %s (high variance)", customer.DisplayName),
			Logic:    LogicAnd,
			Priority: 1,
			Active:   true,
			Direction:      Inflow,
			TargetEntityID: entityUUID,
			Allocations:    []Allocation{},
			RequiresReview: true,
			Conditions: []RuleConditionRequest{
				{Field: FieldCustomer, Operator: OpContainsCS, Value: customer.DisplayName},
			},
		})

		b.logger.Info("ReviewFlags: chaotic customer parked for review",
			"customer", customer.DisplayName,
			"distinct_accounts", row.DistinctAccounts,
			"total_txns", row.TotalTxns,
		)
	}
	return rules, nil
}
