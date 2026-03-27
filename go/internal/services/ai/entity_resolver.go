package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/infra/vector"
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

type VectorQuerier interface {
	QueryVectors(ctx context.Context, namespace string, vector []float32, topK int, filter map[string]interface{}) ([]vector.Match, error)
}

type TextEmbedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// EntityResolver handles robust metadata-filtered querying of entities
type EntityResolver struct {
	store        *store.Store
	vectorClient VectorQuerier
	embedder     TextEmbedder
	threshold    float64
}

// NewEntityResolver generates a configured resolver structure holding Fignode ecosystem services
// By typing as concrete pointers but storing as interfaces, we retain external caller compatibility while allowing internal mock injections.
func NewEntityResolver(s *store.Store, vc *vector.PineconeClient, e *vector.Embedder, threshold float64) *EntityResolver {
	resolver := &EntityResolver{
		store:     s,
		embedder:  e,
		threshold: threshold,
	}
	if vc != nil {
		resolver.vectorClient = vc
	}
	return resolver
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
	if r.vectorClient == nil {
		return nil, nil
	}

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

// ResolveVendor explicitly filters Pinecone for entity_type vendor.
func (r *EntityResolver) ResolveVendor(ctx context.Context, realmID, rawDescription string) (*EntityMatch, error) {
	if rawDescription == "" || r.vectorClient == nil {
		return nil, nil
	}

	queryVector, err := r.embedder.Embed(ctx, rawDescription)
	if err != nil {
		return nil, fmt.Errorf("failed to embed vendor description: %w", err)
	}

	filter := map[string]interface{}{
		"entity_type": map[string]interface{}{"$eq": "vendor"},
	}

	matches, err := r.vectorClient.QueryVectors(ctx, realmID, queryVector, 1, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to query vendor vectors: %w", err)
	}

	return r.getTopMatch(matches, "vendor")
}

// ResolveCustomer explicitly filters Pinecone for entity_type customer.
func (r *EntityResolver) ResolveCustomer(ctx context.Context, realmID, rawDescription string) (*EntityMatch, error) {
	if rawDescription == "" || r.vectorClient == nil {
		return nil, nil
	}

	queryVector, err := r.embedder.Embed(ctx, rawDescription)
	if err != nil {
		return nil, fmt.Errorf("failed to embed customer description: %w", err)
	}

	filter := map[string]interface{}{
		"entity_type": map[string]interface{}{"$eq": "customer"},
	}

	matches, err := r.vectorClient.QueryVectors(ctx, realmID, queryVector, 1, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to query customer vectors: %w", err)
	}

	return r.getTopMatch(matches, "customer")
}

// ResolveAccount merges contexts and strictly routes money_in and money_out transactions safely outside of cash mappings using MongoDB syntax.
func (r *EntityResolver) ResolveAccount(ctx context.Context, realmID, transactionType, rawDescription, resolvedEntityName string) (*EntityMatch, error) {
	if rawDescription == "" || r.vectorClient == nil {
		return nil, nil
	}

	enrichedText := fmt.Sprintf("Entity: %s. Description: %s", resolvedEntityName, rawDescription)

	queryVector, err := r.embedder.Embed(ctx, enrichedText)
	if err != nil {
		return nil, fmt.Errorf("failed to embed account context: %w", err)
	}

	var filter map[string]interface{}

	if transactionType == "money_out" {
		filter = map[string]interface{}{
			"entity_type": map[string]interface{}{"$eq": "account"},
			"classification": map[string]interface{}{
				"$in": []string{"Expense", "Asset", "Cost of Goods Sold"},
			},
			"account_type": map[string]interface{}{
				"$nin": []string{"Bank", "Credit Card", "Accounts Receivable", "Other Current Asset"},
			},
		}
	} else if transactionType == "money_in" {
		filter = map[string]interface{}{
			"entity_type": map[string]interface{}{"$eq": "account"},
			"classification": map[string]interface{}{
				"$in": []string{"Revenue", "Income", "Liability", "Equity"},
			},
			"account_type": map[string]interface{}{
				"$nin": []string{"Bank", "Accounts Payable"},
			},
		}
	} else {
		filter = map[string]interface{}{
			"entity_type": map[string]interface{}{"$eq": "account"},
		}
	}

	matches, err := r.vectorClient.QueryVectors(ctx, realmID, queryVector, 1, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to query account vectors: %w", err)
	}

	return r.getTopMatch(matches, "account")
}

// getTopMatch safely unpacks the structured Fignode metadata
func (r *EntityResolver) getTopMatch(matches []vector.Match, entityType string) (*EntityMatch, error) {
	if len(matches) == 0 {
		return nil, nil
	}

	top := matches[0]
	if top.Score < r.threshold {
		return nil, nil
	}

	name, ok := top.Metadata["name"].(string)
	if !ok {
		return nil, fmt.Errorf("metadata missing 'name' string key")
	}

	return &EntityMatch{
		ID:         top.ID,
		Name:       name,
		Score:      top.Score,
		EntityType: entityType,
		Source:     "vector",
	}, nil
}
