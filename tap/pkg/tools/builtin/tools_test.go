package builtin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/Yankzy/usetoro/tap/pkg/tools/builtin"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestShellTool(t *testing.T) {
	st := &builtin.ShellTool{}
	if st.Name() != "Bash" {
		t.Errorf("Name() = %q, want 'Bash'", st.Name())
	}

	// Verify input schema is valid JSON
	var schema map[string]any
	if err := json.Unmarshal(st.InputSchema(), &schema); err != nil {
		t.Fatalf("InputSchema is not valid JSON: %v", err)
	}
	if _, ok := schema["properties"].(map[string]any)["command"]; !ok {
		t.Error("InputSchema missing 'command' property")
	}

	// Test basic command
	out, err := st.Call(context.Background(), map[string]any{
		"command": "echo hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello\n" {
		t.Errorf("output = %q, want %q", out, "hello\n")
	}
}

func TestShellTool_InvalidInput(t *testing.T) {
	st := &builtin.ShellTool{}
	_, err := st.Call(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for missing command")
	}
}

func TestFileReadTool(t *testing.T) {
	// Create a temp file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	content := "line 1\nline 2\nline 3\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ft := &builtin.FileReadTool{}
	if ft.Name() != "FileRead" {
		t.Errorf("Name() = %q, want 'FileRead'", ft.Name())
	}

	out, err := ft.Call(context.Background(), map[string]any{
		"path": testFile,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty output")
	}
}

func TestFileReadTool_NotFound(t *testing.T) {
	ft := &builtin.FileReadTool{}
	_, err := ft.Call(context.Background(), map[string]any{
		"path": "/nonexistent/file.txt",
	})
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestFileWriteTool(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "output.txt")

	ft := &builtin.FileWriteTool{}
	if ft.Name() != "FileWrite" {
		t.Errorf("Name() = %q, want 'FileWrite'", ft.Name())
	}

	out, err := ft.Call(context.Background(), map[string]any{
		"path":    testFile,
		"content": "hello world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty output")
	}

	// Verify file was written
	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Errorf("file content = %q, want %q", string(data), "hello world")
	}
}

func TestFileEditTool(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "edit_test.txt")
	content := "package main\n\nfunc main() {\n\tprintln(\"old\")\n}\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ft := &builtin.FileEditTool{}
	if ft.Name() != "FileEdit" {
		t.Errorf("Name() = %q, want 'FileEdit'", ft.Name())
	}

	// Test: replace single occurrence
	out, err := ft.Call(context.Background(), map[string]any{
		"path":       testFile,
		"old_string": "\"old\"",
		"new_string": "\"new\"",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty output")
	}

	data, _ := os.ReadFile(testFile)
	if string(data) != "package main\n\nfunc main() {\n\tprintln(\"new\")\n}\n" {
		t.Errorf("unexpected file content: %s", string(data))
	}
}

func TestFileEditTool_NotFound(t *testing.T) {
	ft := &builtin.FileEditTool{}
	_, err := ft.Call(context.Background(), map[string]any{
		"path":       "/nonexistent/file.txt",
		"old_string": "x",
		"new_string": "y",
	})
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestFileEditTool_OldStringNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("hello"), 0644)

	ft := &builtin.FileEditTool{}
	_, err := ft.Call(context.Background(), map[string]any{
		"path":       testFile,
		"old_string": "nonexistent",
		"new_string": "replacement",
	})
	if err == nil {
		t.Error("expected error when old_string not found")
	}
}

func TestGrepTool(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "search.txt")
	os.WriteFile(testFile, []byte("func TestMe() {}\nfunc OtherFunc() {}\n"), 0644)

	gt := &builtin.GrepTool{}
	if gt.Name() != "Grep" {
		t.Errorf("Name() = %q, want 'Grep'", gt.Name())
	}

	out, err := gt.Call(context.Background(), map[string]any{
		"pattern": "TestMe",
		"path":    tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" || out == "No matches found." {
		t.Error("expected matches for 'TestMe' pattern")
	}
}

func TestGrepTool_NoMatch(t *testing.T) {
	tmpDir := t.TempDir()
	gt := &builtin.GrepTool{}
	out, err := gt.Call(context.Background(), map[string]any{
		"pattern": "NoSuchPatternXYZ123",
		"path":    tmpDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Logf("grep output: %s", out)
}

func TestWebFetchTool(t *testing.T) {
	wf := &builtin.WebFetchTool{}
	if wf.Name() != "WebFetch" {
		t.Errorf("Name() = %q, want 'WebFetch'", wf.Name())
	}

	// Verify input schema is valid JSON
	var schema map[string]any
	if err := json.Unmarshal(wf.InputSchema(), &schema); err != nil {
		t.Fatalf("InputSchema is not valid JSON: %v", err)
	}
}

// TestToolResolution verifies the registry's ResolveTools filters correctly.
func TestToolResolution(t *testing.T) {
	reg := tools.NewAgentRegistry()
	reg.RegisterBuiltIns()

	allTools := map[string]tools.Tool{
		"Bash":      &builtin.ShellTool{},
		"FileRead":  &builtin.FileReadTool{},
		"FileWrite": &builtin.FileWriteTool{},
		"FileEdit":  &builtin.FileEditTool{},
		"Grep":      &builtin.GrepTool{},
		"WebFetch":  &builtin.WebFetchTool{},
		"Agent":     &builtin.AgentTool{},
	}

	t.Run("general-purpose gets all tools", func(t *testing.T) {
		def, ok := reg.Get("general-purpose")
		if !ok {
			t.Fatal("general-purpose not found")
		}
		resolved := reg.ResolveTools(def, allTools)
		if len(resolved) != 7 {
			t.Errorf("general-purpose got %d tools, want 7", len(resolved))
		}
	})

	t.Run("explore excludes writable tools", func(t *testing.T) {
		def, ok := reg.Get("explore")
		if !ok {
			t.Fatal("explore not found")
		}
		resolved := reg.ResolveTools(def, allTools)
		if _, ok := resolved["FileWrite"]; ok {
			t.Error("explore should exclude FileWrite")
		}
		if _, ok := resolved["FileEdit"]; ok {
			t.Error("explore should exclude FileEdit")
		}
		if _, ok := resolved["Agent"]; ok {
			t.Error("explore should exclude Agent")
		}
		if _, ok := resolved["Bash"]; !ok {
			t.Error("explore should include Bash")
		}
	})

	t.Run("explicit Agent exclusion", func(t *testing.T) {
		def, ok := reg.Get("general-purpose")
		if !ok {
			t.Fatal("general-purpose not found")
		}
		resolved := reg.ResolveTools(def, allTools, "Agent")
		if _, ok := resolved["Agent"]; ok {
			t.Error("Agent should be excluded when explicitly listed")
		}
	})

	t.Run("custom allowlist", func(t *testing.T) {
		def := tools.AgentDefinition{
			Type:  "custom",
			Tools: []string{"Bash", "FileRead"},
		}
		resolved := reg.ResolveTools(def, allTools)
		if len(resolved) != 2 {
			t.Errorf("custom got %d tools, want 2", len(resolved))
		}
	})
}

func TestEmailTool_DescriptionContainsSubjectGuidance(t *testing.T) {
	et := &builtin.EmailTool{}
	desc := et.Description()

	if desc == "" {
		t.Fatal("Description() returned empty string")
	}

	// Verify the new guidance sentence is present.
	want := "The subject must be a brief, specific summary of the email's purpose"
	if !contains(desc, want) {
		t.Errorf("Description() missing subject guidance.\nDescription: %s\nExpected to contain: %s", desc, want)
	}

	// Verify it still contains the existing Sarah persona info.
	wantSarah := "You are acting as 'Sarah'"
	if !contains(desc, wantSarah) {
		t.Errorf("Description() missing Sarah persona guidance.\nDescription: %s\nExpected to contain: %s", desc, wantSarah)
	}

	// Verify it tells NOT to use agent name.
	wantNoGeneric := "never use the agent name or a generic placeholder"
	if !contains(desc, wantNoGeneric) {
		t.Errorf("Description() missing prohibition against agent name/placeholder.\nDescription: %s\nExpected to contain: %s", desc, wantNoGeneric)
	}
}

func TestEmailTool_InputSchema_HasSubjectRequired(t *testing.T) {
	et := &builtin.EmailTool{}

	var schema map[string]any
	if err := json.Unmarshal(et.InputSchema(), &schema); err != nil {
		t.Fatalf("InputSchema is not valid JSON: %v", err)
	}

	// Verify "subject" is in the required array.
	required, ok := schema["required"].([]interface{})
	if !ok {
		t.Fatal("InputSchema missing 'required' array")
	}

	foundSubject := false
	for _, r := range required {
		if s, ok := r.(string); ok && s == "subject" {
			foundSubject = true
			break
		}
	}
	if !foundSubject {
		t.Errorf("'subject' not found in required fields. required = %v", required)
	}

	// Verify "to" and "body" are also required.
	foundTo := false
	foundBody := false
	for _, r := range required {
		if s, ok := r.(string); ok {
			if s == "to" {
				foundTo = true
			}
			if s == "body" {
				foundBody = true
			}
		}
	}
	if !foundTo {
		t.Error("'to' not found in required fields")
	}
	if !foundBody {
		t.Error("'body' not found in required fields")
	}
}

func TestEmailTool_InputSchema_SubjectDescriptionHasGuidance(t *testing.T) {
	et := &builtin.EmailTool{}

	var schema map[string]any
	if err := json.Unmarshal(et.InputSchema(), &schema); err != nil {
		t.Fatalf("InputSchema is not valid JSON: %v", err)
	}

	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("InputSchema missing 'properties'")
	}

	subject, ok := properties["subject"].(map[string]any)
	if !ok {
		t.Fatal("InputSchema missing 'subject' property")
	}

	subjectDesc, ok := subject["description"].(string)
	if !ok {
		t.Fatal("subject property missing 'description'")
	}

	// Verify the description contains the new specific guidance.
	wantExample := "Invoice #1234 Payment Confirmation"
	if !contains(subjectDesc, wantExample) {
		t.Errorf("subject description missing example: %q\nGot: %s", wantExample, subjectDesc)
	}

	wantNoGeneric := "Do NOT use the agent name or generic text"
	if !contains(subjectDesc, wantNoGeneric) {
		t.Errorf("subject description missing prohibition against generic text.\nGot: %s", subjectDesc)
	}

	wantNoGenericAlt := "'General Purpose Agent'"
	if !contains(subjectDesc, wantNoGenericAlt) {
		t.Errorf("subject description missing example of what NOT to use.\nGot: %s", subjectDesc)
	}

	// Verify subject has type "string".
	if typ, ok := subject["type"].(string); !ok || typ != "string" {
		t.Errorf("subject type = %v, want 'string'", subject["type"])
	}
}

func TestEmailTool_Name(t *testing.T) {
	et := &builtin.EmailTool{}
	if et.Name() != "SendEmail" {
		t.Errorf("Name() = %q, want %q", et.Name(), "SendEmail")
	}
}

func TestHistoryTool_Name(t *testing.T) {
	ht := &builtin.HistoryTool{}
	if ht.Name() != "FetchCommunicationHistory" {
		t.Errorf("Name() = %q, want %q", ht.Name(), "FetchCommunicationHistory")
	}
}

func TestHistoryTool_Description(t *testing.T) {
	ht := &builtin.HistoryTool{}
	desc := ht.Description()
	if desc == "" {
		t.Fatal("Description() returned empty string")
	}
	if !strings.Contains(desc, "communication history") {
		t.Errorf("Description() missing expected content: %s", desc)
	}
}

func TestHistoryTool_InputSchema(t *testing.T) {
	ht := &builtin.HistoryTool{}

	var schema map[string]any
	if err := json.Unmarshal(ht.InputSchema(), &schema); err != nil {
		t.Fatalf("InputSchema is not valid JSON: %v", err)
	}

	required, ok := schema["required"].([]interface{})
	if !ok {
		t.Fatal("InputSchema missing 'required' array")
	}
	foundClientHandle := false
	for _, r := range required {
		if s, ok := r.(string); ok && s == "client_handle" {
			foundClientHandle = true
			break
		}
	}
	if !foundClientHandle {
		t.Error("'client_handle' not found in required fields")
	}
}

func TestHistoryTool_Call_NilDB(t *testing.T) {
	ht := &builtin.HistoryTool{DB: nil}
	_, err := ht.Call(context.Background(), map[string]any{
		"client_handle": "client@test.com",
	})
	if err == nil {
		t.Error("expected error for nil DB")
	}
}

func TestHistoryTool_BodyFormatting(t *testing.T) {
	// Verify that conversation body renders as plain text,
	// not as the pgtype.Text struct representation.
	// This guards the fix on history.go line 94: c.StrippedText.String.
	conv := database.ToroCoreConversation{
		FromHandle:   "agent@test.com",
		ToHandle:     "client@test.com",
		StrippedText: pgtype.Text{String: "Hello, this is the body text.", Valid: true},
	}

	var sb strings.Builder
	c := conv
	sb.WriteString(fmt.Sprintf("--- Message ID: %s ---\n", c.ID))
	sb.WriteString(fmt.Sprintf("Time: %s\n", c.CreatedAt.Time.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("From: %s\n", c.FromHandle))
	sb.WriteString(fmt.Sprintf("To: %s\n", c.ToHandle))
	if c.Subject.Valid && c.Subject.String != "" {
		sb.WriteString(fmt.Sprintf("Subject: %s\n", c.Subject.String))
	}
	sb.WriteString(fmt.Sprintf("Body:\n%s\n\n", c.StrippedText.String))

	result := sb.String()

	if !strings.Contains(result, "Hello, this is the body text.") {
		t.Errorf("output missing body text: %s", result)
	}

	// Ensure the pgtype.Text struct representation does NOT appear.
	if strings.Contains(result, "Valid:") {
		t.Errorf("output contains struct representation of StrippedText: %s", result)
	}
}

func TestHistoryTool_BodyFormatting_EmptyBody(t *testing.T) {
	// Edge case from spec: empty string body renders as Body:\n\n\n
	conv := database.ToroCoreConversation{
		FromHandle:   "agent@test.com",
		ToHandle:     "client@test.com",
		StrippedText: pgtype.Text{String: "", Valid: true},
	}

	var sb strings.Builder
	c := conv
	sb.WriteString(fmt.Sprintf("--- Message ID: %s ---\n", c.ID))
	sb.WriteString(fmt.Sprintf("Time: %s\n", c.CreatedAt.Time.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("From: %s\n", c.FromHandle))
	sb.WriteString(fmt.Sprintf("To: %s\n", c.ToHandle))
	if c.Subject.Valid && c.Subject.String != "" {
		sb.WriteString(fmt.Sprintf("Subject: %s\n", c.Subject.String))
	}
	sb.WriteString(fmt.Sprintf("Body:\n%s\n\n", c.StrippedText.String))

	result := sb.String()

	if !strings.Contains(result, "Body:\n\n\n") {
		t.Errorf("expected empty body to render as 'Body:\\n\\n\\n', got: %s", result)
	}
}

// contains reports whether s contains substr.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
