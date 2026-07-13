package ase

import (
	"context"
)

// StatePersister defines how an agent's state is saved and cached.
type StatePersister interface {
	PersistNode(ctx context.Context, node *AutonomousSemanticEngineNode) error
	PersistHoldReason(ctx context.Context, node *AutonomousSemanticEngineNode) error
	PersistReadyForSync(ctx context.Context, node *AutonomousSemanticEngineNode) error
	CacheActiveAgent(ctx context.Context, node *AutonomousSemanticEngineNode) error
	GetCachedAgent(ctx context.Context, nodeID string, onStateChange func(node *AutonomousSemanticEngineNode, oldState, newState NodeState)) (*AutonomousSemanticEngineNode, error)
	RemoveCachedAgent(ctx context.Context, nodeID string) error
	AcquireLock(ctx context.Context, nodeID string) (bool, error)
	ReleaseLock(ctx context.Context, nodeID string) error
	GetExecutionTrace(ctx context.Context, nodeID string) ([]byte, error)
	UpdateNodeState(ctx context.Context, nodeID string, state NodeState) error
}
