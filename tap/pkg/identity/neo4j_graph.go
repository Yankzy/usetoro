package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Neo4jGraph implements the Graph interface using a Neo4j backend.
type Neo4jGraph struct {
	driver neo4j.DriverWithContext
}

// NewNeo4jGraph creates a new instance of Neo4jGraph.
func NewNeo4jGraph(uri, username, password string) (*Neo4jGraph, error) {
	driver, err := neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(username, password, ""))
	if err != nil {
		return nil, err
	}
	return &Neo4jGraph{driver: driver}, nil
}

// Close terminates the connection to the database.
func (g *Neo4jGraph) Close(ctx context.Context) error {
	return g.driver.Close(ctx)
}

// AddEdge creates a directed relationship between two agent DIDs.
func (g *Neo4jGraph) AddEdge(ctx context.Context, edge *GraphEdge) error {
	session := g.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	// Create nodes if they don't exist, and create/update the relationship.
	// Using APOC could simplify this if we had a dynamic relationship type, 
	// but static string concats for dynamic relationship types must be done safely in Go.
	// Since mapping generic strings to relationship types requires dynamic cypher,
	// we use APOC: `CALL apoc.create.relationship(src, type, {weight: ...}, dst)`
	
	cypher := `
		MERGE (src:Agent {did: $srcDID})
		MERGE (dst:Agent {did: $dstDID})
		WITH src, dst
		CALL apoc.create.relationship(src, $edgeType, {
			id: $edgeID,
			weight: $weight,
			created_at: $createdAt
		}, dst) YIELD rel
		RETURN rel
	`

	params := map[string]interface{}{
		"srcDID":    edge.SourceDID,
		"dstDID":    edge.TargetDID,
		"edgeType":  edge.Type,
		"edgeID":    edge.ID,
		"weight":    edge.Weight,
		"createdAt": edge.CreatedAt.Unix(),
	}

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		result, err := tx.Run(ctx, cypher, params)
		if err != nil {
			return nil, err
		}
		return result.Consume(ctx)
	})

	return err
}

// GetEdges retrieves relationships of a specific type originating from a given DID.
func (g *Neo4jGraph) GetEdges(ctx context.Context, did, edgeType string) ([]*GraphEdge, error) {
	session := g.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	// We use APOC to dynamically match relationship types
	cypher := `
		MATCH (src:Agent {did: $srcDID})
		CALL apoc.cypher.run("MATCH (src)-[r:" + $edgeType + "]->(dst) RETURN r, dst", {src: src}) YIELD value
		RETURN value.r AS rel, value.dst.did AS targetDid
	`

	params := map[string]interface{}{
		"srcDID":   did,
		"edgeType": edgeType,
	}

	res, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		result, err := tx.Run(ctx, cypher, params)
		if err != nil {
			return nil, err
		}

		var edges []*GraphEdge
		for result.Next(ctx) {
			rel := result.Record().Values[0].(neo4j.Relationship)
			targetDid := result.Record().Values[1].(string)

			props := rel.Props
			
			weight := 0.0
			if w, ok := props["weight"].(float64); ok {
				weight = w
			}

			var createdAt time.Time
			if ca, ok := props["created_at"].(int64); ok {
				createdAt = time.Unix(ca, 0)
			}

			edges = append(edges, &GraphEdge{
				ID:        props["id"].(string),
				SourceDID: did,
				TargetDID: targetDid,
				Type:      edgeType,
				Weight:    weight,
				CreatedAt: createdAt,
			})
		}
		return edges, result.Err()
	})

	if err != nil {
		return nil, err
	}

	if edges, ok := res.([]*GraphEdge); ok {
		return edges, nil
	}
	return nil, fmt.Errorf("unexpected result type")
}

// CalculateTrust computes a contextual trust score by traversing historical edges.
func (g *Neo4jGraph) CalculateTrust(ctx context.Context, sourceDID, targetDID string) (float64, error) {
	// A simple PageRank or shortest path trust weight can be calculated here.
	// For MVP, we return a baseline weight of 0.5 + any direct positive weights.
	return 0.8, nil // Placeholder
}
