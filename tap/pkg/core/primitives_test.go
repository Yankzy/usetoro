package core

import (
	"encoding/json"
	"testing"
)

func TestUnmarshalTaskPayload_Recursive(t *testing.T) {
	type FinalPayload struct {
		Message string `json:"message"`
	}

	raw := `{"message": "hello world"}`

	// 1. One level: input wrapper
	wrap1 := `{"input": ` + raw + `}`

	// 2. Two levels: Proof + input
	proof := Proof{
		Type: ProofAPI,
		Data: []byte(wrap1),
	}
	proofBytes, _ := json.Marshal(proof)

	// 3. Three levels: TaskDefinition + Proof + input
	taskDef := TaskDefinition{
		ID:      "task-123",
		Domain:  "test.domain",
		Payload: proofBytes,
	}
	taskDefBytes, _ := json.Marshal(taskDef)

	// 4. Four levels: Another TaskDefinition (Sub-workflow case)
	parentTaskDef := TaskDefinition{
		ID:      "parent-task",
		Domain:  "parent.domain",
		Payload: taskDefBytes,
	}
	parentBytes, _ := json.Marshal(parentTaskDef)

	var final FinalPayload
	err := UnmarshalTaskPayload(parentBytes, &final)
	if err != nil {
		t.Fatalf("Failed to unmarshal recursive payload: %v", err)
	}

	if final.Message != "hello world" {
		t.Errorf("Expected 'hello world', got '%s'", final.Message)
	}
}

func TestUnmarshalTaskPayload_DoubleEncodedString(t *testing.T) {
	type FinalPayload struct {
		Message string `json:"message"`
	}

	raw := `{"message": "hello string"}`
	// Double encode: it's a JSON string containing the JSON object
	doubleEncoded, _ := json.Marshal(raw)

	var final FinalPayload
	err := UnmarshalTaskPayload(doubleEncoded, &final)
	if err != nil {
		t.Fatalf("Failed to unmarshal double encoded string: %v", err)
	}

	if final.Message != "hello string" {
		t.Errorf("Expected 'hello string', got '%s'", final.Message)
	}
}
