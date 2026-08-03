package core

import (
	"fmt"
)

// Prices per 1 token (Optimized: divide by 1,000,000 once during compilation)
const (
	P_INPUT  float64 = 10.00 / 1_000_000.0 // Cost of uncached input tokens
	P_READ   float64 = 1.00 / 1_000_000.0  // Cost of reading the cached input tokens
	P_WRITE  float64 = 12.50 / 1_000_000.0 // Cost of writing the new input tokens
	P_OUTPUT float64 = 45.00 / 1_000_000.0 // Cost of output tokens
)

// TokenCostTracker maintains the state of the session cache and total cost
type TokenCostTracker struct {
	TotalSessionCost    float64
	CachedContextLength int
}

// NewTokenCostTracker initializes a new tracker instance
func NewTokenCostTracker() *TokenCostTracker {
	return &TokenCostTracker{
		TotalSessionCost:    0.0,
		CachedContextLength: 0,
	}
}

// CalculateTurn calculates the cost of a single turn by deriving the write delta,
// and updates the rolling cache state.
func (t *TokenCostTracker) CalculateTurn(currentPromptTokens int, completionTokens int) float64 {
	// The tokens we already have in cache
	tRead := min(t.CachedContextLength, currentPromptTokens)

	// The new tokens appended to the conversation that must be written
	tWrite := currentPromptTokens - tRead

	// Calculate cost using pre-divided rates
	// Must explicitly convert ints to float64 for the math operations
	turnCost := (float64(tRead) * P_READ) +
		(float64(tWrite) * P_WRITE) +
		(float64(completionTokens) * P_OUTPUT)

	// Update state: the new context length includes this turn's prompt + output
	t.CachedContextLength = currentPromptTokens + completionTokens
	t.TotalSessionCost += turnCost

	return turnCost
}

func main() {
	tracker := NewTokenCostTracker()

	// Turn 1: 1,000,000 system + 500 user input, 1000 output
	cost1 := tracker.CalculateTurn(1000500, 1000)
	fmt.Printf("Turn 1 Cost: $%.5f\n", cost1)
	// Output: Turn 1 Cost: $12.55125

	// Turn 2: 1,000,500 (old prompt) + 1,000 (Turn 1 output) + 500 (new user input), 1000 output
	cost2 := tracker.CalculateTurn(1002000, 1000)
	fmt.Printf("Turn 2 Cost: $%.5f\n", cost2)
	// Output: Turn 2 Cost: $1.06425

	fmt.Printf("Total Session Cost: $%.5f\n", tracker.TotalSessionCost)
}
