package ruleEngine

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
)

// numericToFloat64 converts a pgtype.Numeric to float64.
func numericToFloat64(n pgtype.Numeric) (float64, error) {
	f8, err := n.Float64Value()
	if err != nil {
		return 0, err
	}
	if !f8.Valid {
		return 0, fmt.Errorf("numeric is null")
	}
	return f8.Float64, nil
}

// vendorAmountStats holds amount distribution for a vendor's routing to a specific account.
type vendorAmountStats struct {
	entityID     string
	accountID    string
	sourceAcctID string
	amounts      []float64
}

// bootstrapAmounts generates Priority 90 rules that classify transactions by
// amount boundaries. For vendors that route to different accounts based on
// transaction amount, it finds statistical boundaries and generates OpGt/OpLt rules.
func (b *Bootstrapper) bootstrapAmounts(ctx context.Context, realmID string) ([]CreateRuleRequest, error) {
	var rules []CreateRuleRequest

	outflowRules, err := b.bootstrapAmountsOutflow(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("amounts outflow: %w", err)
	}
	rules = append(rules, outflowRules...)

	inflowRules, err := b.bootstrapAmountsInflow(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("amounts inflow: %w", err)
	}
	rules = append(rules, inflowRules...)

	return rules, nil
}

func (b *Bootstrapper) bootstrapAmountsOutflow(ctx context.Context, realmID string) ([]CreateRuleRequest, error) {
	rows, err := b.q.GetVendorAmountDistribution(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("GetVendorAmountDistribution: %w", err)
	}

	// Group amounts by (vendor, target_account, source_account)
	type key struct {
		entity, target, source string
	}
	groups := make(map[key]*vendorAmountStats)

	for _, row := range rows {
		if !row.EntityID.Valid || row.EntityID.String == "" {
			continue
		}
		targetID, ok := row.TargetAccountID.(string)
		if !ok || targetID == "" {
			continue
		}

		k := key{row.EntityID.String, targetID, row.SourceAccountID}
		if groups[k] == nil {
			groups[k] = &vendorAmountStats{
				entityID:     row.EntityID.String,
				accountID:    targetID,
				sourceAcctID: row.SourceAccountID,
			}
		}
		if amount, err := numericToFloat64(row.TotalAmount); err == nil {
			groups[k].amounts = append(groups[k].amounts, amount)
		}
	}

	// Group by vendor to find multi-account vendors
	vendorGroups := make(map[string][]*vendorAmountStats)
	for _, stats := range groups {
		vendorGroups[stats.entityID] = append(vendorGroups[stats.entityID], stats)
	}

	var rules []CreateRuleRequest
	for erpID, statsList := range vendorGroups {
		if len(statsList) < 2 {
			continue // Only interested in vendors with multiple target accounts
		}

		vendor, err := b.q.GetVendorByERPID(ctx, database.GetVendorByERPIDParams{
			RealmID: realmID,
			ErpID:   erpID,
		})
		if err != nil || vendor.DisplayName == "" {
			continue
		}

		// Find clear amount boundaries between accounts
		boundaries := findAmountBoundaries(statsList)
		for _, boundary := range boundaries {
			account, err := b.q.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{
				RealmID: realmID,
				ErpID:   boundary.targetAccountID,
			})
			if err != nil {
				continue
			}

			var entityUUID pgtype.UUID
			_ = entityUUID.Scan(vendor.ID)

			var conditions []RuleConditionRequest
			conditions = append(conditions, RuleConditionRequest{
				Field:    FieldVendor,
				Operator: OpContainsCS,
				Value:    vendor.DisplayName,
			})
			conditions = append(conditions, boundary.conditions...)

			rules = append(rules, CreateRuleRequest{
				RealmID:  realmID,
				Name:     fmt.Sprintf("Amount: %s %s", vendor.DisplayName, boundary.label),
				Logic:    LogicAnd,
				Priority: 90,
				Active:   true,
				Direction:      Outflow,
				TargetEntityID: entityUUID,
				Allocations:    []Allocation{{AccountID: account.ID, Percentage: 100.0}},
				RequiresReview: false,
				Conditions:     conditions,
			})
		}
	}
	return rules, nil
}

func (b *Bootstrapper) bootstrapAmountsInflow(ctx context.Context, realmID string) ([]CreateRuleRequest, error) {
	rows, err := b.q.GetDepositAmountDistribution(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("GetDepositAmountDistribution: %w", err)
	}

	type key struct {
		customer, income, bank string
	}
	groups := make(map[key]*vendorAmountStats)

	for _, row := range rows {
		incomeID, ok := row.IncomeAccountID.(string)
		if !ok || incomeID == "" {
			continue
		}

		k := key{row.CustomerID, incomeID, row.BankAccountID}
		if k.customer == "" {
			continue
		}
		if groups[k] == nil {
			groups[k] = &vendorAmountStats{
				entityID:     k.customer,
				accountID:    k.income,
				sourceAcctID: k.bank,
			}
		}
		if amount, err := numericToFloat64(row.TotalAmount); err == nil {
			groups[k].amounts = append(groups[k].amounts, amount)
		}
	}

	customerGroups := make(map[string][]*vendorAmountStats)
	for _, stats := range groups {
		customerGroups[stats.entityID] = append(customerGroups[stats.entityID], stats)
	}

	var rules []CreateRuleRequest
	for customerErpID, statsList := range customerGroups {
		if len(statsList) < 2 {
			continue
		}

		customer, err := b.q.GetCustomerByERPID(ctx, database.GetCustomerByERPIDParams{
			RealmID: realmID,
			ErpID:   customerErpID,
		})
		if err != nil || customer.DisplayName == "" {
			continue
		}

		boundaries := findAmountBoundaries(statsList)
		for _, boundary := range boundaries {
			account, err := b.q.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{
				RealmID: realmID,
				ErpID:   boundary.targetAccountID,
			})
			if err != nil {
				continue
			}

			var entityUUID pgtype.UUID
			_ = entityUUID.Scan(customer.ID)

			var conditions []RuleConditionRequest
			conditions = append(conditions, RuleConditionRequest{
				Field:    FieldCustomer,
				Operator: OpContainsCS,
				Value:    customer.DisplayName,
			})
			conditions = append(conditions, boundary.conditions...)

			rules = append(rules, CreateRuleRequest{
				RealmID:  realmID,
				Name:     fmt.Sprintf("Amount Income: %s %s", customer.DisplayName, boundary.label),
				Logic:    LogicAnd,
				Priority: 90,
				Active:   true,
				Direction:      Inflow,
				TargetEntityID: entityUUID,
				Allocations:    []Allocation{{AccountID: account.ID, Percentage: 100.0}},
				RequiresReview: false,
				Conditions:     conditions,
			})
		}
	}
	return rules, nil
}

type amountBoundary struct {
	targetAccountID string
	label           string
	conditions      []RuleConditionRequest
}

// findAmountBoundaries analyzes amount distributions across multiple accounts
// and finds clear separation boundaries. Returns rules for accounts that can
// be separated by simple Gt/Lt thresholds.
func findAmountBoundaries(statsList []*vendorAmountStats) []amountBoundary {
	if len(statsList) < 2 {
		return nil
	}

	var boundaries []amountBoundary

	// For each account, compute statistics
	for _, stats := range statsList {
		if len(stats.amounts) < 3 {
			continue
		}

		sort.Float64s(stats.amounts)

		min := stats.amounts[0]
		max := stats.amounts[len(stats.amounts)-1]
		meanVal := mean(stats.amounts)
		std := stddev(stats.amounts, meanVal)

		// Check if this account is separated from others by amount
		// Case 1: All amounts for this account are below a threshold
		// and all other accounts' amounts are above
		for _, other := range statsList {
			if other == stats || len(other.amounts) < 3 {
				continue
			}

			otherMin := minFloat64(other.amounts)

			// This account is always smaller than the other
			if max < otherMin && (max+std*2) < otherMin {
				// Use a threshold at the midpoint with margin
				threshold := (max + otherMin) / 2.0
				threshold = math.Round(threshold*100) / 100 // Round to cents

				boundaries = append(boundaries, amountBoundary{
					targetAccountID: stats.accountID,
					label:           fmt.Sprintf("< $%.2f", threshold),
					conditions: []RuleConditionRequest{
						{Field: FieldAmount, Operator: OpLt, Value: fmt.Sprintf("%.2f", threshold)},
					},
				})
			}

			// This account is always larger than the other
			otherMax := maxFloat64(other.amounts)
			if min > otherMax && (min-std*2) > otherMax {
				threshold := (otherMax + min) / 2.0
				threshold = math.Round(threshold*100) / 100

				boundaries = append(boundaries, amountBoundary{
					targetAccountID: stats.accountID,
					label:           fmt.Sprintf("> $%.2f", threshold),
					conditions: []RuleConditionRequest{
						{Field: FieldAmount, Operator: OpGt, Value: fmt.Sprintf("%.2f", threshold)},
					},
				})
			}
		}

		// Case 2: Check for fixed-amount subscriptions (low variance)
		if std < 1.0 && meanVal > 0 {
			low := math.Round((meanVal-std*2)*100) / 100
			high := math.Round((meanVal+std*2)*100) / 100
			if high-low < 5.0 && meanVal > 5.0 {
				boundaries = append(boundaries, amountBoundary{
					targetAccountID: stats.accountID,
					label:           fmt.Sprintf("≈ $%.2f", meanVal),
					conditions: []RuleConditionRequest{
						{Field: FieldAmount, Operator: OpGte, Value: fmt.Sprintf("%.2f", low)},
						{Field: FieldAmount, Operator: OpLte, Value: fmt.Sprintf("%.2f", high)},
					},
				})
			}
		}
	}

	return boundaries
}

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func stddev(vals []float64, m float64) float64 {
	if len(vals) < 2 {
		return 0
	}
	var sumSq float64
	for _, v := range vals {
		sumSq += (v - m) * (v - m)
	}
	return math.Sqrt(sumSq / float64(len(vals)))
}

func minFloat64(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	min := vals[0]
	for _, v := range vals[1:] {
		if v < min {
			min = v
		}
	}
	return min
}

func maxFloat64(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	max := vals[0]
	for _, v := range vals[1:] {
		if v > max {
			max = v
		}
	}
	return max
}
