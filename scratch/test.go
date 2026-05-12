package main

import (
	"encoding/json"
	"fmt"

	"github.com/tidwall/gjson"
)

func main() {
	schemaStr := `{
		"type": "object",
		"properties": {
			"session_id": { "type": "string" }
		},
		"required": ["session_id"]
	}`
	payload := []byte(`{"realm_id":"abc"}`)
	triggerStr := `{"session_id":"test-session-123"}`

	properties := gjson.Get(schemaStr, "properties").Map()
	shapedPayload := make(map[string]json.RawMessage, len(properties))

	for key := range properties {
		var found gjson.Result
		found = gjson.Get(string(payload), key)
		if !found.Exists() && triggerStr != "" {
			found = gjson.Get(triggerStr, key)
		}
		if found.Exists() {
			shapedPayload[key] = json.RawMessage(found.Raw)
		}
	}

	finalBytes, _ := json.Marshal(shapedPayload)
	fmt.Printf("Shaped Payload: %s\n", string(finalBytes))
}
