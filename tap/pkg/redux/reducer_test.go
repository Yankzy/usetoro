package redux

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestApplyPatchReducer(t *testing.T) {
	tests := []struct {
		name       string
		state      map[string]interface{}
		patchArray []json.RawMessage
		want       map[string]interface{}
		wantErr    bool
	}{
		{
			name:       "empty patch array",
			state:      map[string]interface{}{"status": "OPEN"},
			patchArray: []json.RawMessage{},
			want:       map[string]interface{}{"status": "OPEN"},
			wantErr:    false,
		},
		{
			name:  "add field",
			state: map[string]interface{}{"status": "OPEN"},
			patchArray: []json.RawMessage{
				[]byte(`{"op": "add", "path": "/new_field", "value": "added"}`),
			},
			want:    map[string]interface{}{"status": "OPEN", "new_field": "added"},
			wantErr: false,
		},
		{
			name:  "replace field",
			state: map[string]interface{}{"status": "OPEN"},
			patchArray: []json.RawMessage{
				[]byte(`{"op": "replace", "path": "/status", "value": "CLOSED"}`),
			},
			want:    map[string]interface{}{"status": "CLOSED"},
			wantErr: false,
		},
		{
			name:  "remove field",
			state: map[string]interface{}{"status": "OPEN", "to_delete": "gone"},
			patchArray: []json.RawMessage{
				[]byte(`{"op": "remove", "path": "/to_delete"}`),
			},
			want:    map[string]interface{}{"status": "OPEN"},
			wantErr: false,
		},
		{
			name:  "invalid patch operation",
			state: map[string]interface{}{"status": "OPEN"},
			patchArray: []json.RawMessage{
				[]byte(`{"op": "non_existent_op", "path": "/status"}`),
			},
			want:    nil,
			wantErr: true,
		},
		{
			name:  "malformed patch json",
			state: map[string]interface{}{"status": "OPEN"},
			patchArray: []json.RawMessage{
				[]byte(`{"op": "add", "path": "/broken", "value":`), // syntax error
			},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ApplyPatchReducer(tt.state, tt.patchArray)
			if (err != nil) != tt.wantErr {
				t.Errorf("ApplyPatchReducer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ApplyPatchReducer() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyPatchReducer_PointerIntegrity(t *testing.T) {
	state := map[string]interface{}{"status": "OPEN"}
	patchArray := []json.RawMessage{
		[]byte(`{"op": "replace", "path": "/status", "value": "CLOSED"}`),
	}

	nextState, err := ApplyPatchReducer(state, patchArray)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// modifying nextState should not affect state
	nextState["status"] = "HACKED"

	if state["status"] != "OPEN" {
		t.Errorf("Pointer drift detected. Original state mutated to: %v", state["status"])
	}
}
