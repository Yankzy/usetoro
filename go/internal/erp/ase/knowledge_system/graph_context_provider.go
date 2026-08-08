package knowledge_system

import (
	"context"
	"fmt"

	"github.com/Yankzy/usetoro/internal/erp/ase"
)

// GraphContextProvider resolves Layer 1 Ground Truth Facts and Layer 2 Relationship Subgraphs for ASE DAG nodes.
type GraphContextProvider struct {
	graphStore *GraphStore
}

// NewGraphContextProvider creates a new GraphContextProvider.
func NewGraphContextProvider(graphStore *GraphStore) *GraphContextProvider {
	return &GraphContextProvider{
		graphStore: graphStore,
	}
}

// Name returns the provider registration key.
func (gcp *GraphContextProvider) Name() string {
	return "graph_knowledge_provider"
}

// Resolve queries grounded facts and relationship subgraphs for the provided micro-agent node.
func (gcp *GraphContextProvider) Resolve(
	ctx context.Context,
	node *ase.AutonomousSemanticEngineNode,
	config map[string]any,
	deps ase.ProviderDependencies,
) (any, error) {
	if node == nil || gcp.graphStore == nil {
		return nil, nil
	}

	realmID := node.RealmID
	if realmID == "" {
		return nil, fmt.Errorf("graph context provider: realmID is empty")
	}

	// Read URI from node payload if present
	var factURI string
	if uriVal, ok := node.Payload["fact_uri"].(string); ok {
		factURI = uriVal
	} else if docIDVal, ok := node.Payload["document_id"].(string); ok {
		docType, _ := node.Payload["document_type"].(string)
		if docType == "" {
			docType = "INVOICE"
		}
		factURI = fmt.Sprintf("fact:document:%s:%s", docType, docIDVal)
	}

	if factURI == "" {
		// Return empty context if node payload contains no fact URI pointer
		return nil, nil
	}

	// 1. Fetch Root Ground Truth Fact Node
	fact, err := gcp.graphStore.GetFactByURI(ctx, realmID, factURI)
	if err != nil {
		return nil, fmt.Errorf("graph context provider resolve fact: %w", err)
	}
	if fact == nil {
		return nil, nil
	}

	// 2. Traverse Graph Subgraph (1-hop neighbors)
	neighbors, err := gcp.graphStore.TraverseGraph(ctx, fact.FactID)
	if err != nil {
		deps.Logger.Warn("graph context provider traverse graph error", "fact_id", fact.FactID, "error", err)
	}

	result := map[string]any{
		"root_fact": fact,
		"neighbors": neighbors,
	}

	return result, nil
}
