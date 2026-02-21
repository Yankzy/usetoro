package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLayer3FuzzyRank(t *testing.T) {
	resolver := &EntityResolver{}

	t.Run("Empty candidates returns nil", func(t *testing.T) {
		result := resolver.layer3FuzzyRank("target", []EntityMatch{})
		assert.Nil(t, result)
	})

	t.Run("Single candidate is returned", func(t *testing.T) {
		candidates := []EntityMatch{
			{Name: "Target", Score: 0.9},
		}
		result := resolver.layer3FuzzyRank("target", candidates)
		assert.NotNil(t, result)
		assert.Equal(t, "Target", result.Name)
	})

	t.Run("Exact match with high semantic score wins", func(t *testing.T) {
		candidates := []EntityMatch{
			{Name: "Target", Score: 0.95}, // Exact match, high semantic
			{Name: "Tar get", Score: 0.8}, // Fuzzy match, lower semantic
		}
		result := resolver.layer3FuzzyRank("target", candidates)
		assert.Equal(t, "Target", result.Name)
	})

	t.Run("Better fuzzy match overrides slightly better semantic match", func(t *testing.T) {
		// Scenario: Vector DB returns "Targer" (typo in DB?) with 0.88
		// and "Target" with 0.85 (maybe embedding was slightly off or noise)
		// but "Target" is exact string match.

		// Wait, if "Target" is exact match, fuzzy score is 1.0.
		// "Targer" fuzzy score: Levenshtein=1. Len=6. Score = 1 - 1/6 = 0.833.

		// Candidate A ("Targer"): Semantic 0.88. Fuzzy 0.83. Combined: 0.88*0.7 + 0.83*0.3 = 0.616 + 0.249 = 0.865
		// Candidate B ("Target"): Semantic 0.85. Fuzzy 1.0. Combined: 0.85*0.7 + 1.0*0.3 = 0.595 + 0.3 = 0.895

		// Result should be "Target"

		candidates := []EntityMatch{
			{Name: "Targer", Score: 0.88},
			{Name: "Target", Score: 0.85},
		}
		result := resolver.layer3FuzzyRank("Target", candidates)
		assert.Equal(t, "Target", result.Name)
	})

	t.Run("High semantic score wins despite lower fuzzy score", func(t *testing.T) {
		// "Home Depot" vs "Home Depot Inc"
		// Target: "Home Depot"
		// Candidate A: "Home Depot Inc" (Semantic 0.95, Fuzzy ?)
		// Candidate B: "Home Depo" (Semantic 0.70, Fuzzy ~0.9)

		// "Home Depot Inc" (len 14). Dist("Home Depot", "Home Depot Inc") = 4.
		// MaxLen = 14. Fuzzy = 1 - 4/14 = 1 - 0.28 = 0.72.
		// Combined A: 0.95*0.7 + 0.72*0.3 = 0.665 + 0.216 = 0.881

		// "Home Depo" (len 9). Dist("Home Depot", "Home Depo") = 1.
		// MaxLen = 10. Fuzzy = 1 - 1/10 = 0.9.
		// Combined B: 0.70*0.7 + 0.9*0.3 = 0.49 + 0.27 = 0.76.

		// Winner should be "Home Depot Inc"

		candidates := []EntityMatch{
			{Name: "Home Depot Inc", Score: 0.95},
			{Name: "Home Depo", Score: 0.70},
		}
		result := resolver.layer3FuzzyRank("Home Depot", candidates)
		assert.Equal(t, "Home Depot Inc", result.Name)
	})
}
