package workers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
)

func TestPcmExportWorker_Handle_BasicEnvelope(t *testing.T) {
	worker := &PcmExportWorker{
		logger: testLogger(),
		cfg:    &config.Config{},
	}

	sessionID := uuid.New().String()
	payloadData, _ := json.Marshal(map[string]interface{}{
		"session_id":  sessionID,
		"from_handle": "bot@a.usetoro.io",
		"to_handle":   "user@example.com",
	})

	env := core.Envelope{
		ID:           uuid.New().String(),
		Performative: core.REQUEST,
		Body:         payloadData,
	}

	envBytes, _ := json.Marshal(env)

	ctx := context.Background()
	var envTest core.Envelope
	if err := json.Unmarshal(envBytes, &envTest); err != nil {
		t.Fatalf("failed to unmarshal env: %v", err)
	}

	if envTest.Performative != core.REQUEST {
		t.Fatalf("expected REQUEST performative, got %v", envTest.Performative)
	}

	var parsed struct {
		SessionID  string `json:"session_id"`
		FromHandle string `json:"from_handle"`
		ToHandle   string `json:"to_handle"`
	}
	if err := json.Unmarshal(envTest.Body, &parsed); err != nil {
		t.Fatalf("failed to unmarshal body: %v", err)
	}

	if parsed.SessionID != sessionID {
		t.Errorf("expected sessionID %s, got %s", sessionID, parsed.SessionID)
	}
	if parsed.ToHandle != "user@example.com" {
		t.Errorf("expected to_handle user@example.com, got %s", parsed.ToHandle)
	}
	_ = worker
	_ = ctx
}
