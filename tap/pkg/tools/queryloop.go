package tools

import (
	"context"
	"fmt"
	"log/slog"
)

// DefaultMaxTurns is the fallback turn limit when AgentDefinition.MaxTurns is 0.
const DefaultMaxTurns = 50

// QueryLogger is an optional structured logger for tracing agent execution.
var QueryLogger *slog.Logger

// RunAgent executes the main LLM query loop. It:
//
//  1. Calls llmFunc with messages and tool definitions
//  2. llmFunc handles all tool calling internally (via Runtime.ExecWithToolCalling)
//  3. Returns the final text response
//
// RunAgent is recursive — the AgentTool calls it with a child AgentContext
// and a restricted tool set.
func RunAgent(ctx context.Context, agentCtx AgentContext, tools []Tool, messages []Message, llmFunc LLMCallFunc) (*AgentResult, error) {
	maxTurns := agentCtx.MaxTurns
	if maxTurns <= 0 {
		maxTurns = DefaultMaxTurns
	}

	for turn := 0; turn < maxTurns; turn++ {
		select {
		case <-agentCtx.Abort.Done():
			return nil, fmt.Errorf("agent %s aborted: %w", agentCtx.AgentID, agentCtx.Abort.Err())
		case <-ctx.Done():
			return nil, fmt.Errorf("agent %s context done: %w", agentCtx.AgentID, ctx.Err())
		default:
		}

		output, err := llmFunc(ctx, messages, tools)
		if err != nil {
			return nil, fmt.Errorf("llm call (turn %d): %w", turn, err)
		}

		messages = append(messages, Message{
			Role:    "assistant",
			Content: output,
		})

		if QueryLogger != nil {
			QueryLogger.Info("agent finished",
				"agent_id", agentCtx.AgentID,
				"agent_type", agentCtx.AgentType,
				"turns", turn+1,
			)
		}

		return &AgentResult{
			Output:   output,
			Messages: messages,
		}, nil
	}

	return nil, fmt.Errorf("agent %s exceeded max turns (%d)", agentCtx.AgentID, maxTurns)
}
