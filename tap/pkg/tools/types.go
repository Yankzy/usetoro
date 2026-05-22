// Package tools provides a NATS-free framework for general-purpose LLM agents
// with dynamic tool calling, sub-agent spawning, and context isolation.
package tools

import (
	"context"
	"encoding/json"
)

// Tool is the interface every tool implements.
type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage // JSON Schema for parameters
	Call(ctx context.Context, input map[string]any) (string, error)
}

// ToolCall represents a single tool invocation requested by the LLM.
type ToolCall struct {
	ID    string
	Name  string
	Input map[string]any
	Async bool // run_in_background hint
}

// ToolResult contains the output of a tool execution.
type ToolResult struct {
	ToolCallID string
	Content    string
	Error      string
}

// AgentDefinition maps a type name to a tool allowlist and behavioral defaults.
type AgentDefinition struct {
	Type            string   // "general-purpose", "explore", "plan"
	Tools           []string // tool allowlist; ["*"] means all
	DisallowedTools []string // explicit denylist
	Model           string   // model override or "" to inherit parent
	Background      bool     // if true, always run async
	MaxTurns        int      // 0 = unlimited
	SystemPrompt    string   // agent-specific system prompt appended to parent context
}

// Message is a single turn in the LLM conversation.
type Message struct {
	Role       string     // "user", "assistant", "tool"
	Content    string     // text content
	ToolCalls  []ToolCall // populated when role="assistant" with tool calls
	ToolCallID string     // populated when role="tool" (matches ToolCall.ID)
}

// AgentContext carries the isolated execution scope for a single agent invocation.
type AgentContext struct {
	AgentID   string
	ParentID  string // empty for the root agent
	AgentType string // matches AgentDefinition.Type
	Abort     context.Context
	TurnCount int
	MaxTurns  int
}

// AgentResult is the final output of an agent run.
type AgentResult struct {
	Output   string    // final text response from the LLM
	Messages []Message // full conversation history
}

// LLMCallFunc is a function that sends messages and tool definitions to an LLM,
// handles all tool-calling internally, and returns the final text response.
// This abstraction allows the tools package to remain NATS/LLM-provider-free
// while the general_agent package wires it to Runtime.ExecWithToolCalling.
type LLMCallFunc func(ctx context.Context, messages []Message, tools []Tool) (string, error)
