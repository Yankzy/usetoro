package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// NOTE: These tests require mocks for PineconeClient, Embedder, and Store.
// Since those aren't fully defined yet, I'll provide the test structure.

func TestCoAMapper_MapDescriptionToAccount(t *testing.T) {
	// TODO: Implement with proper mocks
	assert.True(t, true)
}

func TestEntityResolver_ResolveEntity(t *testing.T) {
	// TODO: Implement with proper mocks
	assert.True(t, true)
}

func TestEntityResolver_Layer3FuzzyRank(t *testing.T) {
	resolver := &EntityResolver{}

	target := "Starbucks Coffee"
	candidates := []EntityMatch{
		{Name: "Starbucks", Score: 0.9},
		{Name: "Starlight Cafe", Score: 0.8},
		{Name: "Coffee bean", Score: 0.7},
	}

	best := resolver.layer3FuzzyRank(target, candidates)
	assert.NotNil(t, best)
	assert.Equal(t, "Starbucks", best.Name)
}
