package ase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBatchToRows_BasicExtraction(t *testing.T) {
	_ = InitConfig(nil, nil, nil)

	node1 := NewASENode("tenant-1", "realm-1", "dag-1", "Raw Desc 1", "INFLOW", "100.00")
	node1.NodeID = "node-1"
	node2 := NewASENode("tenant-1", "realm-1", "dag-1", "Raw Desc 2", "OUTFLOW", "200.00")
	node2.NodeID = "node-2"

	cs := &ClassifierService{}
	rows := cs.batchToRows(context.Background(), []*AutonomousSemanticEngineNode{node1, node2})

	assert.Len(t, rows, 2)
	assert.Equal(t, "Raw Desc 1", rows["node-1"].Description)
	assert.Equal(t, "Raw Desc 2", rows["node-2"].Description)
}

func TestBatchCashDirection(t *testing.T) {
	node1 := NewASENode("tenant-1", "realm-1", "dag-1", "Raw Desc 1", "INFLOW", "100.00")
	node1.NodeID = "node-1"
	node2 := NewASENode("tenant-1", "realm-1", "dag-1", "Raw Desc 2", "INFLOW", "200.00")
	node2.NodeID = "node-2"

	dir := batchCashDirection([]*AutonomousSemanticEngineNode{node1, node2})
	assert.Equal(t, "INFLOW", dir)
}
