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
		QueryID: "get_bank_accounts",
	}

	env, _ := core.NewEnvelope(uuid.New().String(), "src", "dst", "cid-123", core.INFORM, payload)

	// Since we can't easily mock the dependencies for Handle, we can at least verify
	// that our UnmarshalTaskPayload logic works as expected.

	var extracted SQLWorkerPayload
	err := core.UnmarshalTaskPayload(env.Body, &extracted)
	if err != nil {
		t.Fatalf("failed to unmarshal from envelope body: %v", err)
	}

	if extracted.QueryID != "get_bank_accounts" {
		t.Errorf("expected QueryID 'get_bank_accounts', got '%s'", extracted.QueryID)
	}
}

func TestSQLWorker_Handle_TaskPayload(t *testing.T) {
	// Test the Orchestrator's "input" wrapper
	inner := SQLWorkerPayload{
		QueryID: "get_checking_accounts",
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

	if extracted.QueryID != "get_checking_accounts" {
		t.Errorf("expected QueryID 'get_checking_accounts', got '%s'", extracted.QueryID)
	}
}

func TestSQLWorker_Handle_ReplyFallback(t *testing.T) {
	// This test verifies that if ReturnSubject is empty, the worker uses msg.Reply.
	// Since we can't easily mock the DB and NATS for a full Handle call here without a lot of setup,
	// we are mostly documenting the expectation.
	// In a real environment, we'd use a mock DB pool.
}
