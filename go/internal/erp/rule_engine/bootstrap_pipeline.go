package ruleEngine

import (
	"context"
	"fmt"
)

// RunAdvancedBootstrap runs the feature-coverage bootstrapping pipeline sourcing
// exclusively from shadow_erp (QBO authoritative data). No fignode dependency.
//
// Pipeline:
//  1. Exact Match (Priority 100) — 100% consensus vendors/customers
//  2. Temporal (Priority 95) — date-based account changes
//  3. Amounts (Priority 90) — amount boundary detection
//  4. Allocations (Priority 75) — historical split percentages
//  5. Review Flags (Priority 1) — high-entropy catch-all
func (b *Bootstrapper) RunAdvancedBootstrap(ctx context.Context, realmID string) error {
	b.logger.Info("Starting advanced rule engine bootstrapping", "realm_id", realmID)

	var allRules []CreateRuleRequest

	// Phase 1: Exact Match (Highest Priority - 100)
	exactRules, err := b.bootstrapExactMatch(ctx, realmID)
	if err != nil {
		b.logger.Error("Exact match bootstrapping failed", "error", err)
	} else {
		b.logger.Info("Exact match bootstrapping complete", "count", len(exactRules))
		allRules = append(allRules, exactRules...)
	}

	// Phase 2: Temporal Analysis (Priority 95)
	temporalRules, err := b.bootstrapTemporal(ctx, realmID)
	if err != nil {
		b.logger.Error("Temporal bootstrapping failed", "error", err)
	} else {
		b.logger.Info("Temporal bootstrapping complete", "count", len(temporalRules))
		allRules = append(allRules, temporalRules...)
	}

	// Phase 3: Amount-Based Boundaries (Priority 90)
	amountRules, err := b.bootstrapAmounts(ctx, realmID)
	if err != nil {
		b.logger.Error("Amount bootstrapping failed", "error", err)
	} else {
		b.logger.Info("Amount bootstrapping complete", "count", len(amountRules))
		allRules = append(allRules, amountRules...)
	}

	// Phase 4: Historical Split Allocations (Priority 75)
	allocRules, err := b.bootstrapAllocations(ctx, realmID)
	if err != nil {
		b.logger.Error("Allocations bootstrapping failed", "error", err)
	} else {
		b.logger.Info("Allocations bootstrapping complete", "count", len(allocRules))
		allRules = append(allRules, allocRules...)
	}

	// Phase 5: Review Flags / High Entropy Catch-All (Priority 1)
	reviewRules, err := b.bootstrapReviewFlags(ctx, realmID)
	if err != nil {
		b.logger.Error("Review flags bootstrapping failed", "error", err)
	} else {
		b.logger.Info("Review flags bootstrapping complete", "count", len(reviewRules))
		allRules = append(allRules, reviewRules...)
	}

	b.logger.Info("All analyzers complete, creating rules",
		"total_rules", len(allRules),
	)

	// Submit all rules directly as flat rules.
	// The rule engine's EvaluateTransaction returns the allocations of the
	// matched top-level rule, so every rule must carry its own Allocations.
	for i, rule := range allRules {
		if err := b.ruleCreator.CreateRule(ctx, rule); err != nil {
			b.logger.Error("Failed to create rule",
				"rule_name", rule.Name,
				"priority", rule.Priority,
				"index", i,
				"error", err,
			)
			continue
		}
	}

	b.logger.Info("Advanced rule engine bootstrapping complete",
		"realm_id", realmID,
		"total_rules_created", len(allRules),
	)
	return nil
}

// RunAdvancedBootstrapWithLegacy runs both the advanced pipeline AND the legacy
// pipeline for backward compatibility. This ensures existing functionality
// (split review rules, 1-to-1 consensus rules) is preserved while the new
// analyzers add more sophisticated pattern detection.
func (b *Bootstrapper) RunAdvancedBootstrapWithLegacy(ctx context.Context, realmID string) error {
	// Run the legacy pipeline first (it creates rules directly)
	if err := b.RunRuleEngineForPurchases(ctx, realmID); err != nil {
		return fmt.Errorf("legacy pipeline failed: %w", err)
	}

	// Then run the advanced pipeline for additional coverage
	if err := b.RunAdvancedBootstrap(ctx, realmID); err != nil {
		return fmt.Errorf("advanced pipeline failed: %w", err)
	}

	return nil
}
