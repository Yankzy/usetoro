package workers

import (
	"testing"
)

func TestExtractRows_Heuristics(t *testing.T) {
	// Case 1: Standard Array
	input1 := `[{"Amount": "10.00", "Date": "2024-01-01"}, {"Amount": "20.00", "Date": "2024-01-02"}]`
	rows1, err := ExtractRows([]byte(input1))
	if err != nil {
		t.Fatalf("failed Case 1: %v", err)
	}
	if len(rows1) != 2 {
		t.Errorf("expected 2 rows, got %d", len(rows1))
	}

	// Case 2: Map of Rows (as returned by mapping agent)
	input2 := `{
		"row_1": {"Amount": "10.00", "Date": "2024-01-01"},
		"row_2": {"Amount": "20.00", "Date": "2024-01-02"}
	}`
	rows2, err := ExtractRows([]byte(input2))
	if err != nil {
		t.Fatalf("failed Case 2: %v", err)
	}
	if len(rows2) != 2 {
		t.Errorf("expected 2 rows, got %d", len(rows2))
	}

	// Case 3: Nested Workflow Payload (The Buggy Case)
	input3 := `{
		"dependencies": {
			"map_columns": {
				"task_id": "123",
				"type": "proof.api",
				"data": {
					"status": "COLUMNS_MAPPED",
					"mapped_rows": {
						"row_1": {"Amount": "10.00", "Date": "2024-01-01"},
						"row_2": {"Amount": "20.00", "Date": "2024-01-02"}
					}
				}
			}
		}
	}`
	rows3, err := ExtractRows([]byte(input3))
	if err != nil {
		t.Fatalf("failed Case 3: %v", err)
	}
	if len(rows3) != 2 {
		t.Errorf("expected 2 rows, got %d. Resulting rows: %+v", len(rows3), rows3)
	}
}

func TestExtractRows_FIPAExclude(t *testing.T) {
	// Case 4: A FIPA Envelope should NOT be treated as a row map
	input4 := `{
		"id": "msg-123",
		"ts": "2024-01-01T00:00:00Z",
		"src": "did:toro:a",
		"dst": "did:toro:b",
		"perf": "inform",
		"body": {
			"rows": [{"a": 1}, {"a": 2}]
		}
	}`
	rows4, err := ExtractRows([]byte(input4))
	if err != nil {
		t.Fatalf("failed Case 4: %v", err)
	}
	if len(rows4) != 2 {
		t.Errorf("expected 2 rows extracted from body, got %d", len(rows4))
	}

	// Case 5: Switch Worker Output (Unified Format)
	input5 := `{
		"dependencies": {
			"map_columns": {
				"mapped_rows": {
					"row_1": {"Amount": "10.00", "Date": "2024-01-01"}
				}
			}
		},
		"route": 0
	}`
	rows5, err := ExtractRows([]byte(input5))
	if err != nil {
		t.Fatalf("failed Case 5: %v", err)
	}
	if len(rows5) != 1 {
		t.Errorf("expected 1 row found inside dependencies+route wrapper, got %d", len(rows5))
	}
}
