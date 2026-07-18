package general_agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/redux"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/Yankzy/usetoro/tap/pkg/tools/builtin"
	"github.com/nats-io/nats.go"
)

func TestExtractTaskConfig_DirectString(t *testing.T) {
	body := json.RawMessage(`"hello world"`)
	prompt, _, _, _, _, _, _, _ := extractTaskConfig(body)
	if prompt != "hello world" {
		t.Errorf("prompt = %q, want %q", prompt, "hello world")
	}
}

func TestExtractTaskConfig_Object(t *testing.T) {
	body := json.RawMessage(`{"key": "value"}`)
	prompt, _, _, _, _, _, _, _ := extractTaskConfig(body)
	if !strings.Contains(prompt, "key") {
		t.Errorf("prompt should contain 'key', got: %s", prompt)
	}
}

func TestExtractTaskConfig_PromptField(t *testing.T) {
	body := json.RawMessage(`{"prompt": "do something useful", "config": {}}`)
	prompt, _, _, _, _, _, _, _ := extractTaskConfig(body)
	if prompt != "do something useful" {
		t.Errorf("prompt = %q, want %q", prompt, "do something useful")
	}
}

func TestExtractTaskConfig_InputField(t *testing.T) {
	body := json.RawMessage(`{"input": "process this data", "config": {}}`)
	prompt, _, _, _, _, _, _, _ := extractTaskConfig(body)
	if prompt != "process this data" {
		t.Errorf("prompt = %q, want %q", prompt, "process this data")
	}
}

func TestExtractTaskConfig_TaskDefinitionWithPayload(t *testing.T) {
	taskDef := core.TaskDefinition{
		ID:             "task-1",
		Domain:         "accounting",
		WorkflowSchema: `{"type":"object","properties":{"result":{"type":"string"}}}`,
		Payload:        json.RawMessage(`{"prompt": "classify these transactions"}`),
	}
	body, _ := json.Marshal(taskDef)

	prompt, _, schema, _, _, _, _, _ := extractTaskConfig(body)
	if prompt != "classify these transactions" {
		t.Errorf("prompt = %q, want %q", prompt, "classify these transactions")
	}
	if schema == "" {
		t.Error("expected non-empty workflow schema from TaskDefinition")
	}
}

func TestExtractTaskConfig_EmptyObject(t *testing.T) {
	body := json.RawMessage(`{}`)
	prompt, _, _, _, _, _, _, _ := extractTaskConfig(body)
	if prompt != "{}" {
		t.Errorf("prompt = %q, want %q", prompt, "{}")
	}
}

func TestParsePatches_Array(t *testing.T) {
	patches, err := redux.ParsePatches(`[{"op":"add","path":"/x","value":1}]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(patches) != 1 {
		t.Errorf("expected 1 patch, got %d", len(patches))
	}
}

func TestParsePatches_SingleObject(t *testing.T) {
	patches, err := redux.ParsePatches(`{"op":"replace","path":"/y","value":"hello"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(patches) != 1 {
		t.Errorf("expected 1 patch, got %d", len(patches))
	}
}

func TestParsePatches_CodeFence(t *testing.T) {
	patches, err := redux.ParsePatches("```json\n[{\"op\":\"add\",\"path\":\"/z\",\"value\":true}]\n```")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(patches) != 1 {
		t.Errorf("expected 1 patch, got %d", len(patches))
	}
}

func TestParsePatches_Invalid(t *testing.T) {
	_, err := redux.ParsePatches(`not json at all`)
	if err == nil {
		t.Error("expected error for invalid input")
	}
}

func TestResolveToolsFromConfig_Empty(t *testing.T) {
	allTools := map[string]tools.Tool{
		"Bash":     &builtin.ShellTool{},
		"FileRead": &builtin.FileReadTool{},
	}
	cfg := core.AgentConfig{}
	result := resolveToolsFromConfig(cfg, allTools)
	if len(result) != 2 {
		t.Errorf("expected 2 tools (all), got %d", len(result))
	}
}

func TestResolveToolsFromConfig_Filtered(t *testing.T) {
	allTools := map[string]tools.Tool{
		"Bash":     &builtin.ShellTool{},
		"FileRead": &builtin.FileReadTool{},
		"Grep":     &builtin.GrepTool{},
	}
	cfg := core.AgentConfig{
		Tools: []core.ToolConfig{
			{Name: "Bash"},
			{Name: "Grep"},
		},
	}
	result := resolveToolsFromConfig(cfg, allTools)
	if len(result) != 2 {
		t.Errorf("expected 2 tools (filtered), got %d", len(result))
	}
}

func TestResolveToolsFromConfig_UnknownTool(t *testing.T) {
	allTools := map[string]tools.Tool{
		"Bash": &builtin.ShellTool{},
	}
	cfg := core.AgentConfig{
		Tools: []core.ToolConfig{
			{Name: "Bash"},
			{Name: "NonExistent"},
		},
	}
	result := resolveToolsFromConfig(cfg, allTools)
	if len(result) != 1 {
		t.Errorf("expected 1 tool (unknown skipped), got %d", len(result))
	}
}

func TestRunAgent_BasicFlow(t *testing.T) {
	// Mock LLM function that returns text immediately
	mockLLM := func(ctx context.Context, messages []tools.Message, tlz []tools.Tool) (string, []tools.Message, error) {
		return "I analyzed the request and here is the answer.", nil, nil
	}

	allTools := []tools.Tool{&builtin.FileReadTool{}}
	agentCtx := tools.NewAgentContext("general-purpose", 10)
	messages := []tools.Message{{Role: "user", Content: "test"}}

	result, err := tools.RunAgent(context.Background(), agentCtx, allTools, messages, mockLLM)
	if err != nil {
		t.Fatalf("RunAgent failed: %v", err)
	}
	if result.Output == "" {
		t.Error("expected non-empty output")
	}
}

func TestRunAgent_ContextCancel(t *testing.T) {
	mockLLM := func(ctx context.Context, messages []tools.Message, tlz []tools.Tool) (string, []tools.Message, error) {
		return "ok", nil, nil
	}

	allTools := []tools.Tool{&builtin.FileReadTool{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	agentCtx := tools.NewAgentContext("test", 10)
	agentCtx.Abort = ctx

	_, err := tools.RunAgent(context.Background(), agentCtx, allTools, []tools.Message{{Role: "user", Content: "test"}}, mockLLM)
	if err == nil {
		t.Error("expected error for canceled context")
	}
}

// Test the full Redux circuit breaker flow with a mock LLM
func TestReduxCircuitBreaker(t *testing.T) {
	t.Run("valid patches pass on first attempt", func(t *testing.T) {
		// This simulates what the handler does with Redux
		output := `[{"op":"add","path":"/result","value":"success"}]`
		patches, err := redux.ParsePatches(output)
		if err != nil {
			t.Fatalf("parse patches: %v", err)
		}

		store, err := redux.NewStore(redux.EngineConfig{
			SchemaString:    `{"type":"object","properties":{"result":{"type":"string"}}}`,
			MaxOperations:   10,
			MaxPayloadBytes: 64 * 1024,
		})
		if err != nil {
			t.Fatalf("redux store: %v", err)
		}

		_, _, faults, _ := store.Reduce(context.Background(), []byte("{}"), 0, []redux.RFC6902Event{{
			EventID:    "evt-1",
			SequenceID: 0,
			PatchArray: patches,
		}})

		if len(faults) > 0 {
			t.Errorf("expected no faults, got: %v", faults)
		}
	})

	t.Run("invalid patches trigger faults", func(t *testing.T) {
		// Patch that violates schema (wrong type)
		output := `[{"op":"add","path":"/result","value":42}]`
		patches, err := redux.ParsePatches(output)
		if err != nil {
			t.Fatalf("parse patches: %v", err)
		}

		store, err := redux.NewStore(redux.EngineConfig{
			SchemaString:    `{"type":"object","properties":{"result":{"type":"string"}}}`,
			MaxOperations:   10,
			MaxPayloadBytes: 64 * 1024,
		})
		if err != nil {
			t.Fatalf("redux store: %v", err)
		}

		_, _, faults, _ := store.Reduce(context.Background(), []byte("{}"), 0, []redux.RFC6902Event{{
			EventID:    "evt-2",
			SequenceID: 0,
			PatchArray: patches,
		}})

		if len(faults) == 0 {
			t.Error("expected faults for type mismatch, got none")
		}
	})
}

func TestExtractRealmID(t *testing.T) {
	// 1. Test raw JSON task payload with TaskDefinition
	taskBody := json.RawMessage(`{
		"payload": {"realm_id": "realm-123"}
	}`)
	rid := extractRealmID(taskBody)
	if rid != "realm-123" {
		t.Errorf("expected realm-123, got %q", rid)
	}

	// 2. Test raw JSON payload directly
	directBody := json.RawMessage(`{"realm_id": "realm-456"}`)
	rid = extractRealmID(directBody)
	if rid != "realm-456" {
		t.Errorf("expected realm-456, got %q", rid)
	}

	// 3. Test tenant_id fallback
	fallbackBody := json.RawMessage(`{"tenant_id": "tenant-789"}`)
	rid = extractRealmID(fallbackBody)
	if rid != "tenant-789" {
		t.Errorf("expected tenant-789, got %q", rid)
	}
}

func TestGeneralAgent_DynamicSkillsIntegration(t *testing.T) {
	tenantID := "test-integration-realm"
	testBaseDir := filepath.Join("docs", "skills", tenantID)
	skillDir := filepath.Join(testBaseDir, "triple")
	scriptsDir := filepath.Join(skillDir, "scripts")

	err := os.MkdirAll(scriptsDir, 0755)
	if err != nil {
		t.Fatalf("failed to create scripts path: %v", err)
	}
	defer os.RemoveAll(testBaseDir)

	manifestContent := `---
name: triple
description: Triple the number
version: 1.0.0
inputs:
  type: object
  properties:
    x: {type: integer}
  required: [x]
entrypoint: scripts/triple.py
---`
	err = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(manifestContent), 0644)
	if err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	pythonScript := `
import sys
import json
data = sys.stdin.read()
inputs = json.loads(data)
x = inputs.get("x", 0)
print(json.dumps({"result": x * 3}))
`
	err = os.WriteFile(filepath.Join(scriptsDir, "triple.py"), []byte(pythonScript), 0644)
	if err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	// Now scan the catalog and verify the dynamic tool is found and executable
	ctx := context.Background()
	bus := &mockEventBus{}

	dynamicTools, err := tools.ScanSkillsCatalog(ctx, tenantID, bus, "did:toro:agent:test")
	if err != nil {
		t.Fatalf("ScanSkillsCatalog failed: %v", err)
	}
	if len(dynamicTools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(dynamicTools))
	}

	tool := dynamicTools[0]
	if tool.Name() != "triple" {
		t.Errorf("expected tool name 'triple', got %s", tool.Name())
	}

	res, err := tool.Call(ctx, map[string]any{"x": float64(5)})
	if err != nil {
		t.Fatalf("failed to call tool: %v", err)
	}

	expectedStatusMsg := "Task dispatched asynchronously to skill triple. I will suspend execution and wait. You will receive an INFORM message when the task completes."
	if res != expectedStatusMsg {
		t.Errorf("expected status output %q, got %q", expectedStatusMsg, res)
	}

	// Verify asynchronous task queueing envelope in mock event bus
	if len(bus.PublishedMessages) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(bus.PublishedMessages))
	}

	published := bus.PublishedMessages[0]
	expectedSubject := fmt.Sprintf("public_python.execute.%s.triple", tenantID)
	if published.Subject != expectedSubject {
		t.Errorf("expected subject %q, got %q", expectedSubject, published.Subject)
	}

	var env core.Envelope
	if err := json.Unmarshal(published.Data, &env); err != nil {
		t.Fatalf("failed to unmarshal FIPA envelope: %v", err)
	}

	if env.SenderDID != "did:toro:agent:test" {
		t.Errorf("expected sender DID did:toro:agent:test, got %s", env.SenderDID)
	}
	if env.Performative != core.REQUEST {
		t.Errorf("expected performative 'request', got %v", env.Performative)
	}

	var reqBody map[string]any
	if err := json.Unmarshal(env.Body, &reqBody); err != nil {
		t.Fatalf("failed to unmarshal envelope body: %v", err)
	}

	scriptCode, _ := reqBody["script"].(string)
	scriptInput, _ := reqBody["input"].(map[string]any)
	tID, _ := reqBody["tenant_id"].(string)
	retSubject, _ := reqBody["return_subject"].(string)

	expectedReturnSubject := core.BuildAgentInbox("did:toro:agent:test")
	if retSubject != expectedReturnSubject {
		t.Errorf("expected return subject %q, got %q", expectedReturnSubject, retSubject)
	}

	// Execute python code locally to check script logic correctness
	tempFile, err := os.CreateTemp("", "mock_worker_*.py")
	if err != nil {
		t.Fatalf("failed temp file: %v", err)
	}
	defer os.Remove(tempFile.Name())

	_, _ = tempFile.WriteString(scriptCode)
	_ = tempFile.Close()

	cmd := exec.CommandContext(ctx, "python3", tempFile.Name())
	inputBytes, _ := json.Marshal(scriptInput)
	cmd.Stdin = strings.NewReader(string(inputBytes))
	cmd.Env = append(os.Environ(),
		"TENANT_ID="+tID,
		"REALM_ID="+tID,
	)

	stdoutBytes, execErr := cmd.CombinedOutput()
	if execErr != nil {
		t.Fatalf("simulated execution failed: %v, output: %s", execErr, string(stdoutBytes))
	}

	var result struct {
		Result int `json:"result"`
	}
	if err := json.Unmarshal(stdoutBytes, &result); err != nil {
		t.Fatalf("failed to unmarshal script output: %v, raw: %s", err, string(stdoutBytes))
	}

	if result.Result != 15 {
		t.Errorf("expected 15, got %d", result.Result)
	}
}

type mockEventBus struct {
	OnRequest         func(ctx context.Context, subject string, data []byte) (*nats.Msg, error)
	PublishedMessages []struct {
		Subject string
		Data    []byte
	}
}

func (b *mockEventBus) Publish(subject string, data []byte) error {
	b.PublishedMessages = append(b.PublishedMessages, struct {
		Subject string
		Data    []byte
	}{Subject: subject, Data: data})
	return nil
}

func (b *mockEventBus) PublishCore(subject string, data []byte) error { return nil }

func (b *mockEventBus) RequestWithContext(ctx context.Context, subject string, data []byte) (*nats.Msg, error) {
	if b.OnRequest != nil {
		return b.OnRequest(ctx, subject, data)
	}
	return nil, nil
}

func (b *mockEventBus) QueueSubscribe(subj, queue string, cb nats.MsgHandler, opts ...nats.SubOpt) (*nats.Subscription, error) {
	return nil, nil
}


