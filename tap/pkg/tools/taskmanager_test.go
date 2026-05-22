package tools_test

import (
	"context"
	"testing"

	"github.com/Yankzy/usetoro/tap/pkg/tools"
)

func TestTaskManager_RegisterAndGet(t *testing.T) {
	tm := tools.NewTaskManager()
	id := tm.Register("agent", "test task", nil)

	ts, ok := tm.Get(id)
	if !ok {
		t.Fatal("task not found after register")
	}
	if ts.Type != "agent" {
		t.Errorf("Type = %q, want 'agent'", ts.Type)
	}
	if ts.Status != "running" {
		t.Errorf("Status = %q, want 'running'", ts.Status)
	}
	if ts.Description != "test task" {
		t.Errorf("Description = %q, want 'test task'", ts.Description)
	}
}

func TestTaskManager_Update(t *testing.T) {
	tm := tools.NewTaskManager()
	id := tm.Register("shell", "test", nil)

	tm.Update(id, "completed", "output text", "")

	ts, ok := tm.Get(id)
	if !ok {
		t.Fatal("task not found after update")
	}
	if ts.Status != "completed" {
		t.Errorf("Status = %q, want 'completed'", ts.Status)
	}
	if ts.Output != "output text" {
		t.Errorf("Output = %q, want 'output text'", ts.Output)
	}
	if ts.EndTime.IsZero() {
		t.Error("EndTime should be set after completion")
	}
}

func TestTaskManager_UpdateWithError(t *testing.T) {
	tm := tools.NewTaskManager()
	id := tm.Register("agent", "failing task", nil)

	tm.Update(id, "failed", "", "something went wrong")

	ts, _ := tm.Get(id)
	if ts.Status != "failed" {
		t.Errorf("Status = %q, want 'failed'", ts.Status)
	}
	if ts.Error != "something went wrong" {
		t.Errorf("Error = %q, want 'something went wrong'", ts.Error)
	}
}

func TestTaskManager_Kill(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tm := tools.NewTaskManager()
	id := tm.Register("agent", "killable task", cancel)

	err := tm.Kill(id)
	if err != nil {
		t.Fatalf("Kill failed: %v", err)
	}

	ts, _ := tm.Get(id)
	if ts.Status != "killed" {
		t.Errorf("Status = %q, want 'killed'", ts.Status)
	}

	// Context should be canceled
	select {
	case <-ctx.Done():
		// Expected
	default:
		t.Error("context should be canceled after kill")
	}
}

func TestTaskManager_KillNonExistent(t *testing.T) {
	tm := tools.NewTaskManager()
	err := tm.Kill("does-not-exist")
	if err == nil {
		t.Error("expected error for non-existent task")
	}
}

func TestTaskManager_KillAlreadyCompleted(t *testing.T) {
	tm := tools.NewTaskManager()
	id := tm.Register("shell", "done task", nil)
	tm.Update(id, "completed", "done", "")

	err := tm.Kill(id)
	if err == nil {
		t.Error("expected error when killing completed task")
	}
}

func TestTaskManager_GetNonExistent(t *testing.T) {
	tm := tools.NewTaskManager()
	_, ok := tm.Get("does-not-exist")
	if ok {
		t.Error("expected false for non-existent task")
	}
}

func TestTaskManager_List(t *testing.T) {
	tm := tools.NewTaskManager()
	tm.Register("agent", "task 1", nil)
	tm.Register("shell", "task 2", nil)
	tm.Register("agent", "task 3", nil)

	list := tm.List()
	if len(list) != 3 {
		t.Errorf("List() returned %d tasks, want 3", len(list))
	}
}
