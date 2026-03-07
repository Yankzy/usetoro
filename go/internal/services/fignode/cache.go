package fignode

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Cache struct {
	rdb *redis.Client
}

func NewCache(rdb *redis.Client) *Cache {
	if rdb == nil {
		return nil
	}
	return &Cache{rdb: rdb}
}

const (
	keyLeaderboard = "fignode:leaderboard:"
	keyUserStats   = "fignode:user:%s:stats"
	keyBatch       = "fignode:batch:%s:current"

	ttlLeaderboard = 5 * time.Minute
	ttlStats       = 30 * time.Second
	ttlBatch       = 24 * time.Hour
)

// --- Leaderboard ---

func (c *Cache) GetLeaderboard(ctx context.Context, period string) ([]LeaderboardItem, bool) {
	if c == nil {
		return nil, false
	}
	data, err := c.rdb.Get(ctx, keyLeaderboard+period+":latest").Bytes()
	if err != nil {
		return nil, false
	}
	var items []LeaderboardItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, false
	}
	return items, true
}

func (c *Cache) SetLeaderboard(ctx context.Context, period string, items []LeaderboardItem) {
	if c == nil {
		return
	}
	data, err := json.Marshal(items)
	if err != nil {
		return
	}
	c.rdb.Set(ctx, keyLeaderboard+period+":latest", data, ttlLeaderboard)
}

// --- User Stats ---

func (c *Cache) GetStats(ctx context.Context, userID uuid.UUID) (*UserStats, bool) {
	if c == nil {
		return nil, false
	}
	key := formatKey(keyUserStats, userID.String())
	data, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil, false
	}
	var stats UserStats
	if err := json.Unmarshal(data, &stats); err != nil {
		return nil, false
	}
	return &stats, true
}

func (c *Cache) SetStats(ctx context.Context, userID uuid.UUID, stats *UserStats) {
	if c == nil {
		return
	}
	data, err := json.Marshal(stats)
	if err != nil {
		return
	}
	c.rdb.Set(ctx, formatKey(keyUserStats, userID.String()), data, ttlStats)
}

func (c *Cache) InvalidateStats(ctx context.Context, userID uuid.UUID) {
	if c == nil {
		return
	}
	c.rdb.Del(ctx, formatKey(keyUserStats, userID.String()))
}

// --- Batch tracking ---

func (c *Cache) GetBatchIDs(ctx context.Context, userID uuid.UUID) ([]string, bool) {
	if c == nil {
		return nil, false
	}
	key := formatKey(keyBatch, userID.String())
	data, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil, false
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return nil, false
	}
	return ids, true
}

func (c *Cache) SetBatchIDs(ctx context.Context, userID uuid.UUID, ids []string) {
	if c == nil {
		return
	}
	data, err := json.Marshal(ids)
	if err != nil {
		return
	}
	c.rdb.Set(ctx, formatKey(keyBatch, userID.String()), data, ttlBatch)
}

func formatKey(pattern, arg string) string {
	// Simple sprintf replacement to avoid importing fmt just for this
	result := make([]byte, 0, len(pattern)+len(arg))
	for i := 0; i < len(pattern); i++ {
		if i+1 < len(pattern) && pattern[i] == '%' && pattern[i+1] == 's' {
			result = append(result, arg...)
			i++
		} else {
			result = append(result, pattern[i])
		}
	}
	return string(result)
}
