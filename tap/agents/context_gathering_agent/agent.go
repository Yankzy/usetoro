package context_gathering_agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/tap/agents"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/Yankzy/usetoro/tap/pkg/tools/builtin"
)

func init() {
	agents.Register("context-gathering-agent", NewContextGatheringAgent)
}

type ContextGatheringAgent struct {
	*agent.BaseAgent
	RT           *agent.Runtime
	toolRegistry *tools.AgentRegistry
	taskManager  *tools.TaskManager
	toolMap      map[string]tools.Tool
}

func NewContextGatheringAgent(env core.Environment) core.Runnable {
	logger := env.Logger.With("agent", "context-gathering")

	registry := tools.NewAgentRegistry()
	registry.RegisterBuiltIns()

	taskMgr := tools.NewTaskManager()

	allTools := make(map[string]tools.Tool)

	// Configure built-in tools or async tools from config
	for _, toolCfg := range env.Config.Tools {
		if toolCfg.ActivityType != "" {
			allTools[toolCfg.Name] = &builtin.AsyncWorkerTool{
				Bus:          env.Bus,
				AgentDID:     env.Config.DID,
				ToolName:     toolCfg.Name,
				ToolDesc:     toolCfg.Description,
				Schema:       json.RawMessage(toolCfg.InputSchema),
				ActivityType: toolCfg.ActivityType,
			}
		} else {
			if builtinTool := builtin.GetTool(toolCfg.Name, env, logger); builtinTool != nil {
				allTools[toolCfg.Name] = builtinTool
			} else {
				logger.Warn("Tool requested in config but not found in builtin registry", "tool", toolCfg.Name)
			}
		}
	}

	base := agent.NewBaseAgent(logger, env.Bus, env.Config, nil)
	cga := &ContextGatheringAgent{
		BaseAgent:    base,
		RT:           agent.NewRuntime(logger, env.Bus, env.Config),
		toolRegistry: registry,
		taskManager:  taskMgr,
		toolMap:      allTools,
	}

	// We set the handler to process NATS requests
	base.Handler = func(msg *nats.Msg) {
		cga.handleMessage(msg, env, msg.Reply)
	}

	return cga
}

// makeLLMCallFunc bridges tools to Runtime.ExecWithToolCalling
func (cga *ContextGatheringAgent) makeLLMCallFunc() tools.LLMCallFunc {
	return func(ctx context.Context, messages []tools.Message, tlz []tools.Tool) (string, []tools.Message, error) {
		msgInputs := make([]agent.MessageInput, 0, len(messages))
		for _, m := range messages {
			input := agent.MessageInput{
				Role:       m.Role,
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
			}
			if len(m.ToolCalls) > 0 {
				input.ToolCalls = make([]agent.ToolCallInput, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					args := tc.RawArguments
					if args == "" {
						b, _ := json.Marshal(tc.Input)
						args = string(b)
					}
					input.ToolCalls[i] = agent.ToolCallInput{
						ID:        tc.ID,
						Name:      tc.Name,
						Arguments: args,
					}
				}
			}
			msgInputs = append(msgInputs, input)
		}

		toolDefs := make([]agent.ToolDef, 0, len(tlz))
		for _, t := range tlz {
			var schema map[string]any
			if err := json.Unmarshal(t.InputSchema(), &schema); err != nil {
				schema = map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				}
			}
			toolDefs = append(toolDefs, agent.ToolDef{
				Name:        t.Name(),
				Description: t.Description(),
				Schema:      schema,
			})
		}

		toolMap := make(map[string]tools.Tool, len(tlz))
		for _, t := range tlz {
			toolMap[t.Name()] = t
		}

		toolHandler := func(tctx context.Context, name string, args map[string]any) (string, error) {
			t, ok := toolMap[name]
			if !ok {
				return "", fmt.Errorf("unknown tool: %s", name)
			}
			return t.Call(tctx, args)
		}

		output, newAgentMsgs, err := cga.RT.ExecWithMessages(ctx, msgInputs, toolDefs, toolHandler)
		if err != nil {
			return "", nil, err
		}

		var newMsgs []tools.Message
		for _, m := range newAgentMsgs {
			var tc []tools.ToolCall
			if len(m.ToolCalls) > 0 {
				tc = make([]tools.ToolCall, len(m.ToolCalls))
				for i, inputTc := range m.ToolCalls {
					var inputMap map[string]any
					_ = json.Unmarshal([]byte(inputTc.Arguments), &inputMap)
					tc[i] = tools.ToolCall{
						ID:           inputTc.ID,
						Name:         inputTc.Name,
						Input:        inputMap,
						RawArguments: inputTc.Arguments,
					}
				}
			}
			newMsgs = append(newMsgs, tools.Message{
				Role:       m.Role,
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
				ToolCalls:  tc,
			})
		}
		return output, newMsgs, nil
	}
}

func (cga *ContextGatheringAgent) handleMessage(msg *nats.Msg, env core.Environment, replySubject string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	var envlp core.Envelope
	if err := json.Unmarshal(msg.Data, &envlp); err != nil {
		cga.BaseAgent.Logger.Error("failed to unmarshal envelope", "error", err)
		msg.Term()
		return
	}

	if envlp.Performative != core.REQUEST && envlp.Performative != core.INFORM {
		msg.Ack()
		return
	}

	cga.BaseAgent.Logger.Info("Mark (Context Gathering Agent) received task", "performative", envlp.Performative, "from", envlp.SenderDID)

	if envlp.ConversationID != "" {
		ctx = context.WithValue(ctx, tools.ConversationIDKey{}, envlp.ConversationID)
	}

	// Extract prompt from payload
	var payload map[string]any
	var prompt string
	if err := json.Unmarshal(envlp.Body, &payload); err == nil {
		if p, ok := payload["prompt"].(string); ok {
			prompt = p
		} else {
			b, _ := json.Marshal(payload)
			prompt = string(b)
		}
	} else {
		prompt = string(envlp.Body)
	}

	if envlp.Performative == core.INFORM {
		prompt = "Worker asynchronous task completed. Result:\n" + prompt
	}

	// Resolve tools
	toolList := make([]tools.Tool, 0, len(cga.toolMap))
	for _, t := range cga.toolMap {
		toolList = append(toolList, t)
	}

	systemPrompt := "You are Mark, the virtual bookkeeping employee at Toro. Your job is to gather context for transactions that the automated system found ambiguous. You are polite, highly detailed, and act like a real accounting assistant. You have tools to fetch QuickBooks Online (QBO) documents (like invoices and bills) and tools to email or message humans for missing context or receipts. When handed an ambiguous transaction, first search QBO if an entity ID is referenced. If you still lack context, immediately reach out to a human. IMPORTANT: The default tool for asking humans for context must be the `email` tool. Only use other messaging tools as a fallback."

	messages := []tools.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	}

	agentCtx := tools.NewAgentContext("context-gathering", 50)
	llmFunc := cga.makeLLMCallFunc()

	cga.BaseAgent.Logger.Info("Mark is analyzing transaction", "tools_available", len(toolList))

	result, err := tools.RunAgent(ctx, agentCtx, toolList, messages, llmFunc)
	if err != nil {
		cga.BaseAgent.Logger.Error("agent run failed", "error", err)
		msg.Ack() // In production, we'd reply with FAILURE
		return
	}

	finalOutput := result.Output

	// Reply to sender
	replyEnv := core.Envelope{
		ID:             uuid.New().String(),
		Timestamp:      time.Now(),
		SenderDID:      env.Config.DID,
		ReceiverDID:    envlp.SenderDID,
		Performative:   core.INFORM,
		ConversationID: envlp.ConversationID,
		Body:           mustMarshal(map[string]string{"output": finalOutput}),
	}
	replyBytes, _ := json.Marshal(replyEnv)

	targetTopic := replySubject
	if targetTopic == "" {
		targetTopic = core.BuildAgentInbox(envlp.SenderDID)
	}
	if err := env.Bus.Publish(targetTopic, replyBytes); err != nil {
		cga.BaseAgent.Logger.Error("failed to publish INFORM", "error", err)
		msg.Nak()
		return
	}

	cga.BaseAgent.Logger.Info("Mark finished context gathering", "output_len", len(finalOutput))
	msg.Ack()
}

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
