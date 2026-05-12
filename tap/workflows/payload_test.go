package workflows

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func TestBuildStepPayload(t *testing.T) {
	state := InstanceState{
		Variables: map[string]json.RawMessage{
			"step1": json.RawMessage(`{"res": "out1"}`),
			"step2": json.RawMessage(`{"res": "out2"}`),
		},
		LastProof: json.RawMessage(`{"res": "last"}`),
	}

	tests := []struct {
		name            string
		step            WorkflowStep
		fallback        []byte
		wantInsideInput map[string]interface{}
	}{
		{
			name: "Direct input (no history, no dependencies)",
			step: WorkflowStep{
				ID: "step3",
			},
			wantInsideInput: map[string]interface{}{"res": "last"},
		},
		{
			name: "History enabled (multi dependencies)",
			step: WorkflowStep{
				ID:             "step3",
				IncludeHistory: true,
				DependsOn:      []string{"step1", "step2"},
			},
			wantInsideInput: map[string]interface{}{
				"dependencies": map[string]interface{}{
					"step1": map[string]interface{}{"res": "out1"},
					"step2": map[string]interface{}{"res": "out2"},
				},
			},
		},
		{
			name: "History disabled (multi dependencies -> fallback to LastProof)",
			step: WorkflowStep{
				ID:             "step3",
				IncludeHistory: false,
				DependsOn:      []string{"step1", "step2"},
			},
			wantInsideInput: map[string]interface{}{"res": "last"},
		},
		{
			name: "Fallback triggered (no LastProof)",
			step: WorkflowStep{
				ID: "step3",
			},
			fallback:        []byte(`{"res": "fallback"}`),
			wantInsideInput: map[string]interface{}{"res": "fallback"},
		},
		{
			name: "Automatic Shaping (resolve missing field from TRIGGER)",
			step: WorkflowStep{
				ID:             "step3",
				WorkflowSchema: `{"type": "object", "properties": {"realm_id": {"type": "string"}}}`,
			},
			fallback:        []byte(`{"status": "ok"}`),
			wantInsideInput: map[string]interface{}{"realm_id": "trigger_realm"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test state
			testState := state
			testState.Variables = make(map[string]json.RawMessage)
			for k, v := range state.Variables {
				testState.Variables[k] = v
			}

			if tt.name == "Automatic Shaping (resolve missing field from TRIGGER)" {
				testState.Variables["TRIGGER"] = json.RawMessage(`{"realm_id": "trigger_realm"}`)
			}
			if tt.name == "Fallback triggered (no LastProof)" {
				testState.LastProof = nil
			}

			res := buildStepPayload(tt.step, testState, tt.fallback)

			// Unmarshal the outer Proof envelop
			var proof core.Proof
			if err := json.Unmarshal(res, &proof); err != nil {
				t.Fatalf("failed to unmarshal proof: %v", err)
			}

			// Since buildStepPayload calls wrapPayloadWithConfig, the payload is wrapped in {"input": ...}
			var wrapped map[string]json.RawMessage
			if err := json.Unmarshal(proof.Data, &wrapped); err != nil {
				t.Fatalf("failed to unmarshal wrapped data: %v", err)
			}

			var gotInput interface{}
			if err := json.Unmarshal(wrapped["input"], &gotInput); err != nil {
				t.Fatalf("failed to unmarshal input: %v", err)
			}

			// Special case for empty input if it happens
			if tt.wantInsideInput == nil && gotInput == nil {
				return
			}

			// Convert tt.wantInsideInput to interface{} for comparison
			wantBytes, _ := json.Marshal(tt.wantInsideInput)
			var wantNormalized interface{}
			json.Unmarshal(wantBytes, &wantNormalized)

			if !reflect.DeepEqual(gotInput, wantNormalized) {
				t.Errorf("%s: buildStepPayload() got input = %v, want %v", tt.name, gotInput, wantNormalized)
			}
		})
	}
}
func TestReshapePayloadToSchema(t *testing.T) {
	schema := `{"type": "object", "properties": {"realm_id": {"type": "string"}, "session_id": {"type": "string"}, "extra": {"type": "string"}}}`
	state := InstanceState{
		Variables: map[string]json.RawMessage{
			"TRIGGER": json.RawMessage(`{"realm_id": "trigger_realm", "session_id": "trigger_session"}`),
			"step1":   json.RawMessage(`{"extra": "step1_extra", "session_id": "step1_session"}`),
		},
	}

	tests := []struct {
		name    string
		payload []byte
		want    map[string]interface{}
	}{
		{
			name:    "Pulls missing fields from TRIGGER (priority)",
			payload: []byte(`{"session_id": "current_session"}`),
			want: map[string]interface{}{
				"realm_id":   "trigger_realm",   // Pulled from TRIGGER
				"session_id": "current_session", // Kept from current payload
				"extra":      "step1_extra",     // Pulled from step1
			},
		},
		{
			name:    "Exact shaping (removes fields not in schema & resolves others)",
			payload: []byte(`{"realm_id": "exists", "garbage": "should_be_removed"}`),
			want: map[string]interface{}{
				"realm_id":   "exists",
				"session_id": "trigger_session", // Auto-resolved from TRIGGER
				"extra":      "step1_extra",     // Auto-resolved from step1
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reshapePayloadToSchema(schema, tt.payload, state)
			var gotMap map[string]interface{}
			json.Unmarshal(got, &gotMap)

			wantBytes, _ := json.Marshal(tt.want)
			var wantMap map[string]interface{}
			json.Unmarshal(wantBytes, &wantMap)

			if !reflect.DeepEqual(gotMap, wantMap) {
				t.Errorf("%s: got %v, want %v", tt.name, gotMap, wantMap)
			}
		})
	}
}
