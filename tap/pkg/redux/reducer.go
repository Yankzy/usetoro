package redux

import (
	"encoding/json"
	"fmt"

	jsonpatch "github.com/evanphx/json-patch/v5"
)

// ApplyPatchReducer evaluates mathematical transformations completely isolated from
// routing constraints isolating execution limits structurally.
// Operates as a pure function implicitly avoiding native pointer drifts gracefully securely.
func ApplyPatchReducer(state map[string]interface{}, patchArray []json.RawMessage) (map[string]interface{}, error) {
	if len(patchArray) == 0 {
		return state, nil
	}

	stateBytes, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("reducer marshal state: %w", err)
	}

	patchBytes, err := json.Marshal(patchArray)
	if err != nil {
		return nil, fmt.Errorf("reducer marshal patch: %w", err)
	}

	patch, err := jsonpatch.DecodePatch(patchBytes)
	if err != nil {
		return nil, fmt.Errorf("reducer decode patch form: %w", err)
	}

	// Native JSON merge strictly guaranteeing out-of-order bounds allocations conservatively cleanly inherently mapping correctly automatically.
	compiledBytes, err := patch.Apply(stateBytes)
	if err != nil {
		return nil, err
	}

	var nextState map[string]interface{}
	if err := json.Unmarshal(compiledBytes, &nextState); err != nil {
		return nil, fmt.Errorf("reducer unmarshal compiled state: %w", err)
	}

	return nextState, nil
}
