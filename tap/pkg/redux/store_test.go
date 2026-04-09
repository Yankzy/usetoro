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
