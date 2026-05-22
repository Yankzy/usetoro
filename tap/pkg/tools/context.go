package tools

import (
	"context"

	"github.com/google/uuid"
)

// NewAgentContext creates a root AgentContext with a background abort context.
func NewAgentContext(agentType string, maxTurns int) AgentContext {
	return AgentContext{
		AgentID:   uuid.New().String(),
		ParentID:  "",
		AgentType: agentType,
		Abort:     context.Background(),
		TurnCount: 0,
		MaxTurns:  maxTurns,
	}
}

// NewChildContext creates a child AgentContext derived from a parent.
// The child inherits the parent's Abort context directly, so canceling the parent
// automatically cancels all descendants. The returned cancel function can be used
// to cancel only this child independently of the parent.
func NewChildContext(parent AgentContext, agentType string, maxTurns int) (AgentContext, context.CancelFunc) {
	childCtx, cancel := context.WithCancel(parent.Abort)
	return AgentContext{
		AgentID:   uuid.New().String(),
		ParentID:  parent.AgentID,
		AgentType: agentType,
		Abort:     childCtx,
		TurnCount: 0,
		MaxTurns:  maxTurns,
	}, cancel
}

// CancelableContext creates a root context that can be explicitly canceled.
func CancelableContext(agentType string, maxTurns int) (AgentContext, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	return AgentContext{
		AgentID:   uuid.New().String(),
		AgentType: agentType,
		Abort:     ctx,
		MaxTurns:  maxTurns,
	}, cancel
}
