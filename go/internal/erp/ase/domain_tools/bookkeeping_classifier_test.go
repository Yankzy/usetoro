package domain_tools

import "github.com/Yankzy/usetoro/internal/erp/ase"

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBatchToRows_BasicExtraction(t *testing.T) {
	_ = ase.InitConfig(nil, nil, nil)

	node1 := ase.NewASENode("tenant-1", "realm-1", "dag-1", map[string]any{"raw_description": "Raw Desc 1", "cash_direction": "INFLOW", "raw_amount": "100.00"})
	node1.NodeID = "node-1"
	node2 := ase.NewASENode("tenant-1", "realm-1", "dag-1", map[string]any{"raw_description": "Raw Desc 2", "cash_direction": "OUTFLOW", "raw_amount": "200.00"})
	node2.NodeID = "node-2"

	cs := &BookkeepingClassifier{}
	rows := cs.batchToRows(context.Background(), []*ase.AutonomousSemanticEngineNode{node1, node2})

	assert.Len(t, rows, 2)
	assert.Equal(t, "Raw Desc 1", rows["node-1"].Description)
	assert.Equal(t, "Raw Desc 2", rows["node-2"].Description)
}

func TestBatchCashDirection(t *testing.T) {
	node1 := ase.NewASENode("tenant-1", "realm-1", "dag-1", map[string]any{"raw_description": "Raw Desc 1", "cash_direction": "INFLOW", "raw_amount": "100.00"})
	node1.NodeID = "node-1"
	node2 := ase.NewASENode("tenant-1", "realm-1", "dag-1", map[string]any{"raw_description": "Raw Desc 2", "cash_direction": "INFLOW", "raw_amount": "200.00"})
	node2.NodeID = "node-2"

	dir := batchCashDirection([]*ase.AutonomousSemanticEngineNode{node1, node2})
	assert.Equal(t, "INFLOW", dir)
}
