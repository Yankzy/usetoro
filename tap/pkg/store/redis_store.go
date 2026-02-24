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

// SaveContract stores a contract in Redis as JSON with no TTL (audit trail).
func (r *RedisStore) SaveContract(ctx context.Context, c *core.Contract) error {
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal contract: %w", err)
	}
	if err := r.rdb.Set(ctx, contractKey(c.ID), data, 0).Err(); err != nil {
		return fmt.Errorf("failed to save contract to redis: %w", err)
	}
	return nil
}

// GetContract retrieves a contract from Redis by ID.
func (r *RedisStore) GetContract(ctx context.Context, id string) (*core.Contract, error) {
	data, err := r.rdb.Get(ctx, contractKey(id)).Result()
	if err == redis.Nil {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("failed to get contract from redis: %w", err)
	}

	var c core.Contract
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		return nil, fmt.Errorf("failed to unmarshal contract: %w", err)
	}
	return &c, nil
}

// UpdateContractStatus atomically updates the status of a stored contract.
func (r *RedisStore) UpdateContractStatus(ctx context.Context, id string, status core.ContractStatus) error {
	c, err := r.GetContract(ctx, id)
	if err != nil {
		return err
	}
	c.Status = status
	return r.SaveContract(ctx, c)
}

func contractKey(id string) string {
	return "hive:contract:" + id
}
