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
	logger        *slog.Logger
	q             *database.Queries
	ruleCreator   RuleCreator
	targetRank    int
	minUsageCount int
}

func NewBootstrapper(logger *slog.Logger, q *database.Queries, ruleCreator RuleCreator) *Bootstrapper {
	return &Bootstrapper{
		logger:        logger,
		q:             q,
		ruleCreator:   ruleCreator,
		targetRank:    1, // Default fallback
		minUsageCount: 1, // Default fallback
	}
}

func (b *Bootstrapper) WithConfig(targetRank, minUsageCount int) *Bootstrapper {
	b.targetRank = targetRank
	b.minUsageCount = minUsageCount
	return b
}

// RunRuleEngineForPurchases is the master orchestrator called after the initial QBO sync.
// It runs the 2-step pipeline:
//  1. Detect chronic splitters → RequiresReview = true
//  2. Generate highly contextual 1-to-1 rules
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
	// (Ensure your deposit bootstrapper injects Direction: Inflow)
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
			Direction:      Outflow, // 🚨 Strict boundary for Purchase rules
			TargetEntityID: entityUUID,
			Allocations:    []Allocation{},
			RequiresReview: true,
			Conditions: []RuleConditionRequest{
				{
					// We intentionally omit Source Account here because Gusto is complex
					// regardless of which checking account pays it.
					Field:    FieldVendor,
					Operator: OpContainsCS,
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

// GenerateRulesFromHistory generates 100% allocation rules mapped securely to
// BOTH the Vendor and the specific Source Bank/Credit Card Account.
func (b *Bootstrapper) GenerateRulesFromHistory(ctx context.Context, realmID string, flaggedVendors map[string]bool) error {
	rows, err := b.q.GetHistoricalPurchaseConsensus(ctx, database.GetHistoricalPurchaseConsensusParams{
		RealmID:       realmID,
		TargetRank:    int32(b.targetRank),
		MinUsageCount: int64(b.minUsageCount),
	})
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

		// Resolve target account UUID from its QBO ERP ID (Debit Side - e.g., Meals)
		account, err := b.q.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{
			RealmID: realmID,
			ErpID:   targetAccountErpID,
		})
		if err != nil {
			b.logger.Warn("Consensus: target account not found, skipping",
				"vendor", vendor.DisplayName, "account_erp_id", targetAccountErpID, "error", err)
			continue
		}

		// Resolve source account UUID from its QBO ERP ID (Credit Side - e.g., Chase Checking)
		sourceAccount, err := b.q.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{
			RealmID: realmID,
			ErpID:   row.SourceAccountID,
		})
		if err != nil {
			b.logger.Warn("Consensus: source account not found, skipping",
				"vendor", vendor.DisplayName, "source_erp_id", row.SourceAccountID, "error", err)
			continue
		}

		var entityUUID pgtype.UUID
		_ = entityUUID.Scan(vendor.ID)

		allocations := []Allocation{{AccountID: account.ID, Percentage: 100.0}}

		if err := b.ruleCreator.CreateRule(ctx, CreateRuleRequest{
			RealmID: realmID,
			// Make the rule name clearly reflect its context to the CPA
			Name:           fmt.Sprintf("Auto-Generated: %s (via %s)", vendor.DisplayName, sourceAccount.Name),
			Logic:          LogicAnd,
			Priority:       10,
			Active:         true,
			Direction:      Outflow, // 🚨 Strict boundary for Purchase rules
			TargetEntityID: entityUUID,
			Allocations:    allocations,
			RequiresReview: false,
			Conditions: []RuleConditionRequest{
				{
					Field:    FieldVendor,
					Operator: OpContainsCS,
					Value:    vendor.DisplayName,
				},
				{
					// 🚨 Contextual lock: Rule only fires for this specific bank account
					Field:    FieldSourceAccount,
					Operator: OpEqualsCS,
					Value:    sourceAccount.Name,
				},
			},
		}); err != nil {
			b.logger.Error("Consensus: failed to create rule", "vendor", vendor.DisplayName, "error", err)
			continue
		}

		b.logger.Info("Consensus: created double-entry 1-to-1 rule",
			"vendor", vendor.DisplayName,
			"target_account", account.Name,
			"source_account", sourceAccount.Name,
			"usage_count", row.UsageCount,
		)
	}

	return nil
}
