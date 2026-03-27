package micrion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

var (
	// ErrPaymentRequired is returned when an agent does not have enough micrions for the toll.
	ErrPaymentRequired = errors.New("402 Payment Required: Insufficient Micrions")
	// ErrAgentNotFound is returned when the agent's balance key does not exist.
	ErrAgentNotFound = errors.New("agent balance not found")
)

const (
	// BucketName is the name of the NATS JetStream KV bucket for micrions.
	BucketName = "micrions"
	// KeyPrefix is the format string for the agent's balance key.
	KeyPrefix = "agent.%s.balance"
	// MaxRetries defines the number of concurrent CAS attempts before failing.
	MaxRetries = 100
	// RollupThreshold defines how many burns before a Postgres synchronization occurs.
	RollupThreshold = 50
)

// ExecutionState is the JSON payload persisted in NATS KV.
type ExecutionState struct {
	EntityID         string `json:"entity_id"`
	Balance          int64  `json:"balance"`
	UncommittedBurns int64  `json:"uncommitted_burns"`
	UncommittedCount int    `json:"uncommitted_count"`
}

// SetupKV creates or retrieves the Micrions KV store bucket in JetStream.
func SetupKV(js nats.JetStreamContext) (nats.KeyValue, error) {
	kv, err := js.KeyValue(BucketName)
	if err == nil {
		return kv, nil
	}
	if !errors.Is(err, nats.ErrBucketNotFound) {
		return nil, fmt.Errorf("failed to access kv bucket: %w", err)
	}
	return js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:  BucketName,
		Storage: nats.FileStorage,
	})
}

func agentKey(agentDID string) string {
	safe := strings.ReplaceAll(agentDID, ":", "-")
	return fmt.Sprintf(KeyPrefix, safe)
}

// GetExecutionState safely reads the agent's current JSON state from NATS.
func GetExecutionState(kv nats.KeyValue, agentDID string) (ExecutionState, error) {
	key := agentKey(agentDID)
	entry, err := kv.Get(key)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) || err.Error() == "nats: key not found" {
			return ExecutionState{}, ErrAgentNotFound
		}
		return ExecutionState{}, fmt.Errorf("failed to get state: %w", err)
	}

	var state ExecutionState
	if err := json.Unmarshal(entry.Value(), &state); err != nil {
		return ExecutionState{}, fmt.Errorf("invalid json state format: %w", err)
	}

	return state, nil
}

// TopUp adds the specified amount of Micrions to the agent's balance.
func (m *WalletManager) TopUp(agentDID string, amount int64, entityID string) error {
	key := agentKey(agentDID)

	for i := 0; i < MaxRetries; i++ {
		entry, err := m.kv.Get(key)
		if err != nil {
			if errors.Is(err, nats.ErrKeyNotFound) || err.Error() == "nats: key not found" {
				// Base case: account initialization
				state := ExecutionState{
					EntityID: entityID,
					Balance:  amount,
				}
				data, _ := json.Marshal(state)
				_, err = m.kv.Create(key, data)
				if err != nil {
					time.Sleep(time.Duration(10+rand.Intn(20)) * time.Millisecond)
					continue
				}
				return nil
			}
			return fmt.Errorf("failed to get balance: %w", err)
		}

		var state ExecutionState
		if parseErr := json.Unmarshal(entry.Value(), &state); parseErr != nil {
			return fmt.Errorf("invalid state format: %w", parseErr)
		}

		state.Balance += amount
		data, _ := json.Marshal(state)

		_, updateErr := m.kv.Update(key, data, entry.Revision())
		if updateErr != nil {
			time.Sleep(time.Duration(10+rand.Intn(20)) * time.Millisecond)
			continue
		}
		return nil
	}

	return fmt.Errorf("top-up failed after %d retries", MaxRetries)
}

// MicroBurn deducts the toll amount using Compare-and-Set logic and handles Postgres rollups.
func (m *WalletManager) MicroBurn(ctx context.Context, agentDID string, toll int64) (int64, error) {
	key := agentKey(agentDID)

	for i := 0; i < MaxRetries; i++ {
		entry, err := m.kv.Get(key)
		if err != nil {
			if errors.Is(err, nats.ErrKeyNotFound) || err.Error() == "nats: key not found" {
				return 0, ErrAgentNotFound
			}
			return 0, fmt.Errorf("failed to get balance: %w", err)
		}

		var state ExecutionState
		if parseErr := json.Unmarshal(entry.Value(), &state); parseErr != nil {
			return 0, fmt.Errorf("invalid state format: %w", parseErr)
		}

		if state.Balance < toll {
			return state.Balance, ErrPaymentRequired
		}

		// Calculate new state
		state.Balance -= toll
		state.UncommittedBurns += toll
		state.UncommittedCount++

		// 50-Tx Rollup Logic (Two Generals Idempotency Lock)
		if state.UncommittedCount >= RollupThreshold {
			// Step A: Synchronously block and safely record the bulk burn to Postgres
			err = m.ledger.LogBulkBurn(ctx, state.EntityID, agentDID, state.UncommittedBurns, entry.Revision())
			if err != nil {
				return 0, fmt.Errorf("fiat ledger sync failed, aborting burn to preserve state: %w", err)
			}
			
			// Step B: Reset counters for the NATS CAS update
			state.UncommittedBurns = 0
			state.UncommittedCount = 0
		}

		data, _ := json.Marshal(state)

		// Execute CAS micro-burn
		_, updateErr := m.kv.Update(key, data, entry.Revision())
		if updateErr != nil {
			// Collision during CAS, retry the loop
			// If we passed the 50 Tx threshold, the LogBulkBurn handled above will be shielded 
			// by the Postgres Unique Constraint on the *next* iteration (which gets a NEW NATS revision).
			time.Sleep(time.Duration(10+rand.Intn(20)) * time.Millisecond)
			continue
		}

		return state.Balance, nil
	}

	return 0, fmt.Errorf("micro-burn failed after %d retries due to concurrent updates", MaxRetries)
}
