package ruleEngine

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
)

// Bootstrapper analyses a realm's historical QBO purchases and auto-generates
// categorization rules using the rule_engine package.
type Bootstrapper struct {
	logger      *slog.Logger
	q           *database.Queries
	ruleCreator RuleCreator
}

func NewBootstrapper(logger *slog.Logger, q *database.Queries, ruleCreator RuleCreator) *Bootstrapper {
	return &Bootstrapper{
		logger:      logger,
		q:           q,
		ruleCreator: ruleCreator,
	}
}

// RunRuleEngineForPurchases is the master orchestrator called after the initial QBO sync.
// It runs the 2-step pipeline:
//  1. Detect chronic splitters → RequiresReview = true
//  2. Generate 1-to-1 rules for all safe vendors
func (b *Bootstrapper) RunRuleEngineForPurchases(ctx context.Context, realmID string) error {
	b.logger.Info("Starting combined rule bootstrapping", "realm_id", realmID)

	// Step 1: Flag the complicated split vendors (e.g., Payroll, Loans)
	flaggedVendors, err := b.GenerateSplitReviewRules(ctx, realmID)
	if err != nil {
		return fmt.Errorf("failed generating split review rules: %w", err)
	}

	// Step 2: Auto-generate 1-to-1 rules for the safe vendors
	if err := b.GenerateRulesFromHistory(ctx, realmID, flaggedVendors); err != nil {
		return fmt.Errorf("failed generating 1-to-1 historical rules: %w", err)
	}

	// Step 3 & 4: Run the equivalent pipeline for Deposit / Income rules
	if err := b.RunRuleEngineForDeposits(ctx, realmID); err != nil {
		return fmt.Errorf("failed deposit onboarding: %w", err)
	}

	b.logger.Info("Successfully completed rule bootstrapping", "realm_id", realmID)
	return nil
}

// GenerateSplitReviewRules identifies vendors that frequently have multiple
// expense lines and forces them to manual CPA review (RequiresReview = true).
// It returns the set of flagged vendor ERP IDs so Step 2 can skip them.
func (b *Bootstrapper) GenerateSplitReviewRules(ctx context.Context, realmID string) (map[string]bool, error) {
	rows, err := b.q.GetHistoricalSplitters(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("GetHistoricalSplitters: %w", err)
	}

	flaggedVendors := make(map[string]bool)

	for _, row := range rows {
		// entity_id is a pgtype.Text (nullable)
		if !row.EntityID.Valid || row.EntityID.String == "" {
			continue
		}
		erpID := row.EntityID.String

		vendor, err := b.q.GetVendorByERPID(ctx, database.GetVendorByERPIDParams{
			RealmID: realmID,
			ErpID:   erpID,
		})
		if err != nil || vendor.DisplayName == "" {
			b.logger.Warn("Splitter: vendor not found, skipping", "erp_id", erpID, "error", err)
			continue
		}

		var entityUUID pgtype.UUID
		_ = entityUUID.Scan(vendor.ID)

		if err := b.ruleCreator.CreateRule(ctx, CreateRuleRequest{
			RealmID:        realmID,
			Name:           fmt.Sprintf("Auto-Generated Split Review: %s", vendor.DisplayName),
			Logic:          LogicAnd,
			Priority:       5,
			Active:         true,
			TargetEntityID: entityUUID,
			Allocations:    []Allocation{},
			RequiresReview: true,
			Conditions: []RuleConditionRequest{
				{
					Field:    FieldVendor,
					Operator: OpEqualsCS,
					Value:    vendor.DisplayName,
				},
			},
		}); err != nil {
			b.logger.Error("Splitter: failed to create rule", "vendor", vendor.DisplayName, "error", err)
			continue
		}

		flaggedVendors[erpID] = true
		b.logger.Info("Splitter: created review rule",
			"vendor", vendor.DisplayName,
			"total_txns", row.TotalTxns,
			"split_count", row.SplitCount,
		)
	}

	return flaggedVendors, nil
}

// GenerateRulesFromHistory generates 100% allocation rules for simple vendors,
// skipping any that were caught by the Splitter logic in Step 1.
func (b *Bootstrapper) GenerateRulesFromHistory(ctx context.Context, realmID string, flaggedVendors map[string]bool) error {
	rows, err := b.q.GetHistoricalPurchaseConsensus(ctx, realmID)
	if err != nil {
		return fmt.Errorf("GetHistoricalPurchaseConsensus: %w", err)
	}

	for _, row := range rows {
		// entity_id is pgtype.Text (nullable)
		if !row.EntityID.Valid || row.EntityID.String == "" {
			continue
		}
		erpID := row.EntityID.String

		// Skip vendors already flagged for manual CPA review
		if flaggedVendors[erpID] {
			continue
		}

		// target_account_id comes from a JSONB extraction; sqlc types it as interface{}
		targetAccountErpID, ok := row.TargetAccountID.(string)
		if !ok || targetAccountErpID == "" {
			b.logger.Warn("Consensus: target_account_id is not a string, skipping", "erp_id", erpID)
			continue
		}

		vendor, err := b.q.GetVendorByERPID(ctx, database.GetVendorByERPIDParams{
			RealmID: realmID,
			ErpID:   erpID,
		})
		if err != nil || vendor.DisplayName == "" {
			b.logger.Warn("Consensus: vendor not found, skipping", "erp_id", erpID, "error", err)
			continue
		}

		// Resolve target account UUID from its QBO ERP ID
		account, err := b.q.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{
			RealmID: realmID,
			ErpID:   targetAccountErpID,
		})
		if err != nil {
			b.logger.Warn("Consensus: account not found, skipping",
				"vendor", vendor.DisplayName, "account_erp_id", targetAccountErpID, "error", err)
			continue
		}

		var entityUUID pgtype.UUID
		_ = entityUUID.Scan(vendor.ID)

		allocations := []Allocation{{AccountID: account.ID, Percentage: 100.0}}

		if err := b.ruleCreator.CreateRule(ctx, CreateRuleRequest{
			RealmID:        realmID,
			Name:           fmt.Sprintf("Auto-Generated: %s", vendor.DisplayName),
			Logic:          LogicAnd,
			Priority:       10,
			Active:         true,
			TargetEntityID: entityUUID,
			Allocations:    allocations,
			RequiresReview: false,
			Conditions: []RuleConditionRequest{
				{
					Field:    FieldVendor,
					Operator: OpEqualsCS,
					Value:    vendor.DisplayName,
				},
			},
		}); err != nil {
			b.logger.Error("Consensus: failed to create rule", "vendor", vendor.DisplayName, "error", err)
			continue
		}

		b.logger.Info("Consensus: created 1-to-1 rule",
			"vendor", vendor.DisplayName,
			"account", account.Name,
			"usage_count", row.UsageCount,
		)
	}

	return nil
}
