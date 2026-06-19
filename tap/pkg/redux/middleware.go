// Package redux provides strict JSON matrix reduction tools.
package redux

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// EnforcePayloadBoundariesMiddleware intercepts the pipeline to halt exhaustive structural memory leaks.
// It strictly guarantees patch bytes map accurately beneath API constraints, preventing 50MB Base64 LLM strings 
// from triggering Go Kernel container OOM panics blindly inherently.
func EnforcePayloadBoundariesMiddleware(patchArray []json.RawMessage, req EngineConfig) error {
	if len(patchArray) > req.MaxOperations {
		req.Metrics.RecordRuleViolation("MaxOperations")
		return ErrMaxOperationsExceeded
	}

	totalBytes := 0
	for _, opBytes := range patchArray {
		totalBytes += len(opBytes)
		var op struct {
			Op   string `json:"op"`
			Path string `json:"path"`
		}
		if err := json.Unmarshal(opBytes, &op); err != nil {
			return ErrInvalidPatchStruct
		}

		// Security Boundary: Root Structure Replacements
		// Since _sys was mathematically discarded from the Engine's awareness, evaluating root modifications prevents doomsday events.
		if (op.Op == "replace" || op.Op == "remove") && (op.Path == "/" || op.Path == "") {
			req.Metrics.RecordRuleViolation("RootReplace")
			return ErrRootReplace
		}
	}

	if totalBytes > req.MaxPayloadBytes {
		req.Metrics.RecordRuleViolation("MaxPayloadBytes")
		return ErrPayloadTooLarge
	}

	return nil
}

// EnforceRBACMiddleware securely isolates path mutability logic determining strict LLM versus API logic authorities.
func EnforceRBACMiddleware(patchArray []json.RawMessage, actor string, policy RBACPolicy, metrics MetricsRecorder) error {
	if len(policy.AllowedPrefixes) == 0 {
		return nil 
	}

	allowedPaths, exists := policy.AllowedPrefixes[actor]
	if !exists {
		metrics.RecordRuleViolation("RBAC_UnknownActor")
		return fmt.Errorf("%w: actor '%s' completely unmapped", ErrRBACViolation, actor)
	}

	for _, opBytes := range patchArray {
		var op struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(opBytes, &op) 

		authorized := false
		for _, prefix := range allowedPaths {
			if strings.HasPrefix(op.Path, prefix) || op.Path == prefix {
				authorized = true
				break
			}
		}

		if !authorized {
			metrics.RecordRuleViolation("RBAC_UnauthorizedPath")
			return fmt.Errorf("%w: actor '%s' denied access to %s", ErrRBACViolation, actor, op.Path)
		}
	}
	return nil
}

// EnforceNoArrayMiddleware acts as a structural defense mechanism against JSON Array Shift bugs.
func EnforceNoArrayMiddleware(data map[string]interface{}, metrics MetricsRecorder) error {
	isSystemWrapper := false
	if _, ok := data["instance_path"]; ok {
		if _, ok2 := data["workflow_def"]; ok2 {
			isSystemWrapper = true
		}
	}

	if isSystemWrapper {
		systemKeys := map[string]bool{
			"workflow_def":      true,
			"current_step_id":    true,
			"instance_path":     true,
			"active_steps":      true,
			"completed_steps":   true,
			"variables":         true,
			"last_proof":        true,
			"parent_step_id":     true,
			"suspended":         true,
			"suspension_step":   true,
			"suspension_reason": true,
			"suspension_route":  true,
			"suspension_kind":   true,
		}
		for k, v := range data {
			if systemKeys[k] {
				continue
			}
			if hasArrayRuleViolation(v) {
				metrics.RecordRuleViolation("ArrayBan")
				return ErrArrayBan
			}
		}
		return nil
	}

	if hasArrayRuleViolation(data) {
		metrics.RecordRuleViolation("ArrayBan")
		return ErrArrayBan
	}
	return nil
}

// EnforceSchemaMiddleware traps business payload schema drift automatically preventing UI breakages.
func EnforceSchemaMiddleware(dataMap map[string]interface{}, schema *jsonschema.Schema, metrics MetricsRecorder) error {
	if schema == nil {
		return nil
	}
	if err := schema.Validate(dataMap); err != nil {
		metrics.RecordRuleViolation("SchemaDrift")
		return fmt.Errorf("%w: %v", ErrSchemaValidation, err)
	}
	return nil
}

func hasArrayRuleViolation(v interface{}) bool {
	switch val := v.(type) {
	case []interface{}:
		return true 
	case map[string]interface{}:
		for _, childV := range val {
			if hasArrayRuleViolation(childV) {
				return true
			}
		}
	}
	return false
}
