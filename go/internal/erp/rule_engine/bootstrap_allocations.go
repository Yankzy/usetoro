package ruleEngine

import (
	"context"
	"fmt"
	"math"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
)

// bootstrapAllocations extracts historical split percentage patterns from
// multi-line purchases and deposits. When a vendor consistently splits
// transactions by a specific ratio (e.g., 50/50, 70/30), it generates
// rules with pre-filled Allocation arrays instead of requiring manual CPA splits.
func (b *Bootstrapper) bootstrapAllocations(ctx context.Context, realmID string) ([]CreateRuleRequest, error) {
	var rules []CreateRuleRequest

	outflowRules, err := b.bootstrapAllocationsOutflow(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("allocations outflow: %w", err)
	}
	rules = append(rules, outflowRules...)

	inflowRules, err := b.bootstrapAllocationsInflow(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("allocations inflow: %w", err)
	}
	rules = append(rules, inflowRules...)

	return rules, nil
}

func (b *Bootstrapper) bootstrapAllocationsOutflow(ctx context.Context, realmID string) ([]CreateRuleRequest, error) {
	rows, err := b.q.GetHistoricalSplitPercentages(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("GetHistoricalSplitPercentages: %w", err)
	}

	// Group by vendor, collecting split patterns
	type splitPattern struct {
		accounts    []string
		percentages []float64
	}
	vendorPatterns := make(map[string]map[string]*splitPattern) // vendor_erp_id -> purchase_id -> pattern

	for _, row := range rows {
		if !row.EntityID.Valid || row.EntityID.String == "" {
			continue
		}
		accountID, ok := row.AccountID.(string)
		if !ok || accountID == "" {
			continue
		}

		lineAmount, err := numericToFloat64(row.LineAmount)
		if err != nil || lineAmount == 0 {
			continue
		}
		totalAmount, err := numericToFloat64(row.TotalAmount)
		if err != nil || totalAmount == 0 {
			continue
		}

		pct := (lineAmount / totalAmount) * 100.0
		pct = math.Round(pct*100) / 100 // Round to 2 decimals

		erpID := row.EntityID.String
		if vendorPatterns[erpID] == nil {
			vendorPatterns[erpID] = make(map[string]*splitPattern)
		}

		// Use purchase ID as pattern key to group lines of the same transaction
		purchaseKey := fmt.Sprintf("%v", row.PurchaseID)
		sp := vendorPatterns[erpID][purchaseKey]
		if sp == nil {
			sp = &splitPattern{}
			vendorPatterns[erpID][purchaseKey] = sp
		}
		sp.accounts = append(sp.accounts, accountID)
		sp.percentages = append(sp.percentages, pct)
	}

	var rules []CreateRuleRequest
	for erpID, patterns := range vendorPatterns {
		// Look for consistent split patterns across multiple transactions
		type consistentSplit struct {
			accountIDs []string
			pcts       []float64
			count      int
		}
		patternConsensus := make(map[string]*consistentSplit) // keyed by account combination

		for _, sp := range patterns {
			if len(sp.accounts) < 2 {
				continue
			}
			key := fmt.Sprintf("%v", sp.accounts)

			if patternConsensus[key] == nil {
				// Average the percentages
				avgPcts := make([]float64, len(sp.percentages))
				copy(avgPcts, sp.percentages)
				patternConsensus[key] = &consistentSplit{
					accountIDs: sp.accounts,
					pcts:       avgPcts,
					count:      1,
				}
			} else {
				cs := patternConsensus[key]
				cs.count++
				for i, p := range sp.percentages {
					if i < len(cs.pcts) {
						cs.pcts[i] = (cs.pcts[i]*float64(cs.count-1) + p) / float64(cs.count)
					}
				}
			}
		}

		for _, cs := range patternConsensus {
			if cs.count < 2 {
				continue // Need at least 2 consistent splits
			}

			vendor, err := b.q.GetVendorByERPID(ctx, database.GetVendorByERPIDParams{
				RealmID: realmID,
				ErpID:   erpID,
			})
			if err != nil || vendor.DisplayName == "" {
				continue
			}

			var allocations []Allocation
			for i, acctErpID := range cs.accountIDs {
				account, err := b.q.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{
					RealmID: realmID,
					ErpID:   acctErpID,
				})
				if err != nil {
					continue
				}
				allocations = append(allocations, Allocation{
					AccountID:  account.ID,
					Percentage: math.Round(cs.pcts[i]*100) / 100,
				})
			}

			if len(allocations) < 2 {
				continue
			}

			var entityUUID pgtype.UUID
			_ = entityUUID.Scan(vendor.ID)

			rules = append(rules, CreateRuleRequest{
				RealmID:  realmID,
				Name:     fmt.Sprintf("Split: %s (%d-way, %d txns)", vendor.DisplayName, len(allocations), cs.count),
				Logic:    LogicAnd,
				Priority: 75,
				Active:   true,
				Direction:      Outflow,
				TargetEntityID: entityUUID,
				Allocations:    allocations,
				RequiresReview: false,
				Conditions: []RuleConditionRequest{
					{Field: FieldVendor, Operator: OpContainsCS, Value: vendor.DisplayName},
				},
			})
		}
	}
	return rules, nil
}

func (b *Bootstrapper) bootstrapAllocationsInflow(ctx context.Context, realmID string) ([]CreateRuleRequest, error) {
	rows, err := b.q.GetDepositSplitPercentages(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("GetDepositSplitPercentages: %w", err)
	}

	type splitPattern struct {
		accounts    []string
		percentages []float64
	}
	customerPatterns := make(map[string]map[string]*splitPattern)

	for _, row := range rows {
		lineAmount, err := numericToFloat64(row.LineAmount)
		if err != nil || lineAmount == 0 {
			continue
		}
		totalAmount, err := numericToFloat64(row.TotalAmount)
		if err != nil || totalAmount == 0 {
			continue
		}

		pct := (lineAmount / totalAmount) * 100.0
		pct = math.Round(pct*100) / 100

		accountID, ok := row.AccountID.(string)
		if !ok || accountID == "" {
			continue
		}

		depositKey := fmt.Sprintf("%v", row.DepositID)
		if customerPatterns[row.CustomerID] == nil {
			customerPatterns[row.CustomerID] = make(map[string]*splitPattern)
		}
		sp := customerPatterns[row.CustomerID][depositKey]
		if sp == nil {
			sp = &splitPattern{}
			customerPatterns[row.CustomerID][depositKey] = sp
		}
		sp.accounts = append(sp.accounts, accountID)
		sp.percentages = append(sp.percentages, pct)
	}

	var rules []CreateRuleRequest
	for customerErpID, patterns := range customerPatterns {
		type consistentSplit struct {
			accountIDs []string
			pcts       []float64
			count      int
		}
		patternConsensus := make(map[string]*consistentSplit)

		for _, sp := range patterns {
			if len(sp.accounts) < 2 {
				continue
			}
			key := fmt.Sprintf("%v", sp.accounts)
			if patternConsensus[key] == nil {
				avgPcts := make([]float64, len(sp.percentages))
				copy(avgPcts, sp.percentages)
				patternConsensus[key] = &consistentSplit{
					accountIDs: sp.accounts,
					pcts:       avgPcts,
					count:      1,
				}
			} else {
				cs := patternConsensus[key]
				cs.count++
				for i, p := range sp.percentages {
					if i < len(cs.pcts) {
						cs.pcts[i] = (cs.pcts[i]*float64(cs.count-1) + p) / float64(cs.count)
					}
				}
			}
		}

		for _, cs := range patternConsensus {
			if cs.count < 2 {
				continue
			}

			customer, err := b.q.GetCustomerByERPID(ctx, database.GetCustomerByERPIDParams{
				RealmID: realmID,
				ErpID:   customerErpID,
			})
			if err != nil || customer.DisplayName == "" {
				continue
			}

			var allocations []Allocation
			for i, acctErpID := range cs.accountIDs {
				account, err := b.q.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{
					RealmID: realmID,
					ErpID:   acctErpID,
				})
				if err != nil {
					continue
				}
				allocations = append(allocations, Allocation{
					AccountID:  account.ID,
					Percentage: math.Round(cs.pcts[i]*100) / 100,
				})
			}

			if len(allocations) < 2 {
				continue
			}

			var entityUUID pgtype.UUID
			_ = entityUUID.Scan(customer.ID)

			rules = append(rules, CreateRuleRequest{
				RealmID:  realmID,
				Name:     fmt.Sprintf("Income Split: %s (%d-way)", customer.DisplayName, len(allocations)),
				Logic:    LogicAnd,
				Priority: 75,
				Active:   true,
				Direction:      Inflow,
				TargetEntityID: entityUUID,
				Allocations:    allocations,
				RequiresReview: false,
				Conditions: []RuleConditionRequest{
					{Field: FieldCustomer, Operator: OpContainsCS, Value: customer.DisplayName},
				},
			})
		}
	}
	return rules, nil
}
