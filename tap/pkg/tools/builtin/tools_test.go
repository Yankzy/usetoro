package builtin_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/Yankzy/usetoro/tap/pkg/tools/builtin"
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
