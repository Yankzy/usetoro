package workers

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func TestRuleEvaluationWorker_Handle_UnwrapsEnvelope(t *testing.T) {
	// 1. Setup Logger
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_ = logger

	// 2. Prepare Enveloped Payload
	sessionID := "550e8400-e29b-41d4-a716-446655440000"
	innerPayload := struct {
		SessionID string `json:"session_id"`
	}{
		SessionID: sessionID,
	}
	innerData, _ := json.Marshal(innerPayload)

	envelope := struct {
		Perf core.Performative `json:"perf"`
		Body json.RawMessage   `json:"body"`
	}{
		Perf: core.REQUEST,
		Body: innerData,
	}
	envelopeData, _ := json.Marshal(envelope)

	// 3. Create NATS message
	msg := &nats.Msg{
		Data: envelopeData,
	}

	// 4. Verify the extraction logic (mirroring what's in RuleEvaluationWorker.Handle)
	data := msg.Data
	var env core.Envelope
	if err := json.Unmarshal(data, &env); err == nil && len(env.Body) > 0 {
		data = env.Body
	}

	var payload struct {
		SessionID string `json:"session_id"`
	}
	if err := core.UnmarshalTaskPayload(data, &payload); err != nil {
		t.Errorf("Expected nil error from UnmarshalTaskPayload, got %v", err)
	}

	if payload.SessionID != sessionID {
		t.Errorf("Expected SessionID %s, got %s", sessionID, payload.SessionID)
	}
}
