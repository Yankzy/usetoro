package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func TestSQLWorker_Handle_Malformed(t *testing.T) {
	w := &SQLWorker{
		logger: slog.Default(),
	}

	msg := &nats.Msg{
		Subject: "worker.inbox.sql.execute",
		Data:    []byte(`{ "invalid": "json"`),
	}

	err := w.Handle(context.Background(), msg)
	if err != nil {
		t.Errorf("expected nil error for malformed payload (non-retryable), got %v", err)
	}
}

func TestSQLWorker_Handle_Envelope(t *testing.T) {
	// We can't easily test the full Handle without mocking pgxpool and nats.Conn
	// but we can test if it correctly extracts the payload from an envelope.

	payload := SQLWorkerPayload{
		Query: "SELECT 1",
	}

	env, _ := core.NewEnvelope(uuid.New().String(), "src", "dst", "cid-123", core.INFORM, payload)

	// Since we can't easily mock the dependencies for Handle, we can at least verify
	// that our UnmarshalTaskPayload logic works as expected.

	var extracted SQLWorkerPayload
	err := core.UnmarshalTaskPayload(env.Body, &extracted)
	if err != nil {
		t.Fatalf("failed to unmarshal from envelope body: %v", err)
	}

	if extracted.Query != "SELECT 1" {
		t.Errorf("expected Query 'SELECT 1', got '%s'", extracted.Query)
	}
}

func TestSQLWorker_Handle_TaskPayload(t *testing.T) {
	// Test the Orchestrator's "input" wrapper
	inner := SQLWorkerPayload{
		Query: "SELECT 2",
	}
	innerBytes, _ := json.Marshal(inner)

	wrapper := struct {
		Input json.RawMessage `json:"input"`
	}{
		Input: innerBytes,
	}
	wrapperBytes, _ := json.Marshal(wrapper)

	var extracted SQLWorkerPayload
	err := core.UnmarshalTaskPayload(wrapperBytes, &extracted)
	if err != nil {
		t.Fatalf("failed to unmarshal from wrapped payload: %v", err)
	}

	if extracted.Query != "SELECT 2" {
		t.Errorf("expected Query 'SELECT 2', got '%s'", extracted.Query)
	}
}
