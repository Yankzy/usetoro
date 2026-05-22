package ruleEngine

import (
	"context"
	"fmt"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
)

// temporalAccountChange describes when a vendor/customer switched target accounts.
type temporalAccountChange struct {
	oldAccountID string
	newAccountID string
	changeDate   time.Time
}

// temporalAccountPeriod tracks an account's usage during a time range.
type temporalAccountPeriod struct {
	accountID string
	firstSeen time.Time
	lastSeen  time.Time
	count     int64
}

// bootstrapTemporal generates Priority 95 rules based on temporal patterns.
func (b *Bootstrapper) bootstrapTemporal(ctx context.Context, realmID string) ([]CreateRuleRequest, error) {
	var rules []CreateRuleRequest

	outflowRules, err := b.bootstrapTemporalOutflow(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("temporal outflow: %w", err)
	}
	rules = append(rules, outflowRules...)

	inflowRules, err := b.bootstrapTemporalInflow(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("temporal inflow: %w", err)
	}
	rules = append(rules, inflowRules...)

	return rules, nil
}

func (b *Bootstrapper) bootstrapTemporalOutflow(ctx context.Context, realmID string) ([]CreateRuleRequest, error) {
	rows, err := b.q.GetVendorTemporalChanges(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("GetVendorTemporalChanges: %w", err)
	}

	// Group by (vendor, source_account) to detect account changes over time
	type key struct {
		entityID     string
		sourceAcctID string
	}
	vendorTimeline := make(map[key][]temporalAccountPeriod)

	for _, row := range rows {
		if !row.EntityID.Valid || row.EntityID.String == "" {
			continue
		}
		targetID, ok := row.TargetAccountID.(string)
		if !ok || targetID == "" {
			continue
		}

		// Parse the interface{} date/time values
		var firstSeen, lastSeen time.Time
		if t, ok := row.FirstSeen.(time.Time); ok {
			firstSeen = t
		}
		if t, ok := row.LastSeen.(time.Time); ok {
			lastSeen = t
		}
		if firstSeen.IsZero() || lastSeen.IsZero() {
			continue
		}

		k := key{row.EntityID.String, row.SourceAccountID}
		vendorTimeline[k] = append(vendorTimeline[k], temporalAccountPeriod{
			accountID: targetID,
			firstSeen: firstSeen,
			lastSeen:  lastSeen,
			count:     row.UsageCount,
		})
	}

	var rules []CreateRuleRequest
	for k, periods := range vendorTimeline {
		if len(periods) < 2 {
			continue
		}

		changes := detectAccountChanges(periods)
		if len(changes) == 0 {
			continue
		}

		vendor, err := b.q.GetVendorByERPID(ctx, database.GetVendorByERPIDParams{
			RealmID: realmID,
			ErpID:   k.entityID,
		})
		if err != nil || vendor.DisplayName == "" {
			continue
		}

		for _, change := range changes {
			newAccount, err := b.q.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{
				RealmID: realmID,
				ErpID:   change.newAccountID,
			})
			if err != nil {
				continue
			}

			var entityUUID pgtype.UUID
			_ = entityUUID.Scan(vendor.ID)

			cutoffDate := change.changeDate.Format("2006-01-02")

			rules = append(rules, CreateRuleRequest{
				RealmID:  realmID,
				Name:     fmt.Sprintf("Temporal: %s after %s", vendor.DisplayName, cutoffDate),
				Logic:    LogicAnd,
				Priority: 95,
				Active:   true,
				Direction:      Outflow,
				TargetEntityID: entityUUID,
				Allocations:    []Allocation{{AccountID: newAccount.ID, Percentage: 100.0}},
				RequiresReview: false,
				Conditions: []RuleConditionRequest{
					{Field: FieldVendor, Operator: OpContainsCS, Value: vendor.DisplayName},
					{Field: FieldDate, Operator: OpGt, Value: cutoffDate},
				},
			})
		}
	}
	return rules, nil
}

func (b *Bootstrapper) bootstrapTemporalInflow(ctx context.Context, realmID string) ([]CreateRuleRequest, error) {
	rows, err := b.q.GetCustomerTemporalChanges(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("GetCustomerTemporalChanges: %w", err)
	}

	type key struct {
		customerID string
		bankAcctID string
	}
	customerTimeline := make(map[key][]temporalAccountPeriod)

	for _, row := range rows {
		var firstSeen, lastSeen time.Time
		if t, ok := row.FirstSeen.(time.Time); ok {
			firstSeen = t
		}
		if t, ok := row.LastSeen.(time.Time); ok {
			lastSeen = t
		}
		if firstSeen.IsZero() || lastSeen.IsZero() {
			continue
		}

		incomeID, ok := row.IncomeAccountID.(string)
		if !ok || incomeID == "" {
			continue
		}

		k := key{row.CustomerID, row.BankAccountID}
		customerTimeline[k] = append(customerTimeline[k], temporalAccountPeriod{
			accountID: incomeID,
			firstSeen: firstSeen,
			lastSeen:  lastSeen,
			count:     row.UsageCount,
		})
	}

	var rules []CreateRuleRequest
	for k, periods := range customerTimeline {
		if len(periods) < 2 {
			continue
		}

		changes := detectAccountChanges(periods)
		if len(changes) == 0 {
			continue
		}

		customer, err := b.q.GetCustomerByERPID(ctx, database.GetCustomerByERPIDParams{
			RealmID: realmID,
			ErpID:   k.customerID,
		})
		if err != nil || customer.DisplayName == "" {
			continue
		}

		for _, change := range changes {
			incomeAccount, err := b.q.GetAccountByERPID(ctx, database.GetAccountByERPIDParams{
				RealmID: realmID,
				ErpID:   change.newAccountID,
			})
			if err != nil {
				continue
			}

			var entityUUID pgtype.UUID
			_ = entityUUID.Scan(customer.ID)

			cutoffDate := change.changeDate.Format("2006-01-02")

			rules = append(rules, CreateRuleRequest{
				RealmID:  realmID,
				Name:     fmt.Sprintf("Temporal Income: %s after %s", customer.DisplayName, cutoffDate),
				Logic:    LogicAnd,
				Priority: 95,
				Active:   true,
				Direction:      Inflow,
				TargetEntityID: entityUUID,
				Allocations:    []Allocation{{AccountID: incomeAccount.ID, Percentage: 100.0}},
				RequiresReview: false,
				Conditions: []RuleConditionRequest{
					{Field: FieldCustomer, Operator: OpContainsCS, Value: customer.DisplayName},
					{Field: FieldDate, Operator: OpGt, Value: cutoffDate},
				},
			})
		}
	}
	return rules, nil
}

// detectAccountChanges finds clear temporal boundaries where one account was
// replaced by another for the same vendor/source-account pair.
func detectAccountChanges(periods []temporalAccountPeriod) []temporalAccountChange {
	if len(periods) < 2 {
		return nil
	}

	var changes []temporalAccountChange

	for i := 0; i < len(periods); i++ {
		for j := i + 1; j < len(periods); j++ {
			if periods[i].lastSeen.Before(periods[j].firstSeen) {
				if periods[i].count >= 2 && periods[j].count >= 2 {
					changeDate := periods[i].lastSeen.Add(
						periods[j].firstSeen.Sub(periods[i].lastSeen) / 2,
					)
					changes = append(changes, temporalAccountChange{
						oldAccountID: periods[i].accountID,
						newAccountID: periods[j].accountID,
						changeDate:   changeDate,
					})
				}
			}
		}
	}

	return changes
}
