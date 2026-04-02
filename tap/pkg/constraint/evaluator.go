package constraint

import (
	"context"
	"fmt"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/reputation"
)

// DefaultEvaluator processes all registered rules.
type DefaultEvaluator struct {
	Rules []Rule
}

// NewDefaultEvaluator initializes the rule engine.
func NewDefaultEvaluator(repEngine reputation.Engine) *DefaultEvaluator {
	return &DefaultEvaluator{
		Rules: []Rule{
			&TrustThresholdRule{repEngine: repEngine, minConfidence: 0.2, minScore: -0.5},
			&CapabilityMatchRule{}, 
		},
	}
}

// Evaluate loops through all active rules and MUST pass all of them.
func (e *DefaultEvaluator) Evaluate(ctx context.Context, contract *core.Contract) (bool, error) {
	for _, rule := range e.Rules {
		pass, err := rule.Check(ctx, contract)
		if err != nil {
			return false, fmt.Errorf("rule %s encountered error: %w", rule.Type(), err)
		}
		if !pass {
			return false, fmt.Errorf("rule %s failed constraint check", rule.Type())
		}
	}
	return true, nil
}
