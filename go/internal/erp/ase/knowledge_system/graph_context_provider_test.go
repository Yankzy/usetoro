package knowledge_system

import (
	"context"
	"testing"

	"github.com/Yankzy/usetoro/internal/erp/ase"
)

func TestGraphContextProvider_Name(t *testing.T) {
	gcp := NewGraphContextProvider(nil)
	if gcp.Name() != "graph_knowledge_provider" {
		t.Fatalf("expected provider name 'graph_knowledge_provider', got %s", gcp.Name())
	}
}

func TestGraphContextProvider_NilNode(t *testing.T) {
	gcp := NewGraphContextProvider(nil)
	res, err := gcp.Resolve(context.Background(), nil, nil, ase.ProviderDependencies{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result for nil node, got %v", res)
	}
}

func TestGraphContextProvider_EmptyPayload(t *testing.T) {
	gcp := NewGraphContextProvider(nil)
	node := ase.NewASENode("t_tenant", "t_realm", "dag_test", map[string]any{})

	res, err := gcp.Resolve(context.Background(), node, nil, ase.ProviderDependencies{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result when payload has no fact_uri, got %v", res)
	}
}
