package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Yankzy/usetoro/tap/pkg/tools"
)

// AgentTool spawns sub-agents in-process with restricted tool pools.
// This is Mechanism A — ephemeral goroutine sub-agents for general exploration,
// research, and planning. For specialized agents registered in the Almanac,
// use the DelegateTool (Mechanism B, defined in the general_agent package).
type AgentTool struct {
	Registry    *tools.AgentRegistry
	TaskManager *tools.TaskManager
	LLMFunc     tools.LLMCallFunc // wired to Runtime.ExecWithToolCalling
	AllTools    map[string]tools.Tool
}

func (t *AgentTool) Name() string { return "Agent" }
func (t *AgentTool) Description() string {
	return "Spawn a sub-agent to handle a specific task. Sub-agents have restricted tool access based on their type. Use 'explore' for codebase exploration, 'plan' for architecture planning, or 'general-purpose' for tasks requiring the full tool set."
}
func (t *AgentTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"description": {"type": "string", "description": "Short description of what the sub-agent should do"},
			"prompt": {"type": "string", "description": "The task for the sub-agent to perform"},
			"subagent_type": {"type": "string", "description": "Agent type: 'general-purpose', 'explore', or 'plan'. Default: 'general-purpose'"},
			"model": {"type": "string", "description": "Model override (optional, inherits parent model if not set)"},
			"run_in_background": {"type": "boolean", "description": "If true, run asynchronously and return immediately with an agent ID"}
		},
		"required": ["description", "prompt"]
	}`)
}

func (t *AgentTool) Call(ctx context.Context, input map[string]any) (string, error) {
	description, _ := input["description"].(string)
	prompt, ok := input["prompt"].(string)
	if !ok || prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}

	agentType := "general-purpose"
	if at, ok := input["subagent_type"].(string); ok && at != "" {
		agentType = at
	}

	runInBackground := false
	if bg, ok := input["run_in_background"].(bool); ok {
		runInBackground = bg
	}

	def, ok := t.Registry.Get(agentType)
	if !ok {
		return "", fmt.Errorf("unknown agent type: %s", agentType)
	}

	subTools := t.Registry.ResolveTools(def, t.AllTools, "Agent")

	maxTurns := def.MaxTurns
	if maxTurns == 0 {
		maxTurns = 25
	}

	subAgentCtx := tools.NewAgentContext(agentType, maxTurns)

	messages := []tools.Message{
		{Role: "user", Content: prompt},
	}
	if def.SystemPrompt != "" {
		messages = append([]tools.Message{
			{Role: "system", Content: def.SystemPrompt},
		}, messages...)
	}

	subToolsList := make([]tools.Tool, 0, len(subTools))
	for _, tool := range subTools {
		subToolsList = append(subToolsList, tool)
	}

	if runInBackground {
		bgCtx, cancel := context.WithCancel(context.Background())
		agentID := t.TaskManager.Register("agent", description, cancel)

		go func() {
			defer cancel()
			result, err := tools.RunAgent(bgCtx, subAgentCtx, subToolsList, messages, t.LLMFunc)
			if err != nil {
				t.TaskManager.Update(agentID, "failed", "", err.Error())
				return
			}
			t.TaskManager.Update(agentID, "completed", result.Output, "")
		}()

		out, _ := json.Marshal(map[string]string{
			"agent_id": agentID,
			"status":   "running",
			"message":  fmt.Sprintf("Sub-agent %s started in background. Use TaskManager to check status.", agentID),
		})
		return string(out), nil
	}

	result, err := tools.RunAgent(ctx, subAgentCtx, subToolsList, messages, t.LLMFunc)
	if err != nil {
		return "", fmt.Errorf("sub-agent %s failed: %w", agentType, err)
	}

	return result.Output, nil
}
