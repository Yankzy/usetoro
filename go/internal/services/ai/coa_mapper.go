package ai

import (
	"context"
	"fmt"
	"sort"

	"github.com/Yankzy/usetoro/internal/infra/vector"
)

// AccountMatch represents a potential CoA match
type AccountMatch struct {
	AccountID string
	Score     float64
	Name      string
}

// CoAMapper handles semantic mapping of transaction descriptions to G/L accounts
type CoAMapper struct {
	vectorClient *vector.PineconeClient
	embedder     *vector.Embedder
	threshold    float64
}

// NewCoAMapper creates a new CoA mapper
func NewCoAMapper(vc *vector.PineconeClient, e *vector.Embedder, threshold float64) *CoAMapper {
	return &CoAMapper{
		vectorClient: vc,
		embedder:     e,
		threshold:    threshold,
	}
}

// MapDescriptionToAccount finds the best matching account for a given description
func (m *CoAMapper) MapDescriptionToAccount(ctx context.Context, realmID, description string, topK int) ([]AccountMatch, error) {
	if description == "" {
		return nil, nil
	}

	// 1. Get embedding for the description
	queryVector, err := m.embedder.Embed(ctx, description)
	if err != nil {
		return nil, fmt.Errorf("failed to embed description: %w", err)
	}

	// 2. Query Pinecone for matches in the specific realm (namespace)
	// Filter for account entity type
	filter := map[string]interface{}{
		"entity_type": "account",
	}

	matches, err := m.vectorClient.QueryVectors(ctx, realmID, queryVector, topK, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to query vector database: %w", err)
	}

	// 3. Convert matches to AccountMatch and filter by threshold
	var results []AccountMatch
	for _, match := range matches {
		if match.Score < m.threshold {
			continue
		}

		results = append(results, AccountMatch{
			AccountID: match.ID,
			Score:     match.Score,
			Name:      match.Metadata["name"].(string),
		})
	}

	// 4. Sort by score descending (Pinecone usually does this, but we filter so we ensure)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}
