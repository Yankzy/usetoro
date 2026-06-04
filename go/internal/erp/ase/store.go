package ase

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const (
	// Redis key prefixes
	activeAgentPrefix = "ase:active:"
	lockPrefix        = "ase:lock:"
)

// StateStore persists transaction agent state to Postgres and Redis.
type StateStore struct {
	pool  *pgxpool.Pool
	redis *redis.Client
}

// NewStateStore creates a new ASE state store.
func NewStateStore(pool *pgxpool.Pool, rdb *redis.Client) *StateStore {
	return &StateStore{pool: pool, redis: rdb}
}

// PersistNode writes the current ASENode state to fignode.staging_transactions.
// It updates v2_status, v2_transfer_hold_reason, confidence_score, macro_class,
// and account_type based on the node's top candidate.
func (s *StateStore) PersistNode(ctx context.Context, node *AutonomousSemanticEngineNode) error {
	query := `UPDATE fignode.staging_transactions
		SET status = $2,
		    error_message = $3,
		    confidence_score = $4,
		    macro_class = CASE WHEN $5::text != '' THEN $5::text ELSE macro_class END,
		    account_type = CASE WHEN $6::text != '' THEN $6::text ELSE account_type END,
		    predicted_vendor_name = CASE WHEN $7::text != '' AND $8::text = 'OUTFLOW' THEN $7::text ELSE predicted_vendor_name END,
		    predicted_customer_name = CASE WHEN $7::text != '' AND $8::text = 'INFLOW' THEN $7::text ELSE predicted_customer_name END,
		    ase_execution_trace = $9::jsonb,
		    updated_at = NOW()
		WHERE id = $1`
	var macroClass, accountType, entityName string
	if topMacro := node.TopCandidate(PropMacroClass); topMacro != nil {
		macroClass = topMacro.Value
	}
	if topAccType := node.TopCandidate(PropAccountType); topAccType != nil {
		accountType = topAccType.Value
	}
	if topEntity := node.TopCandidate(PropCounterparty); topEntity != nil {
		entityName = topEntity.Value
	}

	node.mu.RLock()
	traceBytes, _ := json.Marshal(node.ExecutionTrace)
	node.mu.RUnlock()

	_, err := s.pool.Exec(ctx, query,
		node.NodeID,
		string(node.GetState()),
		node.GetHoldReason(),
		node.GetConfidence(), // Unified confidence C replaces single-property confidence
		macroClass,
		accountType,
		entityName,
		node.CashDirection,
		traceBytes,
	)
	if err != nil {
		return fmt.Errorf("persist ase node: %w", err)
	}

	return nil
}

// PersistHoldReason updates only the hold reason and v2_status for a stalled agent.
func (s *StateStore) PersistHoldReason(ctx context.Context, node *AutonomousSemanticEngineNode) error {
	query := `UPDATE fignode.staging_transactions
		SET status = $2, error_message = $3, ase_execution_trace = $4::jsonb, updated_at = NOW()
		WHERE id = $1`

	node.mu.RLock()
	traceBytes, _ := json.Marshal(node.ExecutionTrace)
	node.mu.RUnlock()

	_, err := s.pool.Exec(ctx, query, node.NodeID, string(node.GetState()), node.GetHoldReason(), traceBytes)
	if err != nil {
		return fmt.Errorf("persist hold reason: %w", err)
	}
	return nil
}

// PersistReadyForSync marks the transaction as ready for QBO sync.
// This is the State Collapse point — only call when confidence >= threshold.
func (s *StateStore) PersistReadyForSync(ctx context.Context, node *AutonomousSemanticEngineNode) error {
	threshold := 0.98
	if cfg := GetConfig(node.TenantID, node.RealmID); cfg != nil {
		threshold = cfg.HyperParameters.ConfidenceThreshold
	}

	if node.GetConfidence() < threshold {
		return fmt.Errorf("guardrail: cannot mark node %s as READY_FOR_SYNC with unified confidence %f below %v", node.NodeID, node.GetConfidence(), threshold)
	}

	query := `UPDATE fignode.staging_transactions
		SET status = $2,
		    error_message = NULL,
		    confidence_score = $3,
		    ase_execution_trace = $4::jsonb,
		    updated_at = NOW()
		WHERE id = $1`

	node.mu.RLock()
	traceBytes, _ := json.Marshal(node.ExecutionTrace)
	node.mu.RUnlock()

	_, err := s.pool.Exec(ctx, query, node.NodeID, string(StateReadyForSync), node.GetConfidence(), traceBytes)
	if err != nil {
		return fmt.Errorf("persist ready for sync: %w", err)
	}
	return nil
}

// CacheActiveAgent stores the serialized node in Redis for fast lookup.
func (s *StateStore) CacheActiveAgent(ctx context.Context, node *AutonomousSemanticEngineNode) error {
	if s.redis == nil {
		return nil
	}
	data, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("marshal agent for cache: %w", err)
	}
	key := activeAgentPrefix + node.NodeID
	
	ttl := 10 * time.Minute
	if cfg := GetConfig(node.TenantID, node.RealmID); cfg != nil {
		ttl = time.Duration(cfg.HyperParameters.ActiveAgentTTLMinutes) * time.Minute
	}
	
	return s.redis.Set(ctx, key, data, ttl).Err()
}

// GetCachedAgent retrieves an active agent from Redis.
// The onStateChange callback must be provided to restore telemetry — it is not
// serialized and must be re-injected after deserialization.
func (s *StateStore) GetCachedAgent(ctx context.Context, nodeID string, onStateChange func(node *AutonomousSemanticEngineNode, oldState, newState NodeState)) (*AutonomousSemanticEngineNode, error) {
	if s.redis == nil {
		return nil, fmt.Errorf("redis not available")
	}
	key := activeAgentPrefix + nodeID
	data, err := s.redis.Get(ctx, key).Bytes()
	if err != nil {
		return nil, fmt.Errorf("get cached agent: %w", err)
	}
	var node AutonomousSemanticEngineNode
	if err := json.Unmarshal(data, &node); err != nil {
		return nil, fmt.Errorf("unmarshal cached agent: %w", err)
	}
	node.InitInternalState()
	if onStateChange != nil {
		node.SetOnStateChange(onStateChange)
	}
	return &node, nil
}

// RemoveCachedAgent deletes an agent from the active Redis cache.
func (s *StateStore) RemoveCachedAgent(ctx context.Context, nodeID string) error {
	if s.redis == nil {
		return nil
	}
	return s.redis.Del(ctx, activeAgentPrefix+nodeID).Err()
}

// AcquireLock attempts to acquire a distributed lock for the given node ID.
// Returns true if the lock was acquired.
func (s *StateStore) AcquireLock(ctx context.Context, nodeID string) (bool, error) {
	if s.redis == nil {
		return true, nil // No Redis = no distributed locking, always succeed.
	}
	key := lockPrefix + nodeID
	
	ttl := 30 * time.Second
	if cfg := GetConfig("", ""); cfg != nil {
		ttl = time.Duration(cfg.HyperParameters.LockTTLSeconds) * time.Second
	}
	
	ok, err := s.redis.SetNX(ctx, key, "1", ttl).Result()
	if err != nil {
		return false, fmt.Errorf("acquire lock: %w", err)
	}
	return ok, nil
}

// ReleaseLock releases a distributed lock for the given node ID.
func (s *StateStore) ReleaseLock(ctx context.Context, nodeID string) error {
	if s.redis == nil {
		return nil
	}
	return s.redis.Del(ctx, lockPrefix+nodeID).Err()
}
