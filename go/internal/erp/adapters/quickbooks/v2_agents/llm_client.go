package v2_agents

import (
	"context"

	"github.com/Yankzy/usetoro/internal/services/ai"
)

// LLMClient is the minimal LLM interface required by V2 agents.
// This exists so agents can be tested with mocks without depending on the concrete ai.LLMClient.
type LLMClient interface {
	GenerateJSON(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error
}

// Ensure the concrete client satisfies the interface.
var _ LLMClient = (*ai.LLMClient)(nil)
