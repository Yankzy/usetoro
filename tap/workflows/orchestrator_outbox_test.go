package workflows

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// A worker can reply as soon as its request is published. Scheduling must
// therefore mark the step active before the outbox relay ever sees the command.
func TestScheduleReadyStepsActivatesBeforeOutboxDelivery(t *testing.T) {
	o := NewOrchestrator(newTestLogger(), &stubBus{}, nil, nil, nil, nil)
	state := InstanceState{
		InstancePath:   []string{"f0a749c4-ca65-49c1-a4e8-060d2f4a0011"},
		ActiveSteps:    map[string]bool{},
		CompletedSteps: map[string]bool{},
		Variables:      map[string]json.RawMessage{},
	}
	def := WorkflowDef{Steps: []WorkflowStep{{
		ID: "document_readiness", ActivityType: "workers.document_readiness",
	}}}

	ready, dispatches, err := o.scheduleReadySteps(context.Background(), def, &state, pgtype.UUID{Valid: true}, []byte(`{"entity_id":"00000000-0000-0000-0000-000000000001"}`))
	if err != nil {
		t.Fatalf("schedule ready steps: %v", err)
	}
	if len(ready) != 1 || len(dispatches) != 1 {
		t.Fatalf("got %d ready steps and %d dispatches, want one of each", len(ready), len(dispatches))
	}
	if !state.ActiveSteps["document_readiness"] {
		t.Fatal("step must be active before its outbox command is delivered")
	}
	if dispatches[0].ConversationID != state.InstancePath[0]+"/document_readiness" {
		t.Fatalf("unexpected conversation ID %q", dispatches[0].ConversationID)
	}
}
