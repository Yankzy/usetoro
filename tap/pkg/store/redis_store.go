package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// RedisStore is the Redis-backed implementation for production use.
type RedisStore struct {
	rdb *redis.Client
}

// NewRedisStore creates a new Redis-backed repository.
func NewRedisStore(rdb *redis.Client) *RedisStore {
	return &RedisStore{rdb: rdb}
}

// SaveContract stores a contract in Redis as JSON.
func (r *RedisStore) SaveContract(c *core.Contract) error {
	ctx := context.Background()
	key := fmt.Sprintf("hive:contract:%s", c.ID)

	// Marshal contract to JSON
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal contract: %w", err)
	}

	// Store in Redis with no TTL (contracts persist indefinitely for audit trail)
	if err := r.rdb.Set(ctx, key, data, 0).Err(); err != nil {
		return fmt.Errorf("failed to save contract to redis: %w", err)
	}

	return nil
}

// GetContract retrieves a contract from Redis by ID.
func (r *RedisStore) GetContract(id string) (*core.Contract, error) {
	ctx := context.Background()
	key := fmt.Sprintf("hive:contract:%s", id)

	// Fetch from Redis
	data, err := r.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("failed to get contract from redis: %w", err)
	}

	// Unmarshal JSON
	var contract core.Contract
	if err := json.Unmarshal([]byte(data), &contract); err != nil {
		return nil, fmt.Errorf("failed to unmarshal contract: %w", err)
	}

	return &contract, nil
}

// UpdateStatus updates the status of a contract in Redis.
func (r *RedisStore) UpdateStatus(id string, status core.ContractStatus) error {
	// Fetch the contract
	contract, err := r.GetContract(id)
	if err != nil {
		return err
	}

	// Update status
	contract.Status = status

	// Save back to Redis
	return r.SaveContract(contract)
}
