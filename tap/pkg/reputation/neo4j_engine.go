package reputation

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/identity"
	"github.com/redis/go-redis/v9"
)

// Neo4jReputationEngine utilizes Neo4j for raw evaluation edges and Redis for caching the calculated ReputationScore.
type Neo4jReputationEngine struct {
	graph       identity.Graph // Assuming the core Identity graph is used for pushing raw eval edges
	redisClient *redis.Client
}

// NewNeo4jReputationEngine instantiates the Reputation Engine.
func NewNeo4jReputationEngine(graph identity.Graph, rdb *redis.Client) *Neo4jReputationEngine {
	return &Neo4jReputationEngine{
		graph:       graph,
		redisClient: rdb,
	}
}

// SubmitOutcome stores the raw outcome as a graph edge and recalculates/caches the score.
func (e *Neo4jReputationEngine) SubmitOutcome(ctx context.Context, eval *Evaluation) error {
	// 1. Push evaluation edge to Graph Database
	edge := &identity.GraphEdge{
		ID:        eval.TaskID, // Ideally a UUID tying to the contract
		SourceDID: eval.Submitter,
		TargetDID: eval.Target,
		Type:      "EVALUATED_" + eval.Outcome, // e.g., "EVALUATED_SUCCESS"
		Weight:    eval.Weight,
		CreatedAt: time.Now().UTC(),
	}

	if err := e.graph.AddEdge(ctx, edge); err != nil {
		return fmt.Errorf("failed to submit outcome to graph: %w", err)
	}

	// 2. Recalculate context score
	// In a real system, would involve pulling all edges for this target and applying time decay.
	// We'll mock the decay factor based on current vs old edge timestamps.
	newScore := &ReputationScore{
		AgentDID:    eval.Target,
		Capability:  "default", // Could be extracted from Task
		Score:       calculateDecayedScore(eval), // Placeholder logic
		Confidence:  0.8,
		LastUpdated: time.Now().UTC(),
	}

	// 3. Cache in Redis
	return e.cacheScore(ctx, newScore)
}

func calculateDecayedScore(eval *Evaluation) float64 {
	// Non-linear decay placeholder
	base := 1.0
	if eval.Outcome != "SUCCESS" {
		base = -1.0
	}
	// Time decay (e.g. less weight if very old)
	ageDays := time.Since(time.Unix(eval.Timestamp, 0)).Hours() / 24.0
	return base * math.Exp(-0.05*ageDays)
}

func (e *Neo4jReputationEngine) cacheScore(ctx context.Context, score *ReputationScore) error {
	key := fmt.Sprintf("reputation:%s:%s", score.AgentDID, score.Capability)
	data, _ := json.Marshal(score)
	// Cache it essentially forever, but TTLs could be implemented if it goes stale.
	return e.redisClient.Set(ctx, key, data, 24*time.Hour).Err()
}

// GetScore reads directly from the Redis cache for sub-millisecond retrieval.
func (e *Neo4jReputationEngine) GetScore(ctx context.Context, did, capability string) (*ReputationScore, error) {
	key := fmt.Sprintf("reputation:%s:%s", did, capability)
	data, err := e.redisClient.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			// No established reputation -> return baseline
			return &ReputationScore{
				AgentDID:   did,
				Capability: capability,
				Score:      0.5,
				Confidence: 0.1,
				LastUpdated: time.Now().UTC(),
			}, nil
		}
		return nil, err
	}

	var score ReputationScore
	if err := json.Unmarshal(data, &score); err != nil {
		return nil, err
	}
	return &score, nil
}

// GetSystemWidePercentile is a placeholder for global APOC rankings like PageRank.
func (e *Neo4jReputationEngine) GetSystemWidePercentile(ctx context.Context, did, capability string) (float64, error) {
	// APOC PageRank query against graph would go here.
	return 0.9, nil
}
