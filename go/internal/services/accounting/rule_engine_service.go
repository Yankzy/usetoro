package accounting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	ruleEngine "github.com/Yankzy/usetoro/internal/erp/rule_engine"
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

// ProcessOrphanedTransactions finds Purchases and Deposits for a realm that lack a rule_id
// and runs them through the rule engine to attempt automatic categorization.
func (s *RuleEngineService) ProcessOrphanedTransactions(ctx context.Context, realmID string) error {
	s.logger.Info("Starting orphaned transaction cleanup", "realm_id", realmID)

	// 1. Process Purchases
	purchases, err := s.q.GetOrphanedPurchases(ctx, realmID)
	if err != nil {
		return fmt.Errorf("failed to fetch orphaned purchases: %w", err)
	}

	for _, p := range purchases {
		tx := s.mapPurchaseToTransaction(p)
		result, err := s.EvaluateTransaction(ctx, tx)
		if err != nil {
			s.logger.Warn("Failed to evaluate purchase", "purchase_id", p.ID, "error", err)
			continue
		}
		if result != nil && result.MatchedRuleGroupID != nil {
			ruleID := pgtype.Int4{}
			ruleID.Scan(*result.MatchedRuleGroupID)
			if err := s.q.UpdatePurchaseRuleID(ctx, database.UpdatePurchaseRuleIDParams{ID: p.ID, RuleID: ruleID}); err != nil {
				s.logger.Warn("Failed to update purchase rule_id", "purchase_id", p.ID, "error", err)
			}
			s.PersistAuditLog(ctx, realmID, p.ID, result)
		}
	}

	// 2. Process Deposits
	deposits, err := s.q.GetOrphanedDeposits(ctx, realmID)
	if err != nil {
		return fmt.Errorf("failed to fetch orphaned deposits: %w", err)
	}

	for _, d := range deposits {
		tx := s.mapDepositToTransaction(d)
		result, err := s.EvaluateTransaction(ctx, tx)
		if err != nil {
			s.logger.Warn("Failed to evaluate deposit", "deposit_id", d.ID, "error", err)
			continue
		}
		if result != nil && result.MatchedRuleGroupID != nil {
			ruleID := pgtype.Int4{}
			ruleID.Scan(*result.MatchedRuleGroupID)
			if err := s.q.UpdateDepositRuleID(ctx, database.UpdateDepositRuleIDParams{ID: d.ID, RuleID: ruleID}); err != nil {
				s.logger.Warn("Failed to update deposit rule_id", "deposit_id", d.ID, "error", err)
			}
			s.PersistAuditLog(ctx, realmID, d.ID, result)
		}
	}

	return nil
}

func (s *RuleEngineService) mapPurchaseToTransaction(p database.GetOrphanedPurchasesRow) ruleEngine.Transaction {
	amount, _ := p.TotalAmount.Float64Value()
	return ruleEngine.Transaction{
		ID:        fmt.Sprintf("%v", p.ID),
		EntityID:  p.RealmID,
		Amount:    amount.Float64,
		Direction: ruleEngine.Outflow,
		Date:      p.TxnDate.Time,
		Vendor:    p.VendorName.String,
		SourceAccount: p.SourceAccountName.String,
	}
}

func (s *RuleEngineService) mapDepositToTransaction(d database.GetOrphanedDepositsRow) ruleEngine.Transaction {
	amount, _ := d.TotalAmount.Float64Value()
	return ruleEngine.Transaction{
		ID:        fmt.Sprintf("%v", d.ID),
		EntityID:  d.RealmID,
		Amount:    amount.Float64,
		Direction: ruleEngine.Inflow,
		Date:      d.TxnDate.Time,
		Customer:  d.CustomerName.String,
		SourceAccount: d.SourceAccountName.String,
	}
}

// getActiveRules fetches all rule groups and conditions for a realm, compiles them,
// and assembles them into a nested tree structure.
func (s *RuleEngineService) getActiveRules(ctx context.Context, realmID string) ([]*ruleEngine.RuleGroup, error) {
	cacheKey := fmt.Sprintf("rules:%s", realmID)

	if val, found := s.cache.Get(cacheKey); found {
		if rules, ok := val.([]*ruleEngine.RuleGroup); ok {
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

	groupMap := make(map[int32]*ruleEngine.RuleGroup, len(dbGroups))
	for _, g := range dbGroups {
		rg := &ruleEngine.RuleGroup{
			ID:             int(g.ID),
			Name:           g.Name,
			Logic:          ruleEngine.LogicChoice(g.Logic),
			Priority:       int(g.Priority),
			Keywords:       g.Keywords.String,
			Active:         g.Active,
			TargetEntityID: g.TargetEntityID,
			RequiresReview: g.RequiresReview,
			Direction:      ruleEngine.CashDirection(g.Direction),
		}

		if len(g.Allocations) > 0 {
			if err := json.Unmarshal(g.Allocations, &rg.Allocations); err != nil {
				s.logger.Warn("Failed to unmarshal allocations", "rule_id", g.ID, "error", err)
			}
		}
		groupMap[g.ID] = rg
	}

	for _, c := range dbConditions {
		if group, ok := groupMap[c.RuleGroupID]; ok {
			group.Conditions = append(group.Conditions, &ruleEngine.RuleCondition{
				ID:       int(c.ID),
				Field:    ruleEngine.Field(c.Field),
				Operator: ruleEngine.Operator(c.Operator),
				Value:    c.Value,
			})
		}
	}

	var topLevelRules []*ruleEngine.RuleGroup
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
	validRules := make([]*ruleEngine.RuleGroup, 0, len(topLevelRules))
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

// CreateRule creates a new rule group and its conditions/children, ensuring strict idempotency.
func (s *RuleEngineService) CreateRule(ctx context.Context, req ruleEngine.CreateRuleRequest) error {
	activeRules, err := s.getActiveRules(ctx, req.RealmID)
	if err != nil {
		return fmt.Errorf("failed to fetch rules for idempotency check: %w", err)
	}

	for _, existingRule := range activeRules {
		if s.isLogicalDuplicate(existingRule, req) {
			s.logger.Debug("Idempotent rule creation: exact rule already exists, skipping.", "rule_name", req.Name)
			return nil
		}
	}

	// Start recursive insertion with a null ParentID
	if err := s.insertRuleRecursive(ctx, req, pgtype.Int4{}); err != nil {
		return err
	}

	cacheKey := fmt.Sprintf("rules:%s", req.RealmID)
	s.cache.Del(cacheKey)
	return nil
}

func (s *RuleEngineService) insertRuleRecursive(ctx context.Context, req ruleEngine.CreateRuleRequest, parentID pgtype.Int4) error {
	allocBytes, err := json.Marshal(req.Allocations)
	if err != nil {
		return fmt.Errorf("failed to marshal allocations: %w", err)
	}

	// Insert the Group
	group, err := s.q.CreateRuleGroup(ctx, database.CreateRuleGroupParams{
		RealmID:        req.RealmID,
		Name:           req.Name,
		Logic:          string(req.Logic),
		Priority:       int32(req.Priority),
		Active:         req.Active,
		TargetEntityID: req.TargetEntityID,
		Allocations:    allocBytes,
		RequiresReview: req.RequiresReview,
		ParentID:       parentID, // Links to parent if this is a nested child group
		Direction:      string(req.Direction),
	})
	if err != nil {
		return fmt.Errorf("failed to insert rule group: %w", err)
	}

	// Insert its Conditions
	for _, cond := range req.Conditions {
		if _, err := s.q.CreateRuleCondition(ctx, database.CreateRuleConditionParams{
			RuleGroupID: group.ID,
			Field:       string(cond.Field),
			Operator:    string(cond.Operator),
			Value:       cond.Value,
		}); err != nil {
			return fmt.Errorf("failed to insert rule condition: %w", err)
		}
	}

	// Recurse for Child Groups
	var currentGroupID pgtype.Int4
	currentGroupID.Scan(group.ID)

	for _, childReq := range req.ChildGroups {
		// Ensure children inherit the realm ID
		childReq.RealmID = req.RealmID
		childReq.Direction = req.Direction
		if err := s.insertRuleRecursive(ctx, childReq, currentGroupID); err != nil {
			return err
		}
	}

	return nil
}

// isLogicalDuplicate compares an incoming request against an existing compiled rule.
func (s *RuleEngineService) isLogicalDuplicate(existing *ruleEngine.RuleGroup, req ruleEngine.CreateRuleRequest) bool {
	// Top-level properties must match
	if !uuidEqual(existing.TargetEntityID, req.TargetEntityID) {
		return false
	}
	if existing.RequiresReview != req.RequiresReview {
		return false
	}
	if string(existing.Logic) != string(req.Logic) {
		return false
	}
	if existing.Direction != req.Direction {
		return false
	}

	// Allocations must match
	if len(existing.Allocations) != len(req.Allocations) {
		return false
	}
	for i, alloc := range existing.Allocations {
		if !uuidEqual(alloc.AccountID, req.Allocations[i].AccountID) || alloc.Percentage != req.Allocations[i].Percentage {
			return false
		}
	}

	// Conditions must match
	if len(existing.Conditions) != len(req.Conditions) {
		return false
	}
	for i, existingCond := range existing.Conditions {
		reqCond := req.Conditions[i]
		if string(existingCond.Field) != string(reqCond.Field) ||
			string(existingCond.Operator) != string(reqCond.Operator) ||
			existingCond.Value != reqCond.Value {
			return false
		}
	}

	// Children must match (Recursion)
	if len(existing.Children) != len(req.ChildGroups) {
		return false
	}
	for i, existingChild := range existing.Children {
		reqChild := req.ChildGroups[i]
		// Recursively check the child
		if !s.isLogicalDuplicate(existingChild, reqChild) {
			return false
		}
	}

	return true
}

func uuidEqual(a, b pgtype.UUID) bool {
	if a.Valid != b.Valid {
		return false
	}
	if !a.Valid {
		return true
	}
	return bytes.Equal(a.Bytes[:], b.Bytes[:])
}

// RuleResult contains the match outcome and target UUIDs for the caller to use.
// Explanation carries the full structured trace for deferred audit-log persistence.
type RuleResult struct {
	MatchedRuleGroupID *int32
	TargetEntityID     pgtype.UUID
	Allocations        []ruleEngine.Allocation
	RequiresReview     bool
	Explanation        ruleEngine.MatchExplanation
}

// EvaluateTransaction runs the transaction through the rule engine to find a match.
// Audit-log persistence is NOT performed here; the caller must invoke PersistAuditLog
// after a proposed_transactions row exists.
func (s *RuleEngineService) EvaluateTransaction(ctx context.Context, tx ruleEngine.Transaction) (*RuleResult, error) {
	rules, err := s.getActiveRules(ctx, tx.EntityID)
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return nil, nil
	}

	candidates := ruleEngine.FilterCandidates(tx, rules)

	for _, rule := range candidates {
		matched, exp := rule.Evaluate(tx)
		if matched {
			id := int32(rule.ID)
			return &RuleResult{
				MatchedRuleGroupID: &id,
				TargetEntityID:     rule.TargetEntityID,
				Allocations:        rule.Allocations,
				RequiresReview:     rule.RequiresReview,
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
