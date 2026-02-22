package accounting

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	quickbooks "github.com/Yankzy/usetoro/qbo"
	"github.com/dgraph-io/ristretto"
	"github.com/jackc/pgx/v5/pgtype"
)

// RuleEngineService manages the caching, compilation, and execution of QBO rules.
type RuleEngineService struct {
	logger *slog.Logger
	q      *database.Queries
	cache  *ristretto.Cache
}

// NewRuleEngineService creates a new RuleEngineService
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

	// 1. Try Cache
	if val, found := s.cache.Get(cacheKey); found {
		if rules, ok := val.([]*quickbooks.RuleGroup); ok {
			return rules, nil
		}
	}

	// 2. Fetch from DB
	dbGroups, err := s.q.GetActiveRuleGroupsByRealm(ctx, realmID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch rule groups: %w", err)
	}

	if len(dbGroups) == 0 {
		return nil, nil // No rules for this realm
	}

	// 3. Extract IDs to fetch conditions
	var groupIDs []int32
	for _, g := range dbGroups {
		groupIDs = append(groupIDs, g.ID)
	}

	dbConditions, err := s.q.GetConditionsByRuleGroups(ctx, groupIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch rule conditions: %w", err)
	}

	// 4. Assemble the objects
	groupMap := make(map[int32]*quickbooks.RuleGroup)
	for _, g := range dbGroups {
		groupMap[g.ID] = &quickbooks.RuleGroup{
			ID:       int(g.ID),
			Name:     g.Name,
			Logic:    quickbooks.LogicChoice(g.Logic),
			Priority: int(g.Priority),
			Keywords: g.Keywords.String,
			Active:   g.Active,
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

	// 5. Build Tree (Parent-Child references)
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

	// 6. Compile Regex/Numbers and Validate
	for _, r := range topLevelRules {
		if err := r.Validate(); err != nil {
			s.logger.Warn("Rule validation failed", "rule_id", r.ID, "error", err)
			continue
		}
		if err := r.Compile(); err != nil {
			s.logger.Warn("Rule compilation failed", "rule_id", r.ID, "error", err)
			continue
		}
	}

	// 7. Save to Cache (TTL: 5 minutes)
	s.cache.SetWithTTL(cacheKey, topLevelRules, 1, 5*time.Minute)

	return topLevelRules, nil
}

// RuleResult contains the target UUIDs if a rule matched.
type RuleResult struct {
	MatchedRuleGroupID *int32
	TargetAccountID    pgtype.UUID
	TargetVendorID     pgtype.UUID
}

// EvaluateTransaction runs the transaction through the rule engine to find a match.
func (s *RuleEngineService) EvaluateTransaction(ctx context.Context, tx quickbooks.Transaction) (*RuleResult, error) {
	rules, err := s.getActiveRules(ctx, tx.EntityID)
	if err != nil {
		return nil, err
	}

	if len(rules) == 0 {
		return nil, nil
	}

	// Candidates selection (keyword optimization)
	candidates := quickbooks.FilterCandidates(tx, rules)

	// Execute evaluation
	for _, rule := range candidates {
		// Pass `s.q` to save the audit log immediately
		matched, _ := rule.Evaluate(ctx, tx, s.q)
		if matched {
			// Find the database record for this target mapping
			dbGroups, err := s.q.GetActiveRuleGroupsByRealm(ctx, tx.EntityID)
			if err != nil {
				return nil, err
			}
			for _, g := range dbGroups {
				if g.ID == int32(rule.ID) {
					id := int32(rule.ID)
					return &RuleResult{
						MatchedRuleGroupID: &id,
						TargetAccountID:    g.TargetAccountID,
						TargetVendorID:     g.TargetVendorID,
					}, nil
				}
			}
		}
	}

	return nil, nil // No match
}
