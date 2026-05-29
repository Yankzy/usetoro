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
	select {
	case <-agentCtx.Abort.Done():
		return nil, fmt.Errorf("agent %s aborted: %w", agentCtx.AgentID, agentCtx.Abort.Err())
	case <-ctx.Done():
		return nil, fmt.Errorf("agent %s context done: %w", agentCtx.AgentID, ctx.Err())
	default:
	}

	output, newMsgs, err := llmFunc(ctx, messages, tools)
	if err != nil {
		return nil, fmt.Errorf("llm call: %w", err)
	}

	if len(newMsgs) > 0 {
		messages = append(messages, newMsgs...)
	} else {
		// Fallback: if the LLMFunc didn't generate structured tool messages,
		// just append the final text response. (For backwards compatibility with old tests)
		messages = append(messages, Message{
			Role:    "assistant",
			Content: output,
		})
	}

	if QueryLogger != nil {
		QueryLogger.Info("agent finished",
			"agent_id", agentCtx.AgentID,
			"agent_type", agentCtx.AgentType,
		)
	}

	return &AgentResult{
		Output:   output,
		Messages: messages,
	}, nil
}
