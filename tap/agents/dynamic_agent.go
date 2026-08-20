package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/Yankzy/usetoro/tap/pkg/tools/builtin"
)

func init() {
	Register("dynamic-agent", NewDynamicAgent)
}

// DynamicAgent represents an agent that dynamically loads its prompt and tools
// from the database at runtime based on the requested agent name.
type DynamicAgent struct {
	*agent.BaseAgent
	RT      *agent.Runtime
	env     core.Environment
	queries *database.Queries
	name    string
	toolMap map[string]tools.Tool
}

// NewDynamicAgent instantiates a generic agent wrapper. The actual behavior
// (prompt, tools) is fetched at execution time.
func NewDynamicAgent(env core.Environment) core.Runnable {
	agentName := env.Config.Name
	if agentName == "" {
		agentName = "dynamic-agent"
	}

	logger := env.Logger.With("agent", agentName)

	allTools := make(map[string]tools.Tool)

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

	// Always pre-register core builtin tools
	coreBuiltins := []string{
		"get_user_workflows",
		"trigger_workflow",
		"CheckExistingDocuments",
		"QueueClientRequest",
		"FetchCommunicationHistory",
		"UpdateTransactionClassification",
		"LookupClient",
		"SendEmail",
	}
	for _, name := range coreBuiltins {
		if _, exists := allTools[name]; !exists {
			if t := builtin.GetTool(name, env, logger); t != nil {
				allTools[name] = t
			}
		}
	}

	da := &DynamicAgent{
		RT:      agent.NewRuntime(logger, env.Bus, env.Config),
		env:     env,
		queries: env.Queries, // Assumes env.Queries is injected
		name:    agentName,
		toolMap: allTools,
	}

	handler := func(msg *nats.Msg) {
		replyTo := msg.Header.Get("Toro-Reply-To")
		if replyTo == "" && msg.Reply != "" && !strings.HasPrefix(msg.Reply, "$JS.ACK.") {
			replyTo = msg.Reply
		}
		da.handleMessage(msg, env, replyTo)
	}

	da.BaseAgent = agent.NewBaseAgent(logger, env.Bus, env.Config, handler)
	return da
}

func (da *DynamicAgent) handleMessage(msg *nats.Msg, env core.Environment, replyTo string) {
	ctx := context.Background()
	var envelope core.Envelope
	if err := json.Unmarshal(msg.Data, &envelope); err != nil {
		da.Logger.Error("failed to unmarshal envelope", "error", err)
		return
	}

	if envelope.Performative != core.INFORM && envelope.Performative != core.REQUEST {
		msg.Ack()
		return
	}

	var taskDef core.TaskDefinition
	if err := json.Unmarshal(envelope.Body, &taskDef); err != nil {
		da.Logger.Error("failed to unmarshal task definition", "error", err)
		msg.Nak()
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(taskDef.Payload, &payload); err != nil {
		da.Logger.Error("failed to unmarshal payload", "error", err)
		msg.Nak()
		return
	}

	requestedAgent, _ := payload["requested_agent"].(string)
	if requestedAgent == "" {
		requestedAgent = da.name
	}

	// 1. Fetch Configuration from Database
	if da.queries == nil {
		da.Logger.Error("queries not injected")
		msg.Nak()
		return
	}

	configRec, err := da.queries.GetAgentConfigurationByName(ctx, requestedAgent)
	if err != nil {
		da.Logger.Warn("dynamic-agent configuration not found in db, using default configuration", "error", err)
		configRec = database.ToroCoreAgentConfiguration{
			Name:         "dynamic-agent",
			SdkClient:    "tap",
			SystemPrompt: "You are the Dynamic Agent. You triage incoming client emails and requests, inspect available workflows and blueprints, and trigger appropriate workflows.",
		}
	}

	if entityID, ok := payload["entity_id"].(string); ok && entityID != "" {
		ctx = context.WithValue(ctx, tools.EntityIDKey{}, entityID)
	}
	if sessionID, ok := payload["session_id"].(string); ok && sessionID != "" {
		ctx = context.WithValue(ctx, tools.SessionIDKey{}, sessionID)
	}
	if envelope.ConversationID != "" {
		ctx = context.WithValue(ctx, tools.ConversationIDKey{}, envelope.ConversationID)
	}
	ctx = context.WithValue(ctx, tools.InboundTaskPayloadKey{}, payload)

	// 2. Resolve SDK and instantiate ToolMultiplexer
	var activeTools []tools.Tool

	// Map the requested SDK to the actual injected client
	switch configRec.SdkClient {
	case "mailpool":
		if env.Mailpool != nil {
			activeTools = append(activeTools, tools.NewSDKMultiplexer(env.Mailpool.ClientInterface))
		} else {
			da.Logger.Warn("mailpool sdk requested by config but not injected")
		}
	case "tap":
		fallthrough
	default:
		if len(env.Config.Tools) > 0 {
			for _, toolCfg := range env.Config.Tools {
				if t, ok := da.toolMap[toolCfg.Name]; ok {
					activeTools = append(activeTools, t)
				} else if builtinTool := builtin.GetTool(toolCfg.Name, env, da.Logger); builtinTool != nil {
					activeTools = append(activeTools, builtinTool)
				}
			}
			// Always ensure core workflow tools are available
			for _, coreName := range []string{"get_user_workflows", "trigger_workflow"} {
				hasTool := false
				for _, at := range activeTools {
					if at.Name() == coreName {
						hasTool = true
						break
					}
				}
				if !hasTool {
					if t, ok := da.toolMap[coreName]; ok {
						activeTools = append(activeTools, t)
					} else if t := builtin.GetTool(coreName, env, da.Logger); t != nil {
						activeTools = append(activeTools, t)
					}
				}
			}
		} else {
			for _, t := range da.toolMap {
				activeTools = append(activeTools, t)
			}
		}
	}

	// 3. Convert tools for LLM
	toolDefs := make([]agent.ToolDef, 0, len(activeTools))
	toolMap := make(map[string]tools.Tool, len(activeTools))

	for _, t := range activeTools {
		var schema map[string]any
		if err := json.Unmarshal(t.InputSchema(), &schema); err != nil || schema == nil {
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
		toolMap[t.Name()] = t
	}

	toolHandler := func(tctx context.Context, name string, args map[string]any) (string, error) {
		t, ok := toolMap[name]
		if !ok {
			return "", fmt.Errorf("tool %s not found", name)
		}
		return t.Call(tctx, args)
	}

	// 4. Execute LLM Loop with Dynamic Context
	sysPrompt := configRec.SystemPrompt

	var docIDs []string
	if rawDocs, ok := payload["document_ids"].([]any); ok {
		for _, d := range rawDocs {
			if s, ok := d.(string); ok && s != "" {
				docIDs = append(docIDs, s)
			}
		}
	} else if rawDocsStr, ok := payload["document_ids"].([]string); ok {
		docIDs = rawDocsStr
	}

	userPrompt, _ := payload["prompt"].(string)
	if userPrompt == "" {
		bodyText, _ := payload["body_text"].(string)
		subject, _ := payload["subject"].(string)
		var sb strings.Builder
		if subject != "" {
			sb.WriteString(fmt.Sprintf("Subject: %s\n\n", subject))
		}
		if bodyText != "" {
			sb.WriteString(fmt.Sprintf("%s\n", bodyText))
		}
		if len(docIDs) > 0 {
			sb.WriteString(fmt.Sprintf("\nAttached Document IDs: %s\n", strings.Join(docIDs, ", ")))
		}
		userPrompt = sb.String()
		if userPrompt == "" {
			userPrompt = fmt.Sprintf("User Request: %s", string(envelope.Body))
		}
	}

	var history []agent.MessageInput
	if msgsRaw, ok := payload["messages"]; ok {
		b, err := json.Marshal(msgsRaw)
		if err == nil {
			_ = json.Unmarshal(b, &history)
		}
	}

	messages := make([]agent.MessageInput, 0, len(history)+2)
	if sysPrompt != "" {
		messages = append(messages, agent.MessageInput{
			Role:    "system",
			Content: sysPrompt,
		})
	}
	messages = append(messages, history...)
	messages = append(messages, agent.MessageInput{
		Role:    "user",
		Content: userPrompt,
	})

	response, _, err := da.RT.ExecWithMessages(ctx, messages, toolDefs, toolHandler)
	if err != nil {
		da.Logger.Error("dynamic-agent exec error", "error", err)
		msg.Nak()
		return
	}

	if replyTo != "" {
		outObj := map[string]string{
			"output": response,
		}
		dataBytes, _ := json.Marshal(outObj)

		proof := core.Proof{
			TaskID: envelope.ID,
			Type:   core.ProofAPI,
			Data:   dataBytes,
		}

		replyEnv, _ := core.NewEnvelope(envelope.ID+"_reply", env.Config.DID, envelope.SenderDID, envelope.ConversationID, core.INFORM, proof)
		b, _ := json.Marshal(replyEnv)
		if err := env.Bus.Publish(replyTo, b); err != nil {
			da.Logger.Error("dynamic-agent failed to publish reply", "error", err, "replyTo", replyTo)
		} else {
			da.Logger.Info("dynamic-agent published reply", "replyTo", replyTo)
		}
	}

	msg.Ack()
}
