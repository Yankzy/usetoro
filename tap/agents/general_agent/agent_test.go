package general_agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/redux"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/Yankzy/usetoro/tap/pkg/tools/builtin"
)

func TestExtractTaskConfig_DirectString(t *testing.T) {
	body := json.RawMessage(`"hello world"`)
	prompt, _, _, _, _ := extractTaskConfig(body)
	if prompt != "hello world" {
		t.Errorf("prompt = %q, want %q", prompt, "hello world")
	}
}

func TestExtractTaskConfig_Object(t *testing.T) {
	body := json.RawMessage(`{"key": "value"}`)
	prompt, _, _, _, _ := extractTaskConfig(body)
	if !strings.Contains(prompt, "key") {
		t.Errorf("prompt should contain 'key', got: %s", prompt)
	}
}

func TestExtractTaskConfig_PromptField(t *testing.T) {
	body := json.RawMessage(`{"prompt": "do something useful", "config": {}}`)
	prompt, _, _, _, _ := extractTaskConfig(body)
	if prompt != "do something useful" {
		t.Errorf("prompt = %q, want %q", prompt, "do something useful")
	}
}

func TestExtractTaskConfig_InputField(t *testing.T) {
	body := json.RawMessage(`{"input": "process this data", "config": {}}`)
	prompt, _, _, _, _ := extractTaskConfig(body)
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

	prompt, _, schema, _, _ := extractTaskConfig(body)
	if prompt != "classify these transactions" {
		t.Errorf("prompt = %q, want %q", prompt, "classify these transactions")
	}
	if schema == "" {
		t.Error("expected non-empty workflow schema from TaskDefinition")
	}
}

func TestExtractTaskConfig_EmptyObject(t *testing.T) {
	body := json.RawMessage(`{}`)
	prompt, _, _, _, _ := extractTaskConfig(body)
	if prompt != "{}" {
		t.Errorf("prompt = %q, want %q", prompt, "{}")
	}
}

func TestParsePatches_Array(t *testing.T) {
	patches, err := parsePatches(`[{"op":"add","path":"/x","value":1}]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(patches) != 1 {
		t.Errorf("expected 1 patch, got %d", len(patches))
	}
}

func TestParsePatches_SingleObject(t *testing.T) {
	patches, err := parsePatches(`{"op":"replace","path":"/y","value":"hello"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(patches) != 1 {
		t.Errorf("expected 1 patch, got %d", len(patches))
	}
}

func TestParsePatches_CodeFence(t *testing.T) {
	patches, err := parsePatches("```json\n[{\"op\":\"add\",\"path\":\"/z\",\"value\":true}]\n```")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(patches) != 1 {
		t.Errorf("expected 1 patch, got %d", len(patches))
	}
}

func TestParsePatches_Invalid(t *testing.T) {
	_, err := parsePatches(`not json at all`)
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
	mockLLM := func(ctx context.Context, messages []tools.Message, tlz []tools.Tool) (string, error) {
		return "I analyzed the request and here is the answer.", nil
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
	mockLLM := func(ctx context.Context, messages []tools.Message, tlz []tools.Tool) (string, error) {
		return "ok", nil
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
		patches, err := parsePatches(output)
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
		patches, err := parsePatches(output)
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

