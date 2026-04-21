package workflows

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestWorkflowLoadingWithSchema(t *testing.T) {
	// Create a temporary directory for test workflows
	tmpDir, err := os.MkdirTemp("", "workflow-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	yamlContent := `
name: Test Workflow
version: "1.2.3"
trigger_topic: test.trigger
steps:
  - id: step1
    activity_type: test.activity
    complexity: 1
    workflow_schema: |
      {
        "type": "object",
        "properties": {
          "foo": { "type": "string" }
        }
      }
    system_prompt: "You are a test agent."
`
	filePath := filepath.Join(tmpDir, "test_workflow.yaml")
	if err := os.WriteFile(filePath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write test yaml: %v", err)
	}

	// unmarshal manually using viper to verify struct tags
	v := viper.New()
	v.SetConfigFile(filePath)
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	var wfDef WorkflowDef
	if err := v.Unmarshal(&wfDef); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if wfDef.Name != "Test Workflow" {
		t.Errorf("expected name 'Test Workflow', got %q", wfDef.Name)
	}

	if len(wfDef.Steps) == 0 {
		t.Fatal("expected 1 step, got 0")
	}

	step := wfDef.Steps[0]
	if step.WorkflowSchema == "" {
		t.Error("workflow_schema was not loaded")
	}

	if step.SystemPrompt != "You are a test agent." {
		t.Errorf("expected system_prompt 'You are a test agent.', got %q", step.SystemPrompt)
	}

	// Verify that the schema is correctly unmarshaled as a string
	if !strings.Contains(step.WorkflowSchema, "foo") {
		t.Errorf("workflow_schema seems wrong: %q", step.WorkflowSchema)
	}
}

func TestWorkflowJsonPreservation(t *testing.T) {
	// Test that marshaling to JSON (for DB) and back preserves the schema
	original := WorkflowDef{
		Name: "JSON Test",
		Steps: []WorkflowStep{
			{
				ID:             "step1",
				WorkflowSchema: `{"type": "string"}`,
				SystemPrompt:   "Be helpful.",
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var reconstructed WorkflowDef
	if err := json.Unmarshal(data, &reconstructed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if reconstructed.Steps[0].SystemPrompt != original.Steps[0].SystemPrompt {
		t.Errorf("prompt mismatch after JSON roundtrip: got %q, want %q", reconstructed.Steps[0].SystemPrompt, original.Steps[0].SystemPrompt)
	}
}
