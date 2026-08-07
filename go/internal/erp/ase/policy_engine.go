package ase

import (
	"fmt"
	"time"
)

type NodeType string

const (
	ConditionNode NodeType = "condition"
	ActionNode    NodeType = "action"
)

// DecisionNode represents a node in the recovery policy decision tree.
// It is designed to be easily unmarshaled from JSON/YAML configurations.
type DecisionNode struct {
	Type             NodeType      `yaml:"type" json:"type"`
	PredicateName    string        `yaml:"predicate,omitempty" json:"predicate,omitempty"`
	TrueBranch       *DecisionNode `yaml:"true_branch,omitempty" json:"true_branch,omitempty"`
	FalseBranch      *DecisionNode `yaml:"false_branch,omitempty" json:"false_branch,omitempty"`
	PermittedActions []string      `yaml:"actions,omitempty" json:"actions,omitempty"`
}

// RecoveryAction defines the cost and probability characteristics of a specific recovery action.
type RecoveryAction struct {
	ActionID        string
	Description     string
	BaseCost        float64 // Normalized composite cost (compute + human)
	ExpectedLatency time.Duration
	ProbOfSuccess   float64 // Historical success rate P(S|a)
	BaseExpectedIG  float64 // Baseline Expected Information Gain
}

// RecoveryPolicyEngine interprets decision trees and selects the best recovery action based on cost matrix.
type RecoveryPolicyEngine struct {
	actions    map[string]RecoveryAction
	predicates map[string]func(node *AutonomousSemanticEngineNode) bool
}

var (
	DefaultRecoveryPolicyEngine = NewRecoveryPolicyEngine()
	DefaultRecoveryExecutor     RecoveryExecutor
)

func NewRecoveryPolicyEngine() *RecoveryPolicyEngine {
	engine := &RecoveryPolicyEngine{
		actions:    make(map[string]RecoveryAction),
		predicates: make(map[string]func(node *AutonomousSemanticEngineNode) bool),
	}
	engine.registerDefaults()
	return engine
}

func (e *RecoveryPolicyEngine) RegisterAction(action RecoveryAction) {
	e.actions[action.ActionID] = action
}

func (e *RecoveryPolicyEngine) RegisterPredicate(name string, fn func(node *AutonomousSemanticEngineNode) bool) {
	e.predicates[name] = fn
}

// registerDefaults sets up the base registry for actions and common predicates.
func (e *RecoveryPolicyEngine) registerDefaults() {
	// Example default actions based on the PRD
	e.RegisterAction(RecoveryAction{
		ActionID:        "search_document_store",
		Description:     "Query internal database for attachables",
		BaseCost:        0.1,
		ExpectedLatency: 100 * time.Millisecond,
		ProbOfSuccess:   0.7,
		BaseExpectedIG:  0.8,
	})
	e.RegisterAction(RecoveryAction{
		ActionID:        "queue_client_request",
		Description:     "Queue an email request for missing context",
		BaseCost:        0.5,
		ExpectedLatency: 24 * time.Hour,
		ProbOfSuccess:   0.95,
		BaseExpectedIG:  0.9,
	})
	e.RegisterAction(RecoveryAction{
		ActionID:        "human_review",
		Description:     "Escalate to a human CPA",
		BaseCost:        1.0,
		ExpectedLatency: 4 * time.Hour,
		ProbOfSuccess:   1.0,
		BaseExpectedIG:  1.0,
	})

	// Example base predicates
	e.RegisterPredicate("always_true", func(n *AutonomousSemanticEngineNode) bool { return true })
	e.RegisterPredicate("always_false", func(n *AutonomousSemanticEngineNode) bool { return false })
}

// SelectedRecoveryAction represents the result of evaluating the tree and cost matrix.
type SelectedRecoveryAction struct {
	Action        RecoveryAction
	ExpectedValue float64
}

// EvaluateTree traverses the decision tree and calculates EV to find the best permitted action.
func (e *RecoveryPolicyEngine) EvaluateTree(root *DecisionNode, node *AutonomousSemanticEngineNode) (*SelectedRecoveryAction, error) {
	if root == nil {
		return nil, fmt.Errorf("root node is nil")
	}

	permittedActions, err := e.traverse(root, node)
	if err != nil {
		return nil, err
	}

	if len(permittedActions) == 0 {
		return nil, fmt.Errorf("no permitted actions resulted from decision tree")
	}

	var bestAction *SelectedRecoveryAction
	for _, actionID := range permittedActions {
		action, exists := e.actions[actionID]
		if !exists {
			// Log missing action but continue evaluating others
			if node.logger != nil {
				node.logger.Warn("recovery policy: unknown action ID", "action_id", actionID)
			}
			continue
		}

		ev := CalculateExpectedValue(action.ProbOfSuccess, action.BaseExpectedIG, action.BaseCost)
		if bestAction == nil || ev > bestAction.ExpectedValue {
			bestAction = &SelectedRecoveryAction{
				Action:        action,
				ExpectedValue: ev,
			}
		}
	}

	if bestAction == nil {
		return nil, fmt.Errorf("failed to evaluate expected value for permitted actions")
	}

	return bestAction, nil
}

func (e *RecoveryPolicyEngine) traverse(n *DecisionNode, node *AutonomousSemanticEngineNode) ([]string, error) {
	switch n.Type {
	case ActionNode:
		return n.PermittedActions, nil
	case ConditionNode:
		fn, exists := e.predicates[n.PredicateName]
		if !exists {
			return nil, fmt.Errorf("unknown predicate: %s", n.PredicateName)
		}
		if fn(node) {
			if n.TrueBranch == nil {
				return nil, fmt.Errorf("true branch missing for predicate: %s", n.PredicateName)
			}
			return e.traverse(n.TrueBranch, node)
		} else {
			if n.FalseBranch == nil {
				return nil, fmt.Errorf("false branch missing for predicate: %s", n.PredicateName)
			}
			return e.traverse(n.FalseBranch, node)
		}
	default:
		return nil, fmt.Errorf("unknown node type: %s", n.Type)
	}
}
