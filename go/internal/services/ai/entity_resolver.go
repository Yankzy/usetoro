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
				ID:         vendor.ID,
				Score:      1.0,
				Name:       vendor.DisplayName,
				Source:     "db",
				EntityType: "vendor",
			}, nil
		}
	case "customer":
		// Assuming similar query for customer exists or will be added
		customers, err := r.store.Queries.GetAllCustomersForRealm(ctx, realmID)
		if err == nil {
			for _, c := range customers {
				if strings.EqualFold(c.DisplayName, name) {
					return &EntityMatch{
						ID:         c.ID,
						Score:      1.0,
						Name:       c.DisplayName,
						Source:     "db",
						EntityType: "customer",
					}, nil
				}
			}
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
		results = append(results, EntityMatch{
			ID:         m.ID,
			Score:      m.Score,
			Name:       m.Metadata["name"].(string),
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

	// Use Levenshtein distance to verify the top semantic candidate
	// if the semantic score is high but string distance is also high, it might be a false positive
	best := &candidates[0]

	// Check all candidates for a better fuzzy match
	for i := range candidates {
		// Calculate Levenshtein distance (lower is better)
		// We normalize it: 1 - (distance / maxLen)
		distance := fuzzy.LevenshteinDistance(strings.ToLower(target), strings.ToLower(candidates[i].Name))
		maxLen := len(target)
		if len(candidates[i].Name) > maxLen {
			maxLen = len(candidates[i].Name)
		}

		fuzzyScore := 1.0
		if maxLen > 0 {
			fuzzyScore = 1.0 - (float64(distance) / float64(maxLen))
		}

		// Combined score: 60% Semantic, 40% Fuzzy
		combinedScore := (candidates[i].Score * 0.6) + (fuzzyScore * 0.4)

		if combinedScore > (best.Score*0.6+0.4) || (i == 0) {
			best = &candidates[i]
			// We can decide to update the score to the combined one
			// best.Score = combinedScore
		}
	}

	return best
}

// Learn updates the shadow DB based on user corrections to improve future matching
func (r *EntityResolver) Learn(ctx context.Context, realmID, rawInput, userCorrectionID, correctionType string) error {
	if correctionType != "vendor" {
		// Currently only active learning for vendors is implemented via synonyms
		return nil
	}

	// 1. Get the vendor being corrected to
	vendor, err := r.store.Queries.GetVendorByID(ctx, database.GetVendorByIDParams{
		RealmID: realmID,
		ID:      userCorrectionID,
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
		return r.store.Queries.UpdateVendorSynonyms(ctx, database.UpdateVendorSynonymsParams{
			RealmID:    realmID,
			ID:         userCorrectionID,
			AiSynonyms: data,
		})
	}

	return nil
}
