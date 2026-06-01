package ase

import (
	"io/ioutil"
	"os"
	"testing"
	"time"
)

func TestParseCOAToDAGConfig(t *testing.T) {
	mockJSON := `[
		{
			"code": "Class 1",
			"name": "EQUITY",
			"children": [
				{
					"code": "11",
					"name": "CAPITAL",
					"role": "eq_capital"
				}
			]
		}
	]`

	cfg, err := ParseCOAToDAGConfig([]byte(mockJSON))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.EntryNode != "root" {
		t.Errorf("expected entry node to be 'root', got %s", cfg.EntryNode)
	}

	rootNode, ok := cfg.Nodes["root"]
	if !ok {
		t.Fatal("expected 'root' node to exist")
	}
	if rootNode.Children["class 1"] != "Class 1" {
		t.Errorf("expected root to have child 'Class 1', got %v", rootNode.Children)
	}

	classNode, ok := cfg.Nodes["Class 1"]
	if !ok {
		t.Fatal("expected 'Class 1' node to exist")
	}
	if classNode.Children["11"] != "11" {
		t.Errorf("expected Class 1 to have child '11', got %v", classNode.Children)
	}

	leafNode, ok := cfg.Nodes["11"]
	if !ok {
		t.Fatal("expected '11' node to exist")
	}
	if leafNode.Kind != "terminal" {
		t.Errorf("expected '11' to be kind 'terminal', got %s", leafNode.Kind)
	}
	if leafNode.ExecutionParams["role"] != "eq_capital" {
		t.Errorf("expected '11' to have role 'eq_capital', got %s", leafNode.ExecutionParams["role"])
	}
}

func TestBuildDAGFromCOAJSON(t *testing.T) {
	mockJSON := `[
		{
			"code": "Class 1",
			"name": "EQUITY",
			"children": [
				{
					"code": "11",
					"name": "CAPITAL",
					"role": "eq_capital"
				}
			]
		}
	]`

	logger := testLogger()
	dag, err := BuildDAGFromCOAJSON([]byte(mockJSON), logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dag.EntryNode == nil || dag.EntryNode.ID != "root" {
		t.Fatalf("expected entry node 'root', got %v", dag.EntryNode)
	}

	class1Node := dag.GetNode("Class 1")
	if class1Node == nil {
		t.Fatal("expected 'Class 1' node to exist in DAG")
	}

	leafNode := dag.GetNode("11")
	if leafNode == nil {
		t.Fatal("expected '11' node to exist in DAG")
	}
	if leafNode.Kind != "terminal" {
		t.Errorf("expected node '11' to be kind 'terminal', got %s", leafNode.Kind)
	}
}

func TestInitCOAConfig_HotReload(t *testing.T) {
	filePath := "./coa_tenant_test_hotreload.json"
	defer os.Remove(filePath)

	initialJSON := `[
		{
			"code": "Class 1",
			"name": "EQUITY",
			"children": [
				{
					"code": "11",
					"name": "CAPITAL"
				}
			]
		}
	]`

	if err := ioutil.WriteFile(filePath, []byte(initialJSON), 0644); err != nil {
		t.Fatalf("failed to write initial JSON: %v", err)
	}

	logger := testLogger()
	// This will pick up the local directory if run from within the package
	if err := InitCOAConfig(logger); err != nil {
		t.Fatalf("failed to init COA config: %v", err)
	}

	// wait briefly for the initial scan to complete if it was async (it's sync though)
	dag := GetCOADAG("test_hotreload", "")
	if dag == nil || dag.GetNode("Class 1") == nil {
		t.Fatal("expected initial COA DAG to be initialized")
	}

	updatedJSON := `[
		{
			"code": "Class 2",
			"name": "ASSETS",
			"children": [
				{
					"code": "21",
					"name": "CASH"
				}
			]
		}
	]`

	// Write updated content to trigger hot-reloading
	if err := ioutil.WriteFile(filePath, []byte(updatedJSON), 0644); err != nil {
		t.Fatalf("failed to write updated JSON: %v", err)
	}

	// Give a moment for fsnotify to fire and load the new config
	time.Sleep(200 * time.Millisecond)

	dag = GetCOADAG("test_hotreload", "")
	if dag == nil {
		t.Fatal("expected hot-reloaded DAG to be found")
	}
	if dag.GetNode("Class 2") == nil {
		t.Error("expected hot-reloaded DAG to contain 'Class 2'")
	}
	if dag.GetNode("Class 1") != nil {
		t.Error("expected hot-reloaded DAG to no longer contain 'Class 1'")
	}
}
