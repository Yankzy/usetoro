// Package redux core structure contracts defining deterministic Node borders statically.
package redux

import (
	"encoding/json"
	"time"
)

// RFC6902Event is the standard NATS Event Sequence Wrapper delivering dynamic LLM instructions.
// The embedded PatchArray explicitly avoids standard unmarshal mapping targets internally
// to prevent Go from silently filtering 'omitempty' nulls matching strictly with specification boundaries.
// The SequenceID enforces strict monotonic idempotent event replays logically.
type RFC6902Event struct {
	EventID    string            `json:"event_id"`    
	SequenceID uint64            `json:"sequence_id"` 
	Timestamp  time.Time         `json:"timestamp"`   
	Type       string            `json:"type"`        
	Actor      string            `json:"actor"`       
	PatchArray []json.RawMessage `json:"patch_array"` 
}

// DomainFault encapsulates a structural validation, RBAC rejection, or mapping error efficiently.
// It is explicitly returned ephemerally in-memory, empowering the Host Orchestrator to suspend infinite LLM loops
// through redis circuit breakers without irrevocably polluting persistent databases.
type DomainFault struct {
	EventID string `json:"event_id"`
	Error   string `json:"error"`
}
