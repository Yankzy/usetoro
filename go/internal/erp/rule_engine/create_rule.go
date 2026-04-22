package ruleEngine

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

// RuleCreator exposes the interface for creating rules in an idempotent way.
type RuleCreator interface {
	CreateRule(ctx context.Context, req CreateRuleRequest) error
}

// CreateRuleRequest describes the payload required to safely create a rule.
type CreateRuleRequest struct {
	RealmID        string
	Name           string
	Logic          LogicChoice
	Priority       int
	Active         bool
	TargetEntityID pgtype.UUID
	Allocations    []Allocation
	RequiresReview bool
	Conditions     []RuleConditionRequest
}

// RuleConditionRequest describes a single condition on a rule group.
type RuleConditionRequest struct {
	Field    Field
	Operator Operator
	Value    string
}
