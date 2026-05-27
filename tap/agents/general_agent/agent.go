package general_agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/tap/agents"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/lookup"
	"github.com/Yankzy/usetoro/tap/pkg/redux"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/Yankzy/usetoro/tap/pkg/tools/builtin"
	"github.com/Yankzy/usetoro/tap/workflows"
)

type conversationIDKey struct{}

func init() {
	agents.Register("general-agent", NewGeneralAgent)
}

// GeneralAgent is a TAP internal agent that bridges NATS communication
// with the in-process tool framework. It uses Runtime.ExecWithToolCalling
// for LLM interactions and Redux for RFC 6902 patch validation.
type GeneralAgent struct {
	*agent.BaseAgent
	RT           *agent.Runtime
	toolRegistry *tools.AgentRegistry
	taskManager  *tools.TaskManager
	toolMap      map[string]tools.Tool
}

func NewGeneralAgent(env core.Environment) core.Runnable {
	logger := env.Logger.With("agent", "general")

	registry := tools.NewAgentRegistry()
	registry.RegisterBuiltIns()

	taskMgr := tools.NewTaskManager()

	// Build the full tool pool (builtin tools + NATS-aware tools).
	// LLMFunc is set below after we have the Runtime reference.
	allTools := map[string]tools.Tool{
		"Bash":      &builtin.ShellTool{},
		"FileRead":  &builtin.FileReadTool{},
		"FileWrite": &builtin.FileWriteTool{},
		"FileEdit":  &builtin.FileEditTool{},
		"Grep":      &builtin.GrepTool{},
		"WebFetch":  &builtin.WebFetchTool{},
		"AlmanacLookup": &almanacLookupTool{
			bus:    env.Bus,
			logger: logger,
		},
		"Delegate": &delegateTool{
			bus:    env.Bus,
			logger: logger,
		},
		"ConversationState": &builtin.ConversationStateTool{
			Bus:      env.Bus,
			Logger:   logger,
			AgentDID: env.Config.DID,
		},
		"ScheduleReminder": &builtin.ScheduleReminderTool{
			Bus:      env.Bus,
			Logger:   logger,
			AgentDID: env.Config.DID,
		},
	}

	base := agent.NewBaseAgent(logger, env.Bus, env.Config, nil)

	ga := &GeneralAgent{
		BaseAgent:    base,
		RT:           agent.NewRuntime(logger, env.Bus, env.Config),
		toolRegistry: registry,
		taskManager:  taskMgr,
		toolMap:      allTools,
	}

	// Wire the AgentTool's LLMFunc (needs the Runtime from ga)
	allTools["Agent"] = &builtin.AgentTool{
		Registry:    registry,
		TaskManager: taskMgr,
		LLMFunc:     ga.makeLLMCallFunc(),
		AllTools:    allTools,
	}

	// Set the NATS handler now that ga is fully constructed
	base.Handler = func(msg *nats.Msg) {
		ga.handleMessage(msg, env, msg.Reply)
	}

	return ga
}

// ── LLM Call Func (bridges tools → Runtime.ExecWithToolCalling) ──────

func (ga *GeneralAgent) makeLLMCallFunc() tools.LLMCallFunc {
	return func(ctx context.Context, messages []tools.Message, tlz []tools.Tool) (string, error) {
		// Convert tools.Message -> agent.MessageInput
		msgInputs := make([]agent.MessageInput, 0, len(messages))
		for _, m := range messages {
			// For tool results, we append them as user messages with a special prefix
			// since the unified MessageInput doesn't have a dedicated tool role yet.
			// (The chat paradigm can handle tool results natively, but we map it simply here
			// to avoid overly complex state management across paradigms).
			content := m.Content
			role := m.Role
			if role == "tool" {
				role = "user"
				content = fmt.Sprintf("TOOL RESULT (%s): %s", m.ToolCallID, m.Content)
			}
			msgInputs = append(msgInputs, agent.MessageInput{
				Role:    role,
				Content: content,
			})
		}

		// Convert tools.Tool → agent.ToolDef
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

		// Build tool handler that dispatches to the tool's Call method
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

		return ga.RT.ExecWithMessages(ctx, msgInputs, toolDefs, toolHandler)
	}
}

func buildPromptFromMessages(messages []tools.Message) string {
	var b strings.Builder
	for _, m := range messages {
		switch m.Role {
		case "system":
			b.WriteString("SYSTEM: ")
		case "user":
			b.WriteString("USER: ")
		case "assistant":
			b.WriteString("ASSISTANT: ")
		case "tool":
			b.WriteString("TOOL RESULT (")
			b.WriteString(m.ToolCallID)
			b.WriteString("): ")
		}
		b.WriteString(m.Content)
		b.WriteString("\n\n")
	}
	return b.String()
}

// ── NATS handler ──────────────────────────────────────────────────────

func (ga *GeneralAgent) handleMessage(msg *nats.Msg, env core.Environment, replySubject string) {
	// Hard cap at 2 delivery attempts to prevent runaway LLM costs.
	if meta, err := msg.Metadata(); err == nil && meta.NumDelivered > 2 {
		ga.BaseAgent.Logger.Error("message exceeded max delivery attempts, terminating",
			"num_delivered", meta.NumDelivered,
			"subject", msg.Subject,
		)
		msg.Term()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	var envlp core.Envelope
	if err := json.Unmarshal(msg.Data, &envlp); err != nil {
		ga.BaseAgent.Logger.Error("failed to unmarshal envelope", "error", err)
		msg.Term()
		return
	}

	if envlp.Performative != core.CFP && envlp.Performative != core.ACCEPT_PROPOSAL && envlp.Performative != core.REQUEST {
		msg.Ack()
		return
	}

	ga.BaseAgent.Logger.Info("received task", "performative", envlp.Performative, "from", envlp.SenderDID)

	if envlp.ConversationID != "" {
		ctx = context.WithValue(ctx, conversationIDKey{}, envlp.ConversationID)
	}

	// Extract the user prompt, system prompt override, workflow schema, and messages
	prompt, taskSysPrompt, wfSchema, rbacPolicy, structuredMsgs := extractTaskConfig(envlp.Body)

	toolList := resolveToolsFromConfig(env.Config, ga.toolMap)

	// Use the task's system prompt if provided, otherwise fall back to config
	systemPrompt := taskSysPrompt
	if systemPrompt == "" {
		systemPrompt = env.Config.SystemPrompt
	}
	if wfSchema != "" {
		if systemPrompt != "" {
			systemPrompt += "\n\n"
		}
		systemPrompt += fmt.Sprintf("OUTPUT FORMAT: You MUST produce a JSON array of RFC 6902 JSON Patch operations. The target state schema is:\n%s\n\nEach operation must have 'op', 'path', and 'value' fields. Example: [{\"op\":\"add\",\"path\":\"/result\",\"value\":\"...\"}]", wfSchema)
	}

	var messages []tools.Message
	if len(structuredMsgs) > 0 {
		messages = make([]tools.Message, 0, len(structuredMsgs)+1)
		messages = append(messages, tools.Message{Role: "system", Content: systemPrompt})
		messages = append(messages, structuredMsgs...)
	} else {
		messages = []tools.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		}
	}

	agentCtx := tools.NewAgentContext("general-purpose", 50)
	llmFunc := ga.makeLLMCallFunc()

	ga.BaseAgent.Logger.Info("running agent", "tools", len(toolList), "has_schema", wfSchema != "")

	// ── Redux circuit-breaker loop ───────────────────────
	const maxRetries = 3
	var faults []redux.DomainFault
	var finalOutput string

	for attempt := 0; attempt < maxRetries; attempt++ {
		if len(faults) > 0 {
			// Feed Redux faults back to the LLM for self-correction
			faultMsg := "Your previous output failed Redux validation. Fix the following issues and try again:\n"
			for _, f := range faults {
				faultMsg += fmt.Sprintf("- %s\n", f.Error)
			}
			messages = append(messages, tools.Message{Role: "user", Content: faultMsg})
		}

		result, err := tools.RunAgent(ctx, agentCtx, toolList, messages, llmFunc)
		if err != nil {
			ga.BaseAgent.Logger.Error("agent run failed", "error", err)
			_ = replyFailure(msg, envlp, env, ga.BaseAgent.Logger, err, replySubject)
			return
		}
		finalOutput = result.Output
		messages = result.Messages

		// If no schema, return raw output (no Redux validation)
		if wfSchema == "" {
			break
		}

		// Parse output as RFC 6902 patches and validate through Redux
		patches, parseErr := parsePatches(finalOutput)
		if parseErr != nil {
			faults = []redux.DomainFault{{EventID: "parse", Error: parseErr.Error()}}
			continue
		}

		store, storeErr := redux.NewStore(redux.EngineConfig{
			SchemaString:    wfSchema,
			RBAC:            rbacPolicy,
			MaxOperations:   10,
			MaxPayloadBytes: 64 * 1024,
		})
		if storeErr != nil {
			ga.BaseAgent.Logger.Error("redux store init failed", "error", storeErr)
			break
		}

		_, _, reduceFaults, _ := store.Reduce(ctx, []byte("{}"), 0, []redux.RFC6902Event{{
			EventID:    uuid.New().String(),
			SequenceID: uint64(attempt),
			Timestamp:  time.Now(),
			Type:       "general_agent",
			Actor:      ga.BaseAgent.Cfg.DID,
			PatchArray: patches,
		}})

		if len(reduceFaults) == 0 {
			faults = nil
			break
		}
		faults = reduceFaults
		ga.BaseAgent.Logger.Warn("redux faults, retrying", "attempt", attempt+1, "faults", len(faults))
	}

	if len(faults) > 0 {
		err := fmt.Errorf("redux circuit breaker tripped after %d retries: %d unresolved faults", maxRetries, len(faults))
		ga.BaseAgent.Logger.Error("redux validation failed", "error", err)
		_ = replyFailure(msg, envlp, env, ga.BaseAgent.Logger, err, replySubject)
		return
	}

	// ── Wrap result in Proof + INFORM ────────────────────
	proof := core.Proof{
		TaskID:    envlp.ID,
		Type:      core.ProofAPI,
		Timestamp: time.Now().Unix(),
		Data:      json.RawMessage(fmt.Sprintf(`{"output": %q}`, finalOutput)),
	}
	proofBytes, _ := json.Marshal(proof)

	replyEnv := core.Envelope{
		ID:             uuid.New().String(),
		Timestamp:      time.Now(),
		SenderDID:      env.Config.DID,
		ReceiverDID:    envlp.SenderDID,
		Performative:   core.INFORM,
		ConversationID: envlp.ConversationID,
		Body:           proofBytes,
	}
	replyBytes, _ := json.Marshal(replyEnv)

	// Extract custom reply header to bypass JetStream Ack-override behavior
	if msg.Header != nil {
		if customReply := msg.Header.Get("Toro-Reply-To"); customReply != "" {
			replySubject = customReply
		}
	}

	// Reply via core NATS if we have a reply subject (inbox from
	// async dispatch). JetStream Publish can't target _INBOX.* subjects.
	if replySubject != "" && !strings.HasPrefix(replySubject, "$JS.") {
		if err := env.Bus.PublishCore(replySubject, replyBytes); err != nil {
			ga.BaseAgent.Logger.Error("failed to respond via inbox", "error", err)
			msg.Nak()
			return
		}
	} else {
		targetTopic := core.BuildAgentInbox(envlp.SenderDID)
		if err := env.Bus.Publish(targetTopic, replyBytes); err != nil {
			ga.BaseAgent.Logger.Error("failed to publish INFORM", "error", err)
			msg.Nak()
			return
		}
	}

	ga.BaseAgent.Logger.Info("agent finished, INFORM sent", "output_len", len(finalOutput))
	msg.Ack()
}

// ── Helpers ───────────────────────────────────────────────────────────

func extractTaskConfig(body json.RawMessage) (prompt string, systemPrompt string, wfSchema string, rbac redux.RBACPolicy, messages []tools.Message) {
	// Try raw string
	var s string
	if err := json.Unmarshal(body, &s); err == nil && s != "" {
		return s, "", "", redux.RBACPolicy{}, nil
	}

	// Try TaskDefinition wrapper
	var taskDef core.TaskDefinition
	if err := json.Unmarshal(body, &taskDef); err == nil && len(taskDef.Payload) > 0 {
		wfSchema = taskDef.WorkflowSchema
		systemPrompt = taskDef.SystemPrompt
		if len(taskDef.RBACPolicy) > 0 {
			rbac = redux.RBACPolicy{AllowedPrefixes: map[string][]string{
				taskDef.SystemPrompt: taskDef.RBACPolicy,
			}}
		}
		var payload map[string]any
		if err := core.UnmarshalTaskPayload(taskDef.Payload, &payload); err == nil {
			if msgsRaw, ok := payload["messages"]; ok {
				msgsBytes, _ := json.Marshal(msgsRaw)
				_ = json.Unmarshal(msgsBytes, &messages)
			}
			
			if p, ok := payload["prompt"].(string); ok && p != "" {
				return p, systemPrompt, wfSchema, rbac, messages
			}
			if in, ok := payload["input"].(string); ok && in != "" {
				return in, systemPrompt, wfSchema, rbac, messages
			}
			b, _ := json.Marshal(payload)
			return string(b), systemPrompt, wfSchema, rbac, messages
		}
	}

	// Try plain JSON object
	var m map[string]any
	if err := json.Unmarshal(body, &m); err == nil {
		if msgsRaw, ok := m["messages"]; ok {
			msgsBytes, _ := json.Marshal(msgsRaw)
			_ = json.Unmarshal(msgsBytes, &messages)
		}

		if p, ok := m["prompt"].(string); ok && p != "" {
			return p, "", wfSchema, rbac, messages
		}
		if in, ok := m["input"].(string); ok && in != "" {
			return in, "", wfSchema, rbac, messages
		}
	}

	return string(body), "", wfSchema, rbac, nil
}

func parsePatches(output string) ([]json.RawMessage, error) {
	output = strings.TrimSpace(output)
	// Strip markdown code fences if present
	if strings.HasPrefix(output, "```") {
		output = strings.TrimPrefix(output, "```json")
		output = strings.TrimPrefix(output, "```")
		output = strings.TrimSuffix(output, "```")
		output = strings.TrimSpace(output)
	}

	var patches []json.RawMessage
	if err := json.Unmarshal([]byte(output), &patches); err != nil {
		// Try single patch
		var single map[string]any
		if err2 := json.Unmarshal([]byte(output), &single); err2 == nil {
			if _, ok := single["op"]; ok {
				b, _ := json.Marshal(single)
				return []json.RawMessage{b}, nil
			}
		}
		return nil, fmt.Errorf("parse patches: %w (raw: %.200s)", err, output)
	}
	return patches, nil
}

func replyFailure(msg *nats.Msg, envlp core.Envelope, env core.Environment, logger *slog.Logger, err error, replySubject string) error {
	payload := map[string]string{"error": err.Error()}
	replyEnv := core.Envelope{
		ID:           uuid.New().String(),
		Timestamp:    time.Now(),
		SenderDID:    env.Config.DID,
		ReceiverDID:  envlp.SenderDID,
		Performative: core.FAILURE,
		Body:         mustMarshal(payload),
	}
	replyBytes, _ := json.Marshal(replyEnv)
	targetTopic := replySubject
	if targetTopic == "" {
		targetTopic = core.BuildAgentInbox(envlp.SenderDID)
	}
	if pubErr := env.Bus.Publish(targetTopic, replyBytes); pubErr != nil {
		logger.Error("failed to publish FAILURE", "error", pubErr)
	}
	return msg.Ack()
}

func resolveToolsFromConfig(cfg core.AgentConfig, allTools map[string]tools.Tool) []tools.Tool {
	if len(cfg.Tools) == 0 {
		list := make([]tools.Tool, 0, len(allTools))
		for name, t := range allTools {
			if name == "ConversationState" || name == "ScheduleReminder" {
				continue
			}
			list = append(list, t)
		}
		return list
	}
	list := make([]tools.Tool, 0, len(cfg.Tools))
	for _, tc := range cfg.Tools {
		if t, ok := allTools[tc.Name]; ok {
			list = append(list, t)
		}
	}
	return list
}

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// ── NATS-aware tools ──────────────────────────────────────────────────

type almanacLookupTool struct {
	bus    core.EventBus
	logger *slog.Logger
}

func (t *almanacLookupTool) Name() string { return "AlmanacLookup" }
func (t *almanacLookupTool) Description() string {
	return "Query the Almanac to discover available specialized agents and their capabilities. Use this before delegating work to find the right agent for a task."
}
func (t *almanacLookupTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"capability_type": {"type": "string", "description": "Filter by capability type (e.g., 'agents.accounting.classify_outflow', 'agents.ocr'). Empty returns all agents."},
			"did": {"type": "string", "description": "Lookup a specific agent by DID"}
		},
		"required": []
	}`)
}

func (t *almanacLookupTool) Call(ctx context.Context, input map[string]any) (string, error) {
	query := lookup.AlmanacQuery{CallerDID: "did:toro:general-agent"}
	if ct, ok := input["capability_type"].(string); ok && ct != "" {
		query.CapabilityType = ct
	}
	if did, ok := input["did"].(string); ok && did != "" {
		query.DID = did
	}
	queryBytes, _ := json.Marshal(query)
	resp, err := t.bus.RequestWithContext(ctx, core.SubjectAlmanacQuery, queryBytes)
	if err != nil {
		return "", fmt.Errorf("almanac query failed: %w", err)
	}
	var entries []lookup.AlmanacEntry
	if err := json.Unmarshal(resp.Data, &entries); err != nil {
		return "", fmt.Errorf("parse almanac response: %w", err)
	}
	if len(entries) == 0 {
		return "No agents found matching the query.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d agent(s):\n", len(entries))
	for _, e := range entries {
		caps := make([]string, len(e.Capabilities))
		for i, c := range e.Capabilities {
			caps[i] = c.Type
		}
		fmt.Fprintf(&b, "- DID: %s\n  Endpoints: %v\n  Capabilities: %v\n", e.DID, e.Endpoints, caps)
	}
	return b.String(), nil
}

type delegateTool struct {
	bus    core.EventBus
	logger *slog.Logger
}

func (t *delegateTool) Name() string { return "Delegate" }
func (t *delegateTool) Description() string {
	return "Delegate work to specialized agents by creating a dynamic workflow. Provide the agent activity types (discovered via AlmanacLookup) and the payload. The Orchestrator will dispatch to the appropriate agents and correlate results."
}
func (t *delegateTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"steps": {"type": "array", "description": "Array of workflow steps. Each step needs: id (unique step ID), activity_type (agent activity type, e.g. 'agents.accounting.classify_outflow')"},
			"payload": {"type": "object", "description": "The payload/data to pass to the workflow"}
		},
		"required": ["steps"]
	}`)
}

func (t *delegateTool) Call(ctx context.Context, input map[string]any) (string, error) {
	convID, _ := ctx.Value(conversationIDKey{}).(string)
	if convID == "" {
		return "", fmt.Errorf("no conversation ID in context; Delegate can only be used within a workflow")
	}
	stepsRaw, ok := input["steps"]
	if !ok {
		return "", fmt.Errorf("steps is required")
	}
	stepsJSON, err := json.Marshal(stepsRaw)
	if err != nil {
		return "", fmt.Errorf("marshal steps: %w", err)
	}
	var steps []workflows.WorkflowStep
	if err := json.Unmarshal(stepsJSON, &steps); err != nil {
		return "", fmt.Errorf("parse steps: %w", err)
	}
	if len(steps) == 0 {
		return "", fmt.Errorf("at least one step is required")
	}
	if len(steps) > workflows.MaxDynamicDelegationSteps {
		return "", fmt.Errorf("too many steps (%d > %d)", len(steps), workflows.MaxDynamicDelegationSteps)
	}
	var payload json.RawMessage
	if p, ok := input["payload"]; ok {
		payload, _ = json.Marshal(p)
	} else {
		payload = json.RawMessage(`{}`)
	}
	req := workflows.DelegationRequest{Steps: steps, Payload: payload}
	reqBytes, _ := json.Marshal(req)
	envlp := core.Envelope{
		ID:             uuid.New().String(),
		Timestamp:      time.Now(),
		SenderDID:      "did:toro:general-agent",
		ReceiverDID:    "did:toro:orchestrator",
		Performative:   core.DELEGATE,
		ConversationID: convID,
		Body:           reqBytes,
	}
	envlpBytes, _ := json.Marshal(envlp)
	if err := t.bus.Publish(workflows.OrchestratorInbox, envlpBytes); err != nil {
		return "", fmt.Errorf("publish DELEGATE to orchestrator: %w", err)
	}
	return fmt.Sprintf("Dynamic workflow dispatched to Orchestrator with %d step(s). ConversationID: %s.", len(steps), convID), nil
}
