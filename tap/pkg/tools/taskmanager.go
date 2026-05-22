package tools

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// TaskState tracks a single background task (sub-agent or shell command).
type TaskState struct {
	ID          string
	Type        string    // "agent", "shell"
	Status      string    // "pending", "running", "completed", "failed", "killed"
	Description string
	StartTime   time.Time
	EndTime     time.Time
	Output      string
	Error       string
	Cancel      context.CancelFunc
}

// TaskManager tracks all background tasks in memory.
type TaskManager struct {
	mu    sync.RWMutex
	tasks map[string]*TaskState
}

// NewTaskManager returns an initialized TaskManager.
func NewTaskManager() *TaskManager {
	return &TaskManager{
		tasks: make(map[string]*TaskState),
	}
}

// Register adds a new task and returns its ID. The caller should start the task
// and call Update when it completes.
func (tm *TaskManager) Register(taskType, description string, cancel context.CancelFunc) string {
	id := uuid.New().String()[:8]
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.tasks[id] = &TaskState{
		ID:          id,
		Type:        taskType,
		Status:      "running",
		Description: description,
		StartTime:   time.Now(),
		Cancel:      cancel,
	}
	return id
}

// Update changes the status of a task. Called by the goroutine when it completes.
func (tm *TaskManager) Update(id, status, output, errMsg string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	ts, ok := tm.tasks[id]
	if !ok {
		return
	}
	ts.Status = status
	ts.Output = output
	ts.Error = errMsg
	ts.EndTime = time.Now()
}

// Kill cancels a running task.
func (tm *TaskManager) Kill(id string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	ts, ok := tm.tasks[id]
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	if ts.Status != "running" {
		return fmt.Errorf("task %s is not running (status: %s)", id, ts.Status)
	}
	if ts.Cancel != nil {
		ts.Cancel()
	}
	ts.Status = "killed"
	ts.EndTime = time.Now()
	return nil
}

// Get returns the current state of a task.
func (tm *TaskManager) Get(id string) (*TaskState, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	ts, ok := tm.tasks[id]
	return ts, ok
}

// List returns a copy of all tasks.
func (tm *TaskManager) List() []TaskState {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	out := make([]TaskState, 0, len(tm.tasks))
	for _, ts := range tm.tasks {
		out = append(out, *ts)
	}
	return out
}
