package domain_tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Yankzy/usetoro/internal/erp/ase"
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

func (s *StateStore) prepareUpdateArgs(ctx context.Context, node *ase.AutonomousSemanticEngineNode) (
	macroClass string, accountType string, entityName string,
	vendorID *string, customerID *string, accountID *string, accountName string,
	reasoning string,
) {
	if topMacro := node.TopCandidate(ase.PropMacroClass); topMacro != nil {
		macroClass = topMacro.Value
		if topMacro.Reasoning != "" {
			reasoning += "[Macro Class]: " + topMacro.Reasoning + "\n"
		}
	}
	if topAccType := node.TopCandidate(ase.PropAccountType); topAccType != nil {
		accountType = topAccType.Value
		if topAccType.Reasoning != "" {
			reasoning += "[Account Type]: " + topAccType.Reasoning + "\n"
		}
	}
	if topEntity := node.TopCandidate(ase.PropCounterparty); topEntity != nil {
		entityName = topEntity.Value
		if topEntity.Reasoning != "" {
			reasoning += "[Counterparty]: " + topEntity.Reasoning + "\n"
		}
	}

	// Resolve IDs
	if entityName != "" {
		if cashDirection(node) == "OUTFLOW" {
			var vID string
			err := s.pool.QueryRow(ctx, "SELECT id::text FROM shadow_erp.vendors WHERE realm_id = $1 AND (display_name ILIKE $2 OR EXISTS (SELECT 1 FROM jsonb_array_elements_text(ai_synonyms) s WHERE $2 ILIKE s)) LIMIT 1", node.RealmID, entityName).Scan(&vID)
			if err == nil {
				vendorID = &vID
			}
		} else if cashDirection(node) == "INFLOW" {
			var cID string
			err := s.pool.QueryRow(ctx, "SELECT id::text FROM shadow_erp.customers WHERE realm_id = $1 AND display_name ILIKE $2 LIMIT 1", node.RealmID, entityName).Scan(&cID)
			if err == nil {
				customerID = &cID
			}
		}
	}

	if topAccountRef := node.TopCandidate(ase.PropAccountRef); topAccountRef != nil {
		if topAccountRef.Reasoning != "" {
			reasoning += "[Account Ref]: " + topAccountRef.Reasoning + "\n"
		}
		var aID string
		var aName string
		err := s.pool.QueryRow(ctx, "SELECT id::text, name FROM shadow_erp.accounts WHERE realm_id = $1 AND erp_id = $2 LIMIT 1", node.RealmID, topAccountRef.Value).Scan(&aID, &aName)
		if err == nil {
			accountID = &aID
			accountName = aName
		}
	}

	if accountID == nil && accountType != "" {
		var aID string
		var aName string
		err := s.pool.QueryRow(ctx, "SELECT id::text, name FROM shadow_erp.accounts WHERE realm_id = $1 AND name ILIKE $2 LIMIT 1", node.RealmID, accountType).Scan(&aID, &aName)
		if err == nil {
			accountID = &aID
			accountName = aName
		}
	}
	return
}

const updateStagingTxnQuery = `UPDATE fignode.staging_transactions
		SET status = $2,
		    error_message = $3,
		    confidence_score = $4,
		    macro_class = CASE WHEN $5::text != '' THEN $5::text ELSE macro_class END,
		    account_type = CASE WHEN $6::text != '' THEN $6::text ELSE account_type END,
		    predicted_vendor_name = CASE WHEN $7::text != '' AND $8::text = 'OUTFLOW' THEN $7::text ELSE predicted_vendor_name END,
		    predicted_customer_name = CASE WHEN $7::text != '' AND $8::text = 'INFLOW' THEN $7::text ELSE predicted_customer_name END,
		    cash_direction = CASE WHEN $8::text != '' THEN $8::text ELSE cash_direction END,
		    ase_execution_trace = $9::jsonb,
		    predicted_vendor_id = CASE WHEN $10::uuid IS NOT NULL THEN $10::uuid ELSE predicted_vendor_id END,
		    predicted_customer_id = CASE WHEN $11::uuid IS NOT NULL THEN $11::uuid ELSE predicted_customer_id END,
		    predicted_account_id = CASE WHEN $12::uuid IS NOT NULL THEN $12::uuid ELSE predicted_account_id END,
		    predicted_account_name = CASE WHEN $13::text != '' THEN $13::text ELSE predicted_account_name END,
		    ai_reasoning = CASE WHEN $14::text != '' THEN $14::text ELSE ai_reasoning END,
		    updated_at = NOW()
		WHERE id = $1`

// PersistNode writes the current ASENode state to fignode.staging_transactions.
// It updates v2_status, v2_transfer_hold_reason, confidence_score, macro_class,
// and account_type based on the node's top candidate.
func (s *StateStore) PersistNode(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	macroClass, accountType, entityName, vendorID, customerID, accountID, accountName, reasoning := s.prepareUpdateArgs(ctx, node)

	node.Mu.RLock()
	traceBytes, _ := json.Marshal(node.ExecutionTrace)
	node.Mu.RUnlock()

	_, err := s.pool.Exec(ctx, updateStagingTxnQuery,
		node.NodeID,
		string(node.GetState()),
		node.GetHoldReason(),
		node.GetConfidence(),
		macroClass,
		accountType,
		entityName,
		cashDirection(node),
		traceBytes,
		vendorID,
		customerID,
		accountID,
		accountName,
		reasoning,
	)
	if err != nil {
		return fmt.Errorf("persist ase node: %w", err)
	}

	return nil
}

// PersistHoldReason updates the hold reason, status, and intermediate classifications for a stalled agent.
func (s *StateStore) PersistHoldReason(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	macroClass, accountType, entityName, vendorID, customerID, accountID, accountName, reasoning := s.prepareUpdateArgs(ctx, node)

	node.Mu.RLock()
	traceBytes, _ := json.Marshal(node.ExecutionTrace)
	node.Mu.RUnlock()

	_, err := s.pool.Exec(ctx, updateStagingTxnQuery,
		node.NodeID,
		string(node.GetState()),
		node.GetHoldReason(),
		node.GetConfidence(),
		macroClass,
		accountType,
		entityName,
		cashDirection(node),
		traceBytes,
		vendorID,
		customerID,
		accountID,
		accountName,
		reasoning,
	)
	if err != nil {
		return fmt.Errorf("persist hold reason: %w", err)
	}
	return nil
}

// PersistReadyForSync marks the transaction as ready for QBO sync.
// This is the State Collapse point — only call when confidence >= threshold.
func (s *StateStore) PersistReadyForSync(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	threshold := 0.98
	if cfg := ase.GetConfig(node.TenantID, node.RealmID, node.DagName); cfg != nil {
		threshold = cfg.HyperParameters.ConfidenceThreshold
	}

	if node.GetConfidence() < threshold {
		return fmt.Errorf("guardrail: cannot mark node %s as READY_FOR_SYNC with unified confidence %f below %v", node.NodeID, node.GetConfidence(), threshold)
	}

	macroClass, accountType, entityName, vendorID, customerID, accountID, accountName, reasoning := s.prepareUpdateArgs(ctx, node)

	node.Mu.RLock()
	traceBytes, _ := json.Marshal(node.ExecutionTrace)
	node.Mu.RUnlock()

	_, err := s.pool.Exec(ctx, updateStagingTxnQuery,
		node.NodeID,
		string(ase.StateReadyForSync),
		"", // error_message
		node.GetConfidence(),
		macroClass,
		accountType,
		entityName,
		cashDirection(node),
		traceBytes,
		vendorID,
		customerID,
		accountID,
		accountName,
		reasoning,
	)
	if err != nil {
		return fmt.Errorf("persist ready for sync: %w", err)
	}
	return nil
}

// CacheActiveAgent stores the serialized node in Redis for fast lookup.
func (s *StateStore) CacheActiveAgent(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error {
	if s.redis == nil {
		return nil
	}
	data, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("marshal agent for cache: %w", err)
	}
	key := activeAgentPrefix + node.NodeID

	ttl := 10 * time.Minute
	if cfg := ase.GetConfig(node.TenantID, node.RealmID, node.DagName); cfg != nil {
		ttl = time.Duration(cfg.HyperParameters.ActiveAgentTTLMinutes) * time.Minute
	}

	return s.redis.Set(ctx, key, data, ttl).Err()
}

// GetCachedAgent retrieves an active agent from Redis.
// The onStateChange callback must be provided to restore telemetry — it is not
// serialized and must be re-injected after deserialization.
func (s *StateStore) GetCachedAgent(ctx context.Context, nodeID string, onStateChange func(node *ase.AutonomousSemanticEngineNode, oldState, newState ase.NodeState)) (*ase.AutonomousSemanticEngineNode, error) {
	if s.redis == nil {
		return nil, fmt.Errorf("redis not available")
	}
	key := activeAgentPrefix + nodeID
	data, err := s.redis.Get(ctx, key).Bytes()
	if err != nil {
		return nil, fmt.Errorf("get cached agent: %w", err)
	}
	var node ase.AutonomousSemanticEngineNode
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
	if cfg := ase.GetConfig("", "", "default"); cfg != nil {
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

func cashDirection(node *ase.AutonomousSemanticEngineNode) string {
	if node.Payload != nil {
		if val, ok := node.Payload["cash_direction"].(string); ok {
			return val
		}
	}
	return ""
}

// GetExecutionTrace returns the execution trace from fignode.staging_transactions
func (s *StateStore) GetExecutionTrace(ctx context.Context, nodeID string) ([]byte, error) {
	var traceJSON []byte
	traceQ := `SELECT ase_execution_trace FROM fignode.staging_transactions WHERE id = $1`
	err := s.pool.QueryRow(ctx, traceQ, nodeID).Scan(&traceJSON)
	return traceJSON, err
}

// UpdateNodeState updates the status of the node in fignode.staging_transactions
func (s *StateStore) UpdateNodeState(ctx context.Context, nodeID string, state ase.NodeState) error {
	updateQ := `UPDATE fignode.staging_transactions SET status = $2, updated_at = NOW() WHERE id = $1`
	_, err := s.pool.Exec(ctx, updateQ, nodeID, string(state))
	return err
}
