package tools_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/tools"
)

// mockTool is a simple tool that records its execution for testing.
type mockTool struct {
	name     string
	executed chan struct{}
	callFn   func(ctx context.Context, input map[string]any) (string, error)
}

func (t *mockTool) Name() string               { return t.name }
func (t *mockTool) Description() string         { return "mock tool" }
func (t *mockTool) InputSchema() json.RawMessage { return json.RawMessage(`{}`) }
func (t *mockTool) Call(ctx context.Context, input map[string]any) (string, error) {
	if t.executed != nil {
		t.executed <- struct{}{}
	}
	if t.callFn != nil {
		return t.callFn(ctx, input)
	}
	return "ok", nil
}

func TestExecuteTools_EmptyCalls(t *testing.T) {
	toolMap := map[string]tools.Tool{"Test": &mockTool{name: "Test"}}
	results, err := tools.ExecuteTools(context.Background(), nil, toolMap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestExecuteTools_UnknownTool(t *testing.T) {
	toolMap := map[string]tools.Tool{}
	results, err := tools.ExecuteTools(context.Background(), []tools.ToolCall{
		{ID: "1", Name: "Unknown"},
	}, toolMap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error == "" {
		t.Error("expected error for unknown tool")
	}
}

func TestExecuteTools_Parallel(t *testing.T) {
	// Bash and Grep are concurrent-safe and should run in parallel
	var mu sync.Mutex
	var order []string

	toolMap := map[string]tools.Tool{
		"Bash": &mockTool{
			name: "Bash",
			callFn: func(ctx context.Context, input map[string]any) (string, error) {
				time.Sleep(20 * time.Millisecond) // ensure both start before either finishes
				mu.Lock()
				order = append(order, "Bash")
				mu.Unlock()
				return "bash output", nil
			},
		},
		"Grep": &mockTool{
			name: "Grep",
			callFn: func(ctx context.Context, input map[string]any) (string, error) {
				mu.Lock()
				order = append(order, "Grep")
				mu.Unlock()
				return "grep output", nil
			},
		},
	}

	results, err := tools.ExecuteTools(context.Background(), []tools.ToolCall{
		{ID: "1", Name: "Bash"},
		{ID: "2", Name: "Grep"},
	}, toolMap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Both should have executed
	for _, r := range results {
		if r.Error != "" {
			t.Errorf("unexpected error for %s: %s", r.ToolCallID, r.Error)
		}
	}
}

func TestExecuteTools_Serial(t *testing.T) {
	// FileEdit and FileWrite are serial-only
	var mu sync.Mutex
	var order []string

	toolMap := map[string]tools.Tool{
		"FileEdit": &mockTool{
			name: "FileEdit",
			callFn: func(ctx context.Context, input map[string]any) (string, error) {
				mu.Lock()
				order = append(order, "FileEdit")
				mu.Unlock()
				time.Sleep(10 * time.Millisecond)
				return "edit output", nil
			},
		},
		"FileWrite": &mockTool{
			name: "FileWrite",
			callFn: func(ctx context.Context, input map[string]any) (string, error) {
				mu.Lock()
				order = append(order, "FileWrite")
				mu.Unlock()
				return "write output", nil
			},
		},
	}

	results, err := tools.ExecuteTools(context.Background(), []tools.ToolCall{
		{ID: "1", Name: "FileEdit"},
		{ID: "2", Name: "FileWrite"},
	}, toolMap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "FileEdit" || order[1] != "FileWrite" {
		t.Errorf("serial order = %v, want [FileEdit, FileWrite]", order)
	}
}

func TestExecuteTools_Mixed(t *testing.T) {
	// Parallel tools should all start before serial tools run
	var mu sync.Mutex
	var order []string
	barrier := make(chan struct{})

	toolMap := map[string]tools.Tool{
		"Bash": &mockTool{
			name: "Bash",
			callFn: func(ctx context.Context, input map[string]any) (string, error) {
				// Wait for barrier so all parallel tools overlap
				<-barrier
				mu.Lock()
				order = append(order, "Bash")
				mu.Unlock()
				return "bash", nil
			},
		},
		"Grep": &mockTool{
			name: "Grep",
			callFn: func(ctx context.Context, input map[string]any) (string, error) {
				<-barrier
				mu.Lock()
				order = append(order, "Grep")
				mu.Unlock()
				return "grep", nil
			},
		},
		"FileEdit": &mockTool{
			name: "FileEdit",
			callFn: func(ctx context.Context, input map[string]any) (string, error) {
				mu.Lock()
				order = append(order, "FileEdit")
				mu.Unlock()
				return "edit", nil
			},
		},
	}

	// Start execution — parallel tools block on barrier, serial tool waits for them
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(barrier)
	}()

	results, err := tools.ExecuteTools(context.Background(), []tools.ToolCall{
		{ID: "1", Name: "Bash"},
		{ID: "2", Name: "Grep"},
		{ID: "3", Name: "FileEdit"},
	}, toolMap)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// FileEdit should run AFTER the parallel tools complete
	mu.Lock()
	defer mu.Unlock()
	foundEdit := -1
	for i, name := range order {
		if name == "FileEdit" {
			foundEdit = i
		}
	}
	if foundEdit < 2 {
		t.Errorf("FileEdit should be last in order, got: %v", order)
	}
}
