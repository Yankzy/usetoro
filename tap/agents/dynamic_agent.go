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
}

// NewDynamicAgent instantiates a generic agent wrapper. The actual behavior
// (prompt, tools) is fetched at execution time.
func NewDynamicAgent(env core.Environment) core.Runnable {
	agentName := env.Config.Name
	if agentName == "" {
		agentName = "dynamic-agent"
	}
	
	logger := env.Logger.With("agent", agentName)

	da := &DynamicAgent{
		RT:      agent.NewRuntime(logger, env.Bus, env.Config),
		env:     env,
		queries: env.Queries, // Assumes env.Queries is injected
		name:    agentName,
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
		da.Logger.Error("failed to load agent configuration from db", "name", requestedAgent, "error", err)
		msg.Nak()
		return
	}

	da.Logger.Info("loaded dynamic agent configuration", "name", configRec.Name, "sdk", configRec.SdkClient)

	// 2. Resolve SDK and instantiate ToolMultiplexer
	var activeTools []tools.Tool
	
	// Map the requested SDK to the actual injected client
	switch configRec.SdkClient {
	case "mailpool":
		if env.Mailpool != nil {
			// Extract the raw Base Client from the generated interface
			// The interface is just a wrapper, we want the underlying client for reflection if possible,
			// or we can just reflect on the interface directly!
			activeTools = append(activeTools, tools.NewSDKMultiplexer(env.Mailpool.ClientInterface))
		} else {
			da.Logger.Warn("mailpool sdk requested by config but not injected")
		}
	case "tap":
		// Load core TAP tools for Mailroom Triage Agent
		toolNames := map[string]bool{
			"UpdateTransactionClassification": true,
			"CheckExistingDocuments":          true,
			"QueueClientRequest":              true,
			"FetchCommunicationHistory":       true,
		}
		for _, toolCfg := range env.Config.Tools {
			if !toolNames[toolCfg.Name] {
				continue
			}
			if toolCfg.ActivityType != "" {
				activeTools = append(activeTools, &builtin.AsyncWorkerTool{
					Bus:          env.Bus,
					AgentDID:     env.Config.DID,
					ToolName:     toolCfg.Name,
					ToolDesc:     toolCfg.Description,
					Schema:       json.RawMessage(toolCfg.InputSchema),
					ActivityType: toolCfg.ActivityType,
				})
			} else {
				if t := builtin.GetTool(toolCfg.Name, env, da.Logger); t != nil {
					activeTools = append(activeTools, t)
				} else {
					da.Logger.Warn("requested tap builtin tool not found", "tool", toolCfg.Name)
				}
			}
		}
	// case "meta", "stripe", "qbo" ... (add as needed in future)
	default:
		da.Logger.Warn("unknown sdk requested", "sdk", configRec.SdkClient)
	}

	// 3. Convert tools for LLM
	toolDefs := make([]agent.ToolDef, 0, len(activeTools))
	toolMap := make(map[string]tools.Tool)
	
	for _, t := range activeTools {
		var schema map[string]any
		_ = json.Unmarshal(t.InputSchema(), &schema)
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

	userPrompt, _ := payload["prompt"].(string)
	if userPrompt == "" {
		// Fallback in case prompt is not in payload
		userPrompt = fmt.Sprintf("User Request: %s", string(envelope.Body))
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
