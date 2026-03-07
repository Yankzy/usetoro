package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/infrastructure/vector"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/lithammer/fuzzysearch/fuzzy"
)

// EntityMatch represents a potential Vendor/Customer match
type EntityMatch struct {
	ID         string
	Score      float64
	Name       string
	Source     string // "db", "vector", "fuzzy"
	EntityType string // "vendor", "customer"
}

// EntityResolver handles robust matching of entities (Vendors/Customers)
type EntityResolver struct {
	store        *store.Store
	vectorClient *vector.PineconeClient
	embedder     *vector.Embedder
	threshold    float64
}

// NewEntityResolver creates a new entity resolver
func NewEntityResolver(s *store.Store, vc *vector.PineconeClient, e *vector.Embedder, threshold float64) *EntityResolver {
	return &EntityResolver{
		store:        s,
		vectorClient: vc,
		embedder:     e,
		threshold:    threshold,
	}
}

// ResolveEntity attempts to find the best match for an entity name using a 3-layer strategy
func (r *EntityResolver) ResolveEntity(ctx context.Context, realmID, entityType, name string) (*EntityMatch, error) {
	if name == "" {
		return nil, nil
	}

	// Layer 1: PostgreSQL Exact or Synonym Match
	match, err := r.layer1DBMatch(ctx, realmID, entityType, name)
	if err != nil {
		return nil, err
	}
	if match != nil {
		return match, nil
	}

	// Layer 2: Pinecone Semantic Match
	matches, err := r.layer2VectorMatch(ctx, realmID, entityType, name)
	if err != nil {
		return nil, err
	}

	// Layer 3: Fuzzy Verification / Candidate Ranking
	if len(matches) > 0 {
		return r.layer3FuzzyRank(name, matches), nil
	}

	return nil, nil
}

func (r *EntityResolver) layer1DBMatch(ctx context.Context, realmID, entityType, name string) (*EntityMatch, error) {
	switch entityType {
	case "vendor":
		vendor, err := r.store.Queries.GetVendorByNameOrSynonym(ctx, database.GetVendorByNameOrSynonymParams{
			RealmID:     realmID,
			DisplayName: name,
		})
		if err == nil {
			return &EntityMatch{
				ID:         vendor.ErpID,
				Score:      1.0,
				Name:       vendor.DisplayName,
				Source:     "db",
				EntityType: "vendor",
			}, nil
		}
	case "customer":
		// Optimized: Push search to DB
		customer, err := r.store.Queries.GetCustomerByName(ctx, database.GetCustomerByNameParams{
			RealmID:     realmID,
			DisplayName: name,
		})
		if err == nil {
			return &EntityMatch{
				ID:         customer.ErpID,
				Score:      1.0,
				Name:       customer.DisplayName,
				Source:     "db",
				EntityType: "customer",
			}, nil
		}
	}
	return nil, nil
}

func (r *EntityResolver) layer2VectorMatch(ctx context.Context, realmID, entityType, name string) ([]EntityMatch, error) {
	queryVector, err := r.embedder.Embed(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to embed entity name: %w", err)
	}

	filter := map[string]interface{}{
		"entity_type": entityType,
	}

	matches, err := r.vectorClient.QueryVectors(ctx, realmID, queryVector, 5, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to query vector database: %w", err)
	}

	var results []EntityMatch
	for _, m := range matches {
		if m.Score < r.threshold {
			continue
		}

		// Safe extraction to prevent panic
		name, ok := m.Metadata["name"].(string)
		if !ok {
			continue // Skip malformed results
		}

		results = append(results, EntityMatch{
			ID:         m.ID,
			Score:      m.Score,
			Name:       name,
			Source:     "vector",
			EntityType: entityType,
		})
	}
	return results, nil
}

func (r *EntityResolver) layer3FuzzyRank(target string, candidates []EntityMatch) *EntityMatch {
	if len(candidates) == 0 {
		return nil
	}

	var bestMatch *EntityMatch
	var maxScore float64 = -1.0

	for i := range candidates {
		candidate := &candidates[i] // Pointer to avoid copying

		// Normalized Levenshtein (0.0 to 1.0 where 1.0 is exact match)
		dist := fuzzy.LevenshteinDistance(strings.ToLower(target), strings.ToLower(candidate.Name))
		maxLen := len(target)
		if len(candidate.Name) > maxLen {
			maxLen = len(candidate.Name)
		}

		fuzzyScore := 0.0
		if maxLen > 0 {
			fuzzyScore = 1.0 - (float64(dist) / float64(maxLen))
		}

		// Weighted Score: 70% Semantic (Vector), 30% Syntax (Fuzzy)
		// Adjust weights based on real-world testing
		finalScore := (candidate.Score * 0.7) + (fuzzyScore * 0.3)

		if finalScore > maxScore {
			maxScore = finalScore
			bestMatch = candidate
			bestMatch.Score = finalScore // Update the score to reflect the hybrid confidence
			bestMatch.Source = "hybrid_vector_fuzzy"
		}
	}

	return bestMatch
}

// Learn updates the shadow DB based on user corrections to improve future matching
func (r *EntityResolver) Learn(ctx context.Context, realmID, rawInput, userCorrectionID, correctionType string) error {
	if correctionType != "vendor" {
		// Currently only active learning for vendors is implemented via synonyms
		return nil
	}

	// TODO: Async Job - Re-embed this vendor with the new synonym included in the text to improve semantic matching for future variations.

	// 1. Get the vendor being corrected to
	// 1. Get the vendor being corrected to
	vendor, err := r.store.Queries.GetVendorByERPID(ctx, database.GetVendorByERPIDParams{
		RealmID: realmID,
		ErpID:   userCorrectionID,
	})
	if err != nil {
		return fmt.Errorf("failed to fetch vendor for learning: %w", err)
	}

	// 2. Parse existing synonyms
	var synonyms []string
	if vendor.AiSynonyms != nil {
		// Assuming ai_synonyms is stored as JSONB in PG and mapped to []byte in Go
		// Actually sqlc might map JSONB to a custom type or []byte. Let's assume []byte.
		_ = json.Unmarshal(vendor.AiSynonyms, &synonyms)
	}

	// 3. Add rawInput as a new synonym if it's unique
	isNew := true
	for _, s := range synonyms {
		if strings.EqualFold(s, rawInput) {
			isNew = false
			break
		}
	}

	if isNew {
		synonyms = append(synonyms, rawInput)
		data, _ := json.Marshal(synonyms)
		// 4. Update vendor synonyms in DB
		// 4. Update vendor synonyms in DB
		return r.store.Queries.UpdateVendorSynonymsByERPID(ctx, database.UpdateVendorSynonymsByERPIDParams{
			RealmID:    realmID,
			ErpID:      userCorrectionID,
			AiSynonyms: data,
		})
	}

	return nil
}
