package ase

import (
	"context"
	"log/slog"
	"testing"
)

type dummyVendorProvider struct{}

func (d *dummyVendorProvider) Name() string { return "vendors" }

func (d *dummyVendorProvider) Resolve(ctx context.Context, node *AutonomousSemanticEngineNode, config map[string]any, deps ProviderDependencies) (any, error) {
	vName, _ := node.Payload["vendor_name"].(string)
	if vName == "Afriquia" {
		return []any{
			map[string]any{"display_name": "AFRIQUIA", "default_expense_account_code": "614100"},
		}, nil
	}
	if vName == "Orange" {
		return []any{
			map[string]any{"display_name": "ORANGE MAROC", "default_expense_account_code": "614500"},
		}, nil
	}
	return nil, nil
}

func TestContextResolver_ResolveBatchContext(t *testing.T) {
	RegisterContextProvider(&dummyVendorProvider{})

	resolver := NewContextResolver()

	node1 := &AutonomousSemanticEngineNode{
		NodeID:  "node-1",
		Payload: map[string]any{"vendor_name": "Afriquia"},
	}
	node2 := &AutonomousSemanticEngineNode{
		NodeID:  "node-2",
		Payload: map[string]any{"vendor_name": "Orange"},
	}
	node3 := &AutonomousSemanticEngineNode{
		NodeID:  "node-3",
		Payload: map[string]any{"vendor_name": "Afriquia"}, // Duplicate vendor
	}

	batch := []*AutonomousSemanticEngineNode{node1, node2, node3}

	cfg := map[string]any{
		"vendors": map[string]any{
			"strategy": "db_similarity",
			"top_k":    5,
		},
	}

	merged, err := resolver.ResolveBatchContext(context.Background(), batch, cfg, ProviderDependencies{Logger: slog.Default()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	candVendors, ok := merged["candidate_vendors"].([]any)
	if !ok {
		t.Fatalf("expected candidate_vendors key in merged sharedContext")
	}

	// Should be deduplicated to 2 vendors: AFRIQUIA and ORANGE MAROC
	if len(candVendors) != 2 {
		t.Fatalf("expected 2 deduplicated vendors, got %d", len(candVendors))
	}
}
