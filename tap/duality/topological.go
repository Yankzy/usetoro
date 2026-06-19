package duality

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// GuardrailConstraint defines boundaries for execution safety (budgets, financial limits).
type GuardrailConstraint struct {
	MaxFinancialLimit float64  `json:"max_financial_limit"`
	MaxTokenBudget    int64    `json:"max_token_budget"`
	RequiredApprovers []string `json:"required_approvers"`
}

// GuardrailDAG represents the mirror safety topology mapping boundaries to execution node IDs.
type GuardrailDAG struct {
	Constraints map[string]GuardrailConstraint
	mu          sync.RWMutex
}

// NewGuardrailDAG constructs an empty safety topology.
func NewGuardrailDAG() *GuardrailDAG {
	return &GuardrailDAG{
		Constraints: make(map[string]GuardrailConstraint),
	}
}

// RegisterConstraint binds safety thresholds to a specific execution step.
func (g *GuardrailDAG) RegisterConstraint(stepID string, constraint GuardrailConstraint) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Constraints[stepID] = constraint
}

// CheckTransition evaluates step payload details against the guardrail dual.
func (g *GuardrailDAG) CheckTransition(ctx context.Context, stepID string, payload []byte) error {
	g.mu.RLock()
	constraint, exists := g.Constraints[stepID]
	g.mu.RUnlock()

	if !exists {
		return nil // No guardrail configured for this node
	}

	// Unmarshal and evaluate financial limits
	var financialData struct {
		Amount float64 `json:"amount"`
	}
	if err := json.Unmarshal(payload, &financialData); err == nil {
		if constraint.MaxFinancialLimit > 0 && financialData.Amount > constraint.MaxFinancialLimit {
			return fmt.Errorf("guardrail violation at step %s: transaction amount %.2f exceeds limit %.2f",
				stepID, financialData.Amount, constraint.MaxFinancialLimit)
		}
	}

	// Unmarshal and evaluate token usage budget limits
	var tokenData struct {
		TokenUsage int64 `json:"token_usage"`
	}
	if err := json.Unmarshal(payload, &tokenData); err == nil {
		if constraint.MaxTokenBudget > 0 && tokenData.TokenUsage > constraint.MaxTokenBudget {
			return fmt.Errorf("guardrail violation at step %s: token usage %d exceeds budget %d",
				stepID, tokenData.TokenUsage, constraint.MaxTokenBudget)
		}
	}

	return nil
}

// ConcurrentGate intercepts the execution graph progression, blocking unless the guardrail dual signs off.
func (g *GuardrailDAG) ConcurrentGate(ctx context.Context, stepID string, payload []byte) error {
	errChan := make(chan error, 1)

	go func() {
		defer close(errChan)
		if err := g.CheckTransition(ctx, stepID, payload); err != nil {
			errChan <- err
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errChan:
		if err != nil {
			return fmt.Errorf("topological barrier tripped at node %s: %w", stepID, err)
		}
	}

	return nil
}
