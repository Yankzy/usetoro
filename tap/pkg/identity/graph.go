package identity

import (
	"context"
	"time"
)

// GraphEdge represents a historical or current relationship between two Identities.
type GraphEdge struct {
	ID        string    `json:"id"`
	SourceDID string    `json:"source_did"`
	TargetDID string    `json:"target_did"`
	Type      string    `json:"type"` // e.g., "INTERACTED_WITH", "DISPUTED_WITH", "VOUCHED_FOR"
	Weight    float64   `json:"weight"`
	CreatedAt time.Time `json:"created_at"`
}

// Graph implementation is intended to be backed by a graph database (like Neo4j)
// to provide performant relationship queries and traversals.
type Graph interface {
	// AddEdge creates a new relationship or updates the weight of an existing one.
	AddEdge(ctx context.Context, edge *GraphEdge) error

	// GetEdges retrieves all relationships of a certain type for a DID.
	GetEdges(ctx context.Context, did, edgeType string) ([]*GraphEdge, error)

	// CalculateTrust returns a contextual degree of trust between two DIDs,
	// checking historical proximity in the interaction graph.
	CalculateTrust(ctx context.Context, sourceDID, targetDID string) (float64, error)
}
