package redux

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

func TestEnforcePayloadBoundariesMiddleware(t *testing.T) {
	cfg := DefaultConfig()

	t.Run("MaxPayloadBytes Exceeded", func(t *testing.T) {
		cfg.MaxPayloadBytes = 50
		patchArray := []json.RawMessage{
			[]byte(`{"op": "add", "path": "/heavy_payload_string", "value": "1234567890_1234567890_1234567890_1234567890"}`),
		}
		err := EnforcePayloadBoundariesMiddleware(patchArray, cfg)
		if err == nil || !strings.Contains(err.Error(), "payload exceeds maximum") {
			t.Fatalf("OOM size limit bypassed: %v", err)
		}
	})

	t.Run("Root Structure Replacement limit", func(t *testing.T) {
		cfg.MaxPayloadBytes = 64 * 1024
		patchArray := []json.RawMessage{
			[]byte(`{"op": "replace", "path": "/", "value": {}}`),
		}
		err := EnforcePayloadBoundariesMiddleware(patchArray, cfg)
		if err == nil || !strings.Contains(err.Error(), "root document") {
			t.Fatalf("root replace was allowed: %v", err)
		}
	})

	t.Run("MaxOperations Exceeded", func(t *testing.T) {
		cfg.MaxOperations = 1
		patchArray := []json.RawMessage{
			[]byte(`{"op": "add", "path": "/a", "value": "1"}`),
			[]byte(`{"op": "add", "path": "/b", "value": "2"}`),
		}
		err := EnforcePayloadBoundariesMiddleware(patchArray, cfg)
		if err == nil || !strings.Contains(err.Error(), "operations limit") {
			t.Fatalf("max operations limit was bypassed: %v", err)
		}
	})
	
	t.Run("Valid Payload", func(t *testing.T) {
		cfg.MaxOperations = 10
		cfg.MaxPayloadBytes = 64 * 1024
		patchArray := []json.RawMessage{
			[]byte(`{"op": "add", "path": "/a", "value": "1"}`),
		}
		err := EnforcePayloadBoundariesMiddleware(patchArray, cfg)
		if err != nil {
			t.Fatalf("valid payload incorrectly denied: %v", err)
		}
	})
}

func TestEnforceRBACMiddleware(t *testing.T) {
	metrics := noopMetrics{}
	policy := RBACPolicy{
		AllowedPrefixes: map[string][]string{
			"AI_AGENT": {"/receipts/", "/status"},
		},
	}

	t.Run("Allowed Prefix", func(t *testing.T) {
		patchArray := []json.RawMessage{
			[]byte(`{"op": "replace", "path": "/status", "value": "CLOSED"}`),
		}
		err := EnforceRBACMiddleware(patchArray, "AI_AGENT", policy, metrics)
		if err != nil {
			t.Fatalf("allowed RBAC path rejected: %v", err)
		}
	})

	t.Run("Disallowed Prefix", func(t *testing.T) {
		patchArray := []json.RawMessage{
			[]byte(`{"op": "replace", "path": "/admin_notes", "value": "hacked"}`),
		}
		err := EnforceRBACMiddleware(patchArray, "AI_AGENT", policy, metrics)
		if err == nil {
			t.Fatalf("expected RBAC failure to deny access")
		}
		if !strings.Contains(err.Error(), "denied access to /admin_notes") {
			t.Fatalf("expected specific RBAC denial message, got: %v", err)
		}
	})

	t.Run("Unknown Actor", func(t *testing.T) {
		patchArray := []json.RawMessage{
			[]byte(`{"op": "replace", "path": "/status", "value": "CLOSED"}`),
		}
		err := EnforceRBACMiddleware(patchArray, "UNKNOWN", policy, metrics)
		if err == nil {
			t.Fatalf("expected RBAC failure for unknown actor")
		}
	})

	t.Run("Empty Policy", func(t *testing.T) {
		emptyPolicy := RBACPolicy{}
		patchArray := []json.RawMessage{
			[]byte(`{"op": "replace", "path": "/status", "value": "CLOSED"}`),
		}
		err := EnforceRBACMiddleware(patchArray, "AI_AGENT", emptyPolicy, metrics)
		if err != nil {
			t.Fatalf("empty policy should allow everything: %v", err)
		}
	})
}

func TestEnforceNoArrayMiddleware(t *testing.T) {
	metrics := noopMetrics{}

	t.Run("No Array", func(t *testing.T) {
		state := map[string]interface{}{
			"status": "OPEN",
			"details": map[string]interface{}{
				"field": "value",
			},
		}
		err := EnforceNoArrayMiddleware(state, metrics)
		if err != nil {
			t.Fatalf("valid state incorrectly denied: %v", err)
		}
	})

	t.Run("Array Found", func(t *testing.T) {
		state := map[string]interface{}{
			"blockers": []interface{}{"bad"},
		}
		err := EnforceNoArrayMiddleware(state, metrics)
		if err == nil || !strings.Contains(err.Error(), "no-array") {
			t.Fatalf("array was allowed: %v", err)
		}
	})

	t.Run("Nested Array Found", func(t *testing.T) {
		state := map[string]interface{}{
			"nested": map[string]interface{}{
				"blockers": []interface{}{"deep_bad"},
			},
		}
		err := EnforceNoArrayMiddleware(state, metrics)
		if err == nil || !strings.Contains(err.Error(), "no-array") {
			t.Fatalf("nested array was allowed: %v", err)
		}
	})

	t.Run("System Wrapper Allowed with Arrays in System Fields", func(t *testing.T) {
		state := map[string]interface{}{
			"workflow_def":  "test_wf",
			"instance_path": []interface{}{"uuid1"},
			"variables": map[string]interface{}{
				"TRIGGER": map[string]interface{}{
					"rows": []interface{}{"row1"},
				},
			},
			"status": "ENRICHED",
		}
		err := EnforceNoArrayMiddleware(state, metrics)
		if err != nil {
			t.Fatalf("system wrapper incorrectly denied: %v", err)
		}
	})

	t.Run("System Wrapper Denied with Array in Domain Field", func(t *testing.T) {
		state := map[string]interface{}{
			"workflow_def":  "test_wf",
			"instance_path": []interface{}{"uuid1"},
			"status":        "ENRICHED",
			"enrichments": map[string]interface{}{
				"bad_domain_array": []interface{}{"item1"},
			},
		}
		err := EnforceNoArrayMiddleware(state, metrics)
		if err == nil || !strings.Contains(err.Error(), "no-array") {
			t.Fatalf("system wrapper with domain array was allowed")
		}
	})
}

func TestEnforceSchemaMiddleware(t *testing.T) {
	metrics := noopMetrics{}
	
	schemaString := `{
		"type": "object",
		"properties": {
			"status": {"type": "string"}
		},
		"additionalProperties": false
	}`
	
	compiler := jsonschema.NewCompiler()
	compiler.AddResource("schema.json", strings.NewReader(schemaString))
	schema, _ := compiler.Compile("schema.json")

	t.Run("Valid Schema", func(t *testing.T) {
		state := map[string]interface{}{
			"status": "OPEN",
		}
		err := EnforceSchemaMiddleware(state, schema, metrics)
		if err != nil {
			t.Fatalf("valid schema rejected: %v", err)
		}
	})

	t.Run("Invalid Schema", func(t *testing.T) {
		state := map[string]interface{}{
			"rogue_field": "123",
		}
		err := EnforceSchemaMiddleware(state, schema, metrics)
		if err == nil || !strings.Contains(err.Error(), "schema validation failed") {
			t.Fatalf("schema drift allowed: %v", err)
		}
	})

	t.Run("Nil Schema", func(t *testing.T) {
		state := map[string]interface{}{
			"rogue_field": "123",
		}
		err := EnforceSchemaMiddleware(state, nil, metrics)
		if err != nil {
			t.Fatalf("nil schema incorrectly returned error: %v", err)
		}
	})
}
