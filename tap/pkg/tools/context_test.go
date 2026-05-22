package tools_test

import (
	"context"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/tools"
)

func TestNewAgentContext(t *testing.T) {
	ctx := tools.NewAgentContext("test-type", 10)

	if ctx.AgentID == "" {
		t.Error("AgentID should not be empty")
	}
	if ctx.ParentID != "" {
		t.Error("root context ParentID should be empty")
	}
	if ctx.AgentType != "test-type" {
		t.Errorf("AgentType = %q, want %q", ctx.AgentType, "test-type")
	}
	if ctx.MaxTurns != 10 {
		t.Errorf("MaxTurns = %d, want 10", ctx.MaxTurns)
	}
	if ctx.Abort == nil {
		t.Error("Abort context should not be nil")
	}
}

func TestNewChildContext(t *testing.T) {
	parent := tools.NewAgentContext("parent-type", 20)
	child, cancel := tools.NewChildContext(parent, "child-type", 5)
	defer cancel()

	if child.ParentID != parent.AgentID {
		t.Errorf("child ParentID = %q, want %q", child.ParentID, parent.AgentID)
	}
	if child.AgentType != "child-type" {
		t.Errorf("child AgentType = %q, want %q", child.AgentType, "child-type")
	}
	if child.MaxTurns != 5 {
		t.Errorf("child MaxTurns = %d, want 5", child.MaxTurns)
	}
	if child.AgentID == parent.AgentID {
		t.Error("child should have a different AgentID")
	}

	// Child context should not be canceled yet
	select {
	case <-child.Abort.Done():
		t.Error("child context should not be canceled initially")
	default:
	}
}

func TestContext_CancelPropagation(t *testing.T) {
	parent, parentCancel := tools.CancelableContext("parent-type", 10)
	defer parentCancel()

	child, childCancel := tools.NewChildContext(parent, "child-type", 5)
	defer childCancel()

	// Cancel parent — child should be canceled too
	parentCancel()

	select {
	case <-child.Abort.Done():
		// Expected — child was canceled
	case <-time.After(100 * time.Millisecond):
		t.Error("child context should have been canceled when parent was canceled")
	}
}

func TestCancelableContext(t *testing.T) {
	ctx, cancel := tools.CancelableContext("test", 10)

	// Should not be canceled initially
	select {
	case <-ctx.Abort.Done():
		t.Error("context should not be canceled initially")
	default:
	}

	cancel()

	select {
	case <-ctx.Abort.Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("context should be canceled after cancel()")
	}
}

func TestChildContext_IndependentCancel(t *testing.T) {
	parent, parentCancel := tools.CancelableContext("parent", 10)
	defer parentCancel()

	child, childCancel := tools.NewChildContext(parent, "child", 5)

	// Cancel only the child — parent should still be alive
	childCancel()

	select {
	case <-child.Abort.Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("child should be canceled")
	}

	select {
	case <-parent.Abort.Done():
		t.Error("parent should NOT be canceled when only child is canceled")
	default:
		// Expected
	}
}

// Ensure tools.NewAgentContext satisfies the interface contract
var _ context.Context = tools.NewAgentContext("test", 1).Abort
