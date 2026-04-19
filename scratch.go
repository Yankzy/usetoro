package main

import (
	"encoding/json"
	"fmt"
)

func ExtractRows(data []byte) ([]map[string]interface{}, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}

	// 1. Try as Array
	var slice []map[string]interface{}
	if err := json.Unmarshal(data, &slice); err == nil && len(slice) > 0 {
		isRouteWrapper := true
		for _, item := range slice {
			_, hasRoute := item["route"]
			_, hasData := item["data"]
			if !hasRoute || !hasData || len(item) > 2 {
				isRouteWrapper = false
				break
			}
		}

		if isRouteWrapper {
			var combinedRows []map[string]interface{}
			for _, item := range slice {
				if dataBytes, err := json.Marshal(item["data"]); err == nil {
					if extracted, err := ExtractRows(dataBytes); err == nil {
						combinedRows = append(combinedRows, extracted...)
					}
				}
			}
			if len(combinedRows) > 0 {
				return combinedRows, nil
			}
		}

		return slice, nil
	}

	// 2. Try as Map of Rows (e.g. { "id1": { "row": 1 }, "id2": { "row": 2 } })
	var m map[string]map[string]interface{}
	if err := json.Unmarshal(data, &m); err == nil && len(m) > 0 {
		isRowMap := true
		rows := make([]map[string]interface{}, 0, len(m))
		for _, v := range m {
			if len(v) == 0 {
				isRowMap = false
				break
			}
			rows = append(rows, v)
		}
		if isRowMap && len(rows) > 0 {
			return rows, nil
		}
	}

	// 3. Recursive search in known wrapper fields
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(data, &generic); err == nil {
		searchFields := []string{"data", "input", "dependencies", "mapped_rows", "rows", "payload"}
		for _, field := range searchFields {
			if nextData, ok := generic[field]; ok && len(nextData) > 0 {
				if rows, err := ExtractRows(nextData); err == nil && len(rows) > 0 {
					return rows, nil
				}
			}
		}

		for _, nextData := range generic {
			if len(nextData) > 0 && (nextData[0] == '{' || nextData[0] == '[') {
				if rows, err := ExtractRows(nextData); err == nil && len(rows) > 0 {
					return rows, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("data is neither a JSON array nor a JSON object containing rows")
}

func main() {
	payload := []byte(` + "`" + `
{
  "dependencies": {
    "ambiguity_gate": [
      {
        "route": 0,
        "data": {
          "dependencies": {
            "map_columns": {
              "status": "success",
              "mapped_rows": {
                 "row_1": {"Amount": "10"},
                 "row_2": {"Amount": "20"}
              }
            }
          }
        }
      }
    ],
    "map_columns": {
      "status": "success",
      "mapped_rows": {
         "row_1": {"Amount": "10"},
         "row_2": {"Amount": "20"}
      }
    }
  }
}
	` + "`" + `)
	rows, err := ExtractRows(payload)
	fmt.Printf("ERROR: %v\nROWS: %v\nROWS LEN: %d\n", err, rows, len(rows))
}
