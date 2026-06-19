package tools

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
	"github.com/nats-io/nats.go"
)

type mockEventBus struct {
	PublishedMessages []struct {
		Subject string
		Data    []byte
	}
}

var _ core.EventBus = (*mockEventBus)(nil)

func (b *mockEventBus) Publish(subject string, data []byte) error {
	b.PublishedMessages = append(b.PublishedMessages, struct {
		Subject string
		Data    []byte
	}{Subject: subject, Data: data})
	return nil
}
func (b *mockEventBus) PublishCore(subject string, data []byte) error { return nil }
func (b *mockEventBus) RequestWithContext(ctx context.Context, subject string, data []byte) (*nats.Msg, error) {
	return nil, nil
}
func (b *mockEventBus) QueueSubscribe(subj, queue string, cb nats.MsgHandler, opts ...nats.SubOpt) (*nats.Subscription, error) {
	return nil, nil
}

func TestScanSkillsCatalog(t *testing.T) {
	tenantID := "test-skills-realm"
	testBaseDir := filepath.Join("docs", "skills", tenantID)
	err := os.MkdirAll(testBaseDir, 0755)
	if err != nil {
		t.Fatalf("failed to create path: %v", err)
	}
	defer os.RemoveAll(testBaseDir)

	// Create valid skill
	skillDir1 := filepath.Join(testBaseDir, "calc")
	err = os.MkdirAll(skillDir1, 0755)
	if err != nil {
		t.Fatalf("failed to create skill folder: %v", err)
	}

	content1 := `---
name: calc
description: Performs basic math
version: 1.0.0
inputs:
  type: object
  properties:
    op: {type: string}
    a: {type: integer}
    b: {type: integer}
  required: [op, a, b]
entrypoint: scripts/calc.py
---
Detailed manual workflow here.`
	err = os.WriteFile(filepath.Join(skillDir1, "SKILL.md"), []byte(content1), 0644)
	if err != nil {
		t.Fatalf("failed to write skill manifest: %v", err)
	}

	// Create invalid skill (missing name)
	skillDir2 := filepath.Join(testBaseDir, "invalid-skill")
	err = os.MkdirAll(skillDir2, 0755)
	if err != nil {
		t.Fatalf("failed to create skill folder: %v", err)
	}
	content2 := `---
description: Missing name metadata
version: 1.0.0
entrypoint: scripts/run.py
---
body`
	err = os.WriteFile(filepath.Join(skillDir2, "SKILL.md"), []byte(content2), 0644)
	if err != nil {
		t.Fatalf("failed to write invalid manifest: %v", err)
	}

	ctx := context.Background()
	bus := &mockEventBus{}
	toolsList, err := ScanSkillsCatalog(ctx, tenantID, bus, "did:toro:agent:test")
	if err != nil {
		t.Fatalf("unexpected error scanning catalog: %v", err)
	}

	if len(toolsList) != 1 {
		t.Fatalf("expected exactly 1 parsed skill tool, got %d", len(toolsList))
	}

	tool := toolsList[0]
	if tool.Name() != "calc" {
		t.Errorf("expected tool name 'calc', got %s", tool.Name())
	}
	if tool.Description() != "Performs basic math" {
		t.Errorf("expected description 'Performs basic math', got %s", tool.Description())
	}

	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatalf("failed to unmarshal input schema: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("expected type object in schema, got %v", schema["type"])
	}
}

func TestExecuteDynamicSkill(t *testing.T) {
	tenantID := "test-executor-realm"
	testBaseDir := filepath.Join("docs", "skills", tenantID)
	skillDir := filepath.Join(testBaseDir, "math-solver")
	scriptsDir := filepath.Join(skillDir, "scripts")

	err := os.MkdirAll(scriptsDir, 0755)
	if err != nil {
		t.Fatalf("failed to create scripts path: %v", err)
	}
	defer os.RemoveAll(testBaseDir)

	manifestContent := `---
name: math-solver
description: Solve simple equations
version: 1.0.0
inputs:
  type: object
  properties:
    val: {type: integer}
  required: [val]
entrypoint: scripts/solve.py
---`
	err = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(manifestContent), 0644)
	if err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	pythonScript := `
import sys
import json
import os

try:
    data = sys.stdin.read()
    inputs = json.loads(data)
    val = inputs.get("val", 0)
    tenant = os.environ.get("TENANT_ID", "")
    realm = os.environ.get("REALM_ID", "")
    
    result = {
        "status": "success",
        "doubled": val * 2,
        "tenant_id": tenant,
        "realm_id": realm
    }
    print(json.dumps(result))
except Exception as e:
    sys.stderr.write(str(e))
    sys.exit(1)
`
	err = os.WriteFile(filepath.Join(scriptsDir, "solve.py"), []byte(pythonScript), 0644)
	if err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	mockBus := &mockEventBus{}
	ctx := context.Background()
	agentDID := "did:toro:agent:math-assistant"

	toolsList, err := ScanSkillsCatalog(ctx, tenantID, mockBus, agentDID)
	if err != nil {
		t.Fatalf("unexpected error scanning: %v", err)
	}

	if len(toolsList) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(toolsList))
	}

	tool := toolsList[0]
	input := map[string]any{"val": float64(21)} // Go JSON decoder yields float64 for numbers

	output, err := tool.Call(ctx, input)
	if err != nil {
		t.Fatalf("unexpected error executing Call: %v", err)
	}

	expectedStatusMsg := "Task dispatched asynchronously to skill math-solver. I will suspend execution and wait. You will receive an INFORM message when the task completes."
	if output != expectedStatusMsg {
		t.Errorf("expected status output %q, got %q", expectedStatusMsg, output)
	}

	// Verify asynchronous task queueing envelope in mock event bus
	if len(mockBus.PublishedMessages) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(mockBus.PublishedMessages))
	}

	published := mockBus.PublishedMessages[0]
	expectedSubject := fmt.Sprintf("public_python.execute.%s.math-solver", tenantID)
	if published.Subject != expectedSubject {
		t.Errorf("expected subject %q, got %q", expectedSubject, published.Subject)
	}

	var env core.Envelope
	if err := json.Unmarshal(published.Data, &env); err != nil {
		t.Fatalf("failed to unmarshal FIPA envelope: %v", err)
	}

	if env.SenderDID != agentDID {
		t.Errorf("expected sender DID %s, got %s", agentDID, env.SenderDID)
	}
	if env.Performative != core.REQUEST {
		t.Errorf("expected performative 'request', got %v", env.Performative)
	}

	// Decode Envelope body and simulate the python worker execution
	var reqBody map[string]any
	if err := json.Unmarshal(env.Body, &reqBody); err != nil {
		t.Fatalf("failed to unmarshal envelope body: %v", err)
	}

	scriptCode, _ := reqBody["script"].(string)
	scriptInput, _ := reqBody["input"].(map[string]any)
	tID, _ := reqBody["tenant_id"].(string)
	retSubject, _ := reqBody["return_subject"].(string)

	expectedReturnSubject := core.BuildAgentInbox(agentDID)
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
		Status   string `json:"status"`
		Doubled  int    `json:"doubled"`
		TenantID string `json:"tenant_id"`
		RealmID  string `json:"realm_id"`
	}
	if err := json.Unmarshal(stdoutBytes, &result); err != nil {
		t.Fatalf("failed to unmarshal script output: %v, raw: %s", err, string(stdoutBytes))
	}

	if result.Status != "success" || result.Doubled != 42 || result.TenantID != tenantID {
		t.Errorf("unexpected script results: %+v", result)
	}
}

func TestExecuteDirectoryTraversalSafety(t *testing.T) {
	tenantID := "test-traversal-realm"
	testBaseDir := filepath.Join("docs", "skills", tenantID)
	skillDir := filepath.Join(testBaseDir, "traversal")

	err := os.MkdirAll(skillDir, 0755)
	if err != nil {
		t.Fatalf("failed to create path: %v", err)
	}
	defer os.RemoveAll(testBaseDir)

	// Create manifest with a malicious entrypoint path trying to get outside skillDir
	manifestContent := `---
name: traversal
description: Path traversal attack
version: 1.0.0
inputs:
  type: object
entrypoint: ../../../malicious.py
---`
	err = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(manifestContent), 0644)
	if err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	ctx := context.Background()
	bus := &mockEventBus{}
	toolsList, err := ScanSkillsCatalog(ctx, tenantID, bus, "did:toro:agent:test")
	if err != nil {
		t.Fatalf("unexpected error scanning: %v", err)
	}

	if len(toolsList) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(toolsList))
	}

	tool := toolsList[0]
	_, err = tool.Call(ctx, nil)
	if err == nil {
		t.Fatal("expected traversal error, got nil")
	}

	if !strings.Contains(err.Error(), "directory traversal detected") {
		t.Errorf("unexpected error message: %v", err)
	}
}
