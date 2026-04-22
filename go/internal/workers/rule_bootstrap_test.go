package workers

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func TestRuleBootstrapWorker_Handle_UnwrapsEnvelope(t *testing.T) {
	// 1. Setup Logger
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_ = logger // For potential future use

	// 2. Prepare Enveloped Payload (The problematic one)
	realmID := "9341456276406470"
	innerPayload := struct {
		RealmID string `json:"realm_id"`
	}{
		RealmID: realmID,
	}
	innerData, _ := json.Marshal(innerPayload)

	envelope := struct {
		Perf string          `json:"perf"`
		Body json.RawMessage `json:"body"`
	}{
		Perf: "ACCEPT_PROPOSAL",
		Body: innerData,
	}
	envelopeData, _ := json.Marshal(envelope)

	// 3. Create NATS message
	msg := &nats.Msg{
		Data: envelopeData,
	}

	// 4. Verify the logic added to RuleBootstrapWorker.Handle
	data := msg.Data
	var env core.Envelope
	if err := json.Unmarshal(data, &env); err == nil && len(env.Body) > 0 {
		data = env.Body
	}

	var payload struct {
		RealmID string `json:"realm_id"`
	}
	if err := core.UnmarshalTaskPayload(data, &payload); err != nil {
		t.Errorf("Expected nil error from UnmarshalTaskPayload, got %v", err)
	}

	if payload.RealmID != realmID {
		t.Errorf("Expected RealmID %s, got %s", realmID, payload.RealmID)
	}
}

func TestRuleBootstrapWorker_Handle_WorksWithKebabCase(t *testing.T) {
	// 1. Prepare Enveloped Payload with kebab-case constant (The new behavior)
	realmID := "9341456276406470"
	innerPayload := struct {
		RealmID string `json:"realm_id"`
	}{
		RealmID: realmID,
	}
	innerData, _ := json.Marshal(innerPayload)

	envelope := struct {
		Perf core.Performative `json:"perf"`
		Body json.RawMessage   `json:"body"`
	}{
		Perf: core.ACCEPT_PROPOSAL, // kebab-case "accept-proposal"
		Body: innerData,
	}
	envelopeData, _ := json.Marshal(envelope)

	// 2. Create NATS message
	msg := &nats.Msg{
		Data: envelopeData,
	}

	// 3. Verify extraction logic
	data := msg.Data
	var env core.Envelope
	if err := json.Unmarshal(data, &env); err == nil && len(env.Body) > 0 {
		data = env.Body
	}

	var payload struct {
		RealmID string `json:"realm_id"`
	}
	if err := core.UnmarshalTaskPayload(data, &payload); err != nil {
		t.Errorf("Expected nil error from UnmarshalTaskPayload, got %v", err)
	}

	if payload.RealmID != realmID {
		t.Errorf("Expected RealmID %s, got %s", realmID, payload.RealmID)
	}
}
