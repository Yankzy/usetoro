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
		name           string
		step           WorkflowStep
		fallback       []byte
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear LastProof for the fallback test case
			testState := state
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
				t.Errorf("buildStepPayload() got input = %v, want %v", gotInput, wantNormalized)
			}
		})
	}
}
