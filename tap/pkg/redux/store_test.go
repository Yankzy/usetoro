package redux

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCoreReduxLoop(t *testing.T) {
	store, err := NewStore(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	base := []byte(`{}`)
	events := []RFC6902Event{
		{
			EventID: "evt_1",
			SequenceID: 1,
			PatchArray: []json.RawMessage{
				[]byte(`{"op": "add", "path": "/status", "value": "AWAITING_RECEIPT"}`),
			},
		},
	}
	
	out, nextSeq, faults, err := store.Reduce(context.Background(), base, 1, events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nextSeq != 2 {
		t.Fatalf("expected next sequence to be 2, got %d", nextSeq)
	}
	if len(faults) > 0 {
		t.Fatalf("unexpected faults: %v", faults)
	}
	if !strings.Contains(string(out), `"status":"AWAITING_RECEIPT"`) {
		t.Fatalf("state not updated properly: %s", string(out))
	}
}

func TestEphemeralFaultDegradation(t *testing.T) {
	store, err := NewStore(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	base := []byte(`{"status":"OPEN"}`)
	events := []RFC6902Event{
		{
			EventID: "evt_invalid",
			SequenceID: 1,
			PatchArray: []json.RawMessage{
				[]byte(`{"op": "remove", "path": "/non_existent"}`),
			},
		},
	}
	
	out, nextSeq, faults, err := store.Reduce(context.Background(), base, 1, events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nextSeq != 1 {
		t.Fatalf("sequence improperly incremented on fault")
	}
	if len(faults) != 1 {
		t.Fatalf("expected 1 fault, got %d", len(faults))
	}
	if strings.Contains(string(out), `system_errors`) {
		t.Fatalf("system_errors polluted the persistent state: %s", string(out))
	}
	if !strings.Contains(string(out), `"status":"OPEN"`) {
		t.Fatalf("valid state was wiped out: %s", string(out))
	}
}

func TestOptimisticConcurrency(t *testing.T) {
	store, err := NewStore(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	base := []byte(`{"status":"APPROVED"}`)
	events := []RFC6902Event{
		{
			EventID: "evt_ai_late",
			PatchArray: []json.RawMessage{
				[]byte(`{"op": "test", "path": "/status", "value": "AWAITING_RECEIPT"}`),
				[]byte(`{"op": "replace", "path": "/status", "value": "PROCESSING"}`),
			},
		},
	}
	
	out, _, faults, err := store.Reduce(context.Background(), base, 0, events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(faults) != 1 {
		t.Fatalf("expected optimistic lock failure fault")
	}
	if strings.Contains(string(out), `"status":"PROCESSING"`) {
		t.Fatalf("optimistic lock failed, state was overwritten: %s", string(out))
	}
	if !strings.Contains(string(out), `"status":"APPROVED"`) {
		t.Fatalf("base state was lost: %s", string(out))
	}
}

func TestPayloadByteLimits(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxPayloadBytes = 50 // Incredibly tiny OOM restriction map logically.
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}

	base := []byte(`{}`)
	events := []RFC6902Event{
		{
			EventID: "evt_heavy",
			PatchArray: []json.RawMessage{
				[]byte(`{"op": "add", "path": "/heavy_payload_string", "value": "1234567890_1234567890_1234567890_1234567890"}`),
			},
		},
	}
	
	_, _, faults, err := store.Reduce(context.Background(), base, 0, events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(faults) != 1 || !strings.Contains(faults[0].Error, "payload exceeds maximum") {
		t.Fatalf("OOM size limit bypassed natively: %v", faults)
	}
}

func TestRootReplaceLimit(t *testing.T) {
	store, err := NewStore(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	base := []byte(`{}`)
	events := []RFC6902Event{
		{
			EventID: "evt_root_replace",
			PatchArray: []json.RawMessage{
				[]byte(`{"op": "replace", "path": "/", "value": {}}`),
			},
		},
	}
	
	_, _, faults, err := store.Reduce(context.Background(), base, 0, events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(faults) != 1 || !strings.Contains(faults[0].Error, "root document") {
		t.Fatalf("root replace was allowed: %v", faults)
	}
}

func TestArrayBan(t *testing.T) {
	store, err := NewStore(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	base := []byte(`{}`)
	events := []RFC6902Event{
		{
			EventID: "evt_array",
			PatchArray: []json.RawMessage{
				[]byte(`{"op": "add", "path": "/blockers", "value": ["bad"]}`),
			},
		},
	}
	
	_, _, faults, err := store.Reduce(context.Background(), base, 0, events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(faults) != 1 || !strings.Contains(faults[0].Error, "no-array") {
		t.Fatalf("array was allowed: %v", faults)
	}
}

func TestSchemaViolation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SchemaString = `{
		"type": "object",
		"properties": {
			"status": {"type": "string"}
		},
		"additionalProperties": false
	}`
	
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}

	base := []byte(`{}`)
	events := []RFC6902Event{
		{
			EventID: "evt_schema",
			PatchArray: []json.RawMessage{
				[]byte(`{"op": "add", "path": "/rogue_field", "value": "123"}`),
			},
		},
	}
	
	_, _, faults, err := store.Reduce(context.Background(), base, 0, events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(faults) != 1 || !strings.Contains(faults[0].Error, "schema validation failed") {
		t.Fatalf("schema drift allowed: %v", faults)
	}
}

func TestRBACSecurity(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RBAC = RBACPolicy{
		AllowedPrefixes: map[string][]string{
			"AI_AGENT": {"/receipts/", "/status"},
		},
	}
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}

	base := []byte(`{"status":"OPEN", "admin_notes":"keep secret"}`)
	events := []RFC6902Event{
		{
			EventID: "evt_allowed",
			SequenceID: 1,
			Actor: "AI_AGENT",
			PatchArray: []json.RawMessage{
				[]byte(`{"op": "replace", "path": "/status", "value": "CLOSED"}`),
			},
		},
		{
			EventID: "evt_denied",
			SequenceID: 2,
			Actor: "AI_AGENT",
			PatchArray: []json.RawMessage{
				[]byte(`{"op": "replace", "path": "/admin_notes", "value": "hacked"}`),
			},
		},
	}
	
	out, _, faults, err := store.Reduce(context.Background(), base, 1, events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(faults) != 1 {
		t.Fatalf("expected exactly 1 fault, got %d. Faults: %v", len(faults), faults)
	}
	if !strings.Contains(faults[0].Error, "denied access to /admin_notes") {
		t.Fatalf("expected RBAC failure string, got: %s", faults[0].Error)
	}
	if strings.Contains(string(out), `"admin_notes":"hacked"`) {
		t.Fatalf("RBAC breached: %s", string(out))
	}
	if !strings.Contains(string(out), `"status":"CLOSED"`) {
		t.Fatalf("Allowed RBAC path failed to apply: %s", string(out))
	}
}

func TestSequenceMismatch(t *testing.T) {
	store, err := NewStore(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	base := []byte(`{"status":"OPEN"}`)
	events := []RFC6902Event{
		{
			EventID: "evt_out_of_sync",
			SequenceID: 5,
			PatchArray: []json.RawMessage{
				[]byte(`{"op": "replace", "path": "/status", "value": "CLOSED"}`),
			},
		},
	}
	
	out, nextSeq, faults, err := store.Reduce(context.Background(), base, 4, events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(faults) != 1 || !strings.Contains(faults[0].Error, "sequence drift") {
		t.Fatalf("Sequence logic bypassed naturally: %v", faults)
	}
	if nextSeq != 4 {
		t.Fatalf("sequence mutated invalidly: %d", nextSeq)
	}
	if !strings.Contains(string(out), `"status":"OPEN"`) {
		t.Fatalf("out of order patch applied: %s", string(out))
	}
}
