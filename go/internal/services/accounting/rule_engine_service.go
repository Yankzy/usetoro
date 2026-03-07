package accounting

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	quickbooks "github.com/Yankzy/usetoro/internal/erp/adapters/quickbooks/sdk"
	"github.com/dgraph-io/ristretto"
	"github.com/jackc/pgx/v5/pgtype"
)

// RuleEngineService manages the caching, compilation, and execution of QBO rules.
type RuleEngineService struct {
	logger *slog.Logger
	q      *database.Queries
	cache  *ristretto.Cache
}

// NewRuleEngineService creates a new RuleEngineService.
func NewRuleEngineService(logger *slog.Logger, q *database.Queries, cache *ristretto.Cache) *RuleEngineService {
	return &RuleEngineService{
		logger: logger,
		q:      q,
		cache:  cache,
	}
}

// getActiveRules fetches all rule groups and conditions for a realm, compiles them,
// and assembles them into a nested tree structure.
func (s *RuleEngineService) getActiveRules(ctx context.Context, realmID string) ([]*quickbooks.RuleGroup, error) {
	cacheKey := fmt.Sprintf("rules:%s", realmID)

	if val, found := s.cache.Get(cacheKey); found {
		if rules, ok := val.([]*quickbooks.RuleGroup); ok {
			return rules, nil
		}
	}

	dbGroups, err := s.q.GetActiveRuleGroupsByRealm(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch rule groups: %w", err)
	}
	if len(dbGroups) == 0 {
		return nil, nil
	}

	groupIDs := make([]int32, len(dbGroups))
	for i, g := range dbGroups {
		groupIDs[i] = g.ID
	}

	dbConditions, err := s.q.GetConditionsByRuleGroups(ctx, groupIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch rule conditions: %w", err)
	}

	groupMap := make(map[int32]*quickbooks.RuleGroup, len(dbGroups))
	for _, g := range dbGroups {
		groupMap[g.ID] = &quickbooks.RuleGroup{
			ID:              int(g.ID),
			Name:            g.Name,
			Logic:           quickbooks.LogicChoice(g.Logic),
			Priority:        int(g.Priority),
			Keywords:        g.Keywords.String,
			Active:          g.Active,
			TargetAccountID: g.TargetAccountID,
			TargetVendorID:  g.TargetVendorID,
		}
	}

	for _, c := range dbConditions {
		if group, ok := groupMap[c.RuleGroupID]; ok {
			group.Conditions = append(group.Conditions, &quickbooks.RuleCondition{
				ID:       int(c.ID),
				Field:    quickbooks.Field(c.Field),
				Operator: quickbooks.Operator(c.Operator),
				Value:    c.Value,
			})
		}
	}

	var topLevelRules []*quickbooks.RuleGroup
	for _, g := range dbGroups {
		group := groupMap[g.ID]
		if g.ParentID.Valid {
			if parent, ok := groupMap[g.ParentID.Int32]; ok {
				group.Parent = parent
				parent.Children = append(parent.Children, group)
			}
		} else {
			topLevelRules = append(topLevelRules, group)
		}
	}

	// P1-A: Only cache rules that pass validation and compilation.
	// Invalid rules are excluded to avoid silent "always false" degradation
	// and potential data races from lazy compilation on shared cached structs.
	validRules := make([]*quickbooks.RuleGroup, 0, len(topLevelRules))
	for _, r := range topLevelRules {
		if err := r.Validate(); err != nil {
			s.logger.Warn("Rule validation failed, excluding from cache", "rule_id", r.ID, "error", err)
			continue
		}
		if err := r.Compile(); err != nil {
			s.logger.Warn("Rule compilation failed, excluding from cache", "rule_id", r.ID, "error", err)
			continue
		}
		validRules = append(validRules, r)
	}

	s.cache.SetWithTTL(cacheKey, validRules, 1, 5*time.Minute)
	return validRules, nil
}

// RuleResult contains the match outcome and target UUIDs for the caller to use.
// Explanation carries the full structured trace for deferred audit-log persistence.
type RuleResult struct {
	MatchedRuleGroupID *int32
	TargetAccountID    pgtype.UUID
	TargetVendorID     pgtype.UUID
	Explanation        quickbooks.MatchExplanation
}

// EvaluateTransaction runs the transaction through the rule engine to find a match.
// Audit-log persistence is NOT performed here; the caller must invoke PersistAuditLog
// after a proposed_transactions row exists.
func (s *RuleEngineService) EvaluateTransaction(ctx context.Context, tx quickbooks.Transaction) (*RuleResult, error) {
	rules, err := s.getActiveRules(ctx, tx.EntityID)
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return nil, nil
	}

	candidates := quickbooks.FilterCandidates(tx, rules)

	for _, rule := range candidates {
		matched, exp := rule.Evaluate(tx)
		if matched {
			id := int32(rule.ID)
			return &RuleResult{
				MatchedRuleGroupID: &id,
				TargetAccountID:    rule.TargetAccountID,
				TargetVendorID:     rule.TargetVendorID,
				Explanation:        exp,
			}, nil
		}
	}

	return nil, nil
}

// PersistAuditLog writes the rule evaluation audit log after the proposed_transactions
// row exists. Only the winning match (or a no-match summary) is persisted.
func (s *RuleEngineService) PersistAuditLog(ctx context.Context, realmID string, transactionID pgtype.UUID, result *RuleResult) {
	if result == nil {
		return
	}

	matchInfoJSON, err := json.Marshal(result.Explanation)
	if err != nil {
		s.logger.Warn("Failed to marshal audit explanation", "error", err)
		return
	}

	var ruleGroupID pgtype.Int4
	if result.MatchedRuleGroupID != nil {
		ruleGroupID.Scan(*result.MatchedRuleGroupID)
	}

	_, err = s.q.CreateRuleAuditLog(ctx, database.CreateRuleAuditLogParams{
		RealmID:             realmID,
		TransactionID:       transactionID,
		RuleGroupID:         ruleGroupID,
		Matched:             result.Explanation.FinalResult,
		MatchInfo:           matchInfoJSON,
		HumanReadableReason: result.Explanation.HumanReadableReason(),
	})
	if err != nil {
		s.logger.Warn("Failed to persist rule audit log", "error", err, "realm_id", realmID)
	}
}
