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
	filePath := filepath.Join(tmpDir, "test_workflow.yml")
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

// TestSubWorkflowLoading verifies that the sub_workflow field survives the full
// YAML → Viper → WorkflowDef → JSON → WorkflowDef round-trip used by UpsertWorkflowFromFile.
func TestSubWorkflowLoading(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "workflow-subwf-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	yamlContent := `
name: Bookkeeping Workflow
version: "1.0"
trigger_topic: events.accounting.bookkeeping
steps:
  - id: run_cleanup
    sub_workflow: "CSV Cleaner Pipeline"
    timeout: "300s"
    description: "Embed CSV cleanup as a sub-workflow."

  - id: run_rule_engine
    activity_type: workers.rule_bootstrap
    negotiate: false
    timeout: "300s"
    depends_on:
      - run_cleanup
`
	filePath := filepath.Join(tmpDir, "bookkeeping.yml")
	if err := os.WriteFile(filePath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write yaml: %v", err)
	}

	// --- Phase 1: YAML → Viper → WorkflowDef (the path taken by UpsertWorkflowFromFile) ---
	v := viper.New()
	v.SetConfigFile(filePath)
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("viper ReadInConfig: %v", err)
	}

	var wfDef WorkflowDef
	if err := v.Unmarshal(&wfDef); err != nil {
		t.Fatalf("viper Unmarshal: %v", err)
	}

	if len(wfDef.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(wfDef.Steps))
	}

	subStep := wfDef.Steps[0]
	if subStep.ID != "run_cleanup" {
		t.Errorf("expected step id 'run_cleanup', got %q", subStep.ID)
	}
	if subStep.SubWorkflow != "CSV Cleaner Pipeline" {
		t.Errorf("sub_workflow not loaded: got %q, want 'CSV Cleaner Pipeline'", subStep.SubWorkflow)
	}
	if subStep.ActivityType != "" {
		t.Errorf("sub_workflow step should have no activity_type, got %q", subStep.ActivityType)
	}

	// --- Phase 2: WorkflowDef → JSON → WorkflowDef (the DB serialization round-trip) ---
	data, err := json.Marshal(wfDef)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var roundTripped WorkflowDef
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if roundTripped.Steps[0].SubWorkflow != "CSV Cleaner Pipeline" {
		t.Errorf("sub_workflow lost after JSON round-trip: got %q", roundTripped.Steps[0].SubWorkflow)
	}
	if roundTripped.Steps[1].DependsOn[0] != "run_cleanup" {
		t.Errorf("depends_on not preserved after JSON round-trip: got %v", roundTripped.Steps[1].DependsOn)
	}
}

// TestNormalizeSkipsSubWorkflowSteps verifies that normalizeWorkflowDef does not set Complexity
// or auto-inject depends_on on sub-workflow steps, which would pollute the stored blueprint.
func TestNormalizeSkipsSubWorkflowSteps(t *testing.T) {
	def := WorkflowDef{
		Name: "Bookkeeping Workflow",
		Steps: []WorkflowStep{
			{
				ID:          "run_cleanup",
				SubWorkflow: "CSV Cleaner Pipeline",
				Timeout:     "300s",
			},
			{
				ID:           "bootstrap_rule_engine",
				ActivityType: "workers.rule_bootstrap",
				DependsOn:    []string{"run_cleanup"},
			},
			{
				ID:           "run_rule_engine",
				ActivityType: "workers.rule_evaluation",
				DependsOn:    []string{"bootstrap_rule_engine"},
			},
			{
				ID:           "classify",
				ActivityType: "agents.accounting.classify_outflow",
				// no DependsOn — normalizer should auto-wire to run_rule_engine (not bootstrap_rule_engine)
			},
		},
	}

	normalized, _ := normalizeWorkflowDef(def)

	subStep := normalized.Steps[0]

	// sub_workflow field must be preserved
	if subStep.SubWorkflow != "CSV Cleaner Pipeline" {
		t.Errorf("sub_workflow was cleared during normalization: got %q", subStep.SubWorkflow)
	}
	// Complexity must NOT be set on a sub-workflow step
	if subStep.Complexity != 0 {
		t.Errorf("sub_workflow step should have Complexity=0, got %v", subStep.Complexity)
	}
	// No auto depends_on should be injected on a sub-workflow step (it's index 0, so N/A, but guard anyway)
	if len(subStep.DependsOn) != 0 {
		t.Errorf("sub_workflow step should have no depends_on, got %v", subStep.DependsOn)
	}

	// The normal worker step (index 1) must keep its explicit depends_on unchanged
	workerStep := normalized.Steps[1]
	if len(workerStep.DependsOn) != 1 || workerStep.DependsOn[0] != "run_cleanup" {
		t.Errorf("worker step depends_on mutated: got %v", workerStep.DependsOn)
	}

	// The evaluation worker step (index 2) must keep its explicit depends_on
	evalStep := normalized.Steps[2]
	if len(evalStep.DependsOn) != 1 || evalStep.DependsOn[0] != "bootstrap_rule_engine" {
		t.Errorf("evaluation step depends_on mutated: got %v", evalStep.DependsOn)
	}

	// The agent step (index 3) must get auto-depends_on pointing to the evaluation step
	agentStep := normalized.Steps[3]
	if len(agentStep.DependsOn) != 1 || agentStep.DependsOn[0] != "run_rule_engine" {
		t.Errorf("agent step auto depends_on should point to 'run_rule_engine', got %v", agentStep.DependsOn)
	}
}
