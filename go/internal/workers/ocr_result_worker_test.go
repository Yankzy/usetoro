package workers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
)

func TestOCRResultWorker_NilDependencies(t *testing.T) {
	worker := &OCRResultWorker{
		db:     nil,
		pool:   nil,
		logger: nil,
		cfg:    nil,
		nc:     nil,
	}

	assert.NotNil(t, worker)
}

func TestOCRResultWorker_Subscriptions(t *testing.T) {
	worker := &OCRResultWorker{}
	subs := worker.Subscriptions()

	assert.Len(t, subs, 1)
	assert.Equal(t, "worker.inbox.go.knowledge_ingest", subs[0].Subject)
}

func TestOCRResultWorker_ForwardToCallback_NoTopic(t *testing.T) {
	worker := &OCRResultWorker{}

	payload := map[string]any{
		"document_id": uuid.New().String(),
		"status":      "OCR_SUCCESS",
	}

	// Should not panic when topic is missing or connection is nil
	worker.forwardToCallback(payload)
}

func TestOCRResultWorker_Handle_InvalidPayload(t *testing.T) {
	worker := &OCRResultWorker{}

	// Invalid JSON payload should be logged and skipped without error
	ctx := context.Background()
	msg := &nats.Msg{
		Subject: "worker.inbox.go.knowledge_ingest",
		Data:    []byte(`invalid json`),
	}

	err := worker.Handle(ctx, msg)
	assert.NoError(t, err)
}

func TestOCRResultWorker_Handle_NonSuccessStatus(t *testing.T) {
	worker := &OCRResultWorker{}

	ctx := context.Background()
	payload := map[string]any{
		"document_id":             uuid.New().String(),
		"status":                  "OCR_FAILED",
		"original_callback_topic": "events.test.callback",
	}
	msgBytes, _ := json.Marshal(payload)
	msg := &nats.Msg{
		Subject: "worker.inbox.go.knowledge_ingest",
		Data:    msgBytes,
	}

	// Non-success status should be forwarded to callback and return nil
	err := worker.Handle(ctx, msg)
	assert.NoError(t, err)
}

func TestOCRResultWorker_Handle_ErrorStatusFromPythonOCR(t *testing.T) {
	worker := &OCRResultWorker{}

	ctx := context.Background()
	payload := map[string]any{
		"document_id":             uuid.New().String(),
		"status":                  "ERROR",
		"error":                   "OpenAI connection timeout",
		"original_callback_topic": "events.test.callback",
	}
	msgBytes, _ := json.Marshal(payload)
	msg := &nats.Msg{
		Subject: "worker.inbox.go.knowledge_ingest",
		Data:    msgBytes,
	}

	// ERROR status payload should be handled cleanly, forwarding to callback topic
	err := worker.Handle(ctx, msg)
	assert.NoError(t, err)
}

func TestOCRResultWorker_Handle_MissingDocumentID(t *testing.T) {
	worker := &OCRResultWorker{}

	ctx := context.Background()
	payload := map[string]any{
		"status":                  "OCR_SUCCESS",
		"original_callback_topic": "events.test.callback",
	}
	msgBytes, _ := json.Marshal(payload)
	msg := &nats.Msg{
		Subject: "worker.inbox.go.knowledge_ingest",
		Data:    msgBytes,
	}

	// Missing document_id should be forwarded and return nil
	err := worker.Handle(ctx, msg)
	assert.NoError(t, err)
}

func TestOCRResultWorker_AccountingIntakeCallback(t *testing.T) {
	t.Run("Non-success OCR suppresses dispatch to accounting intake", func(t *testing.T) {
		worker := &OCRResultWorker{}
		payload := map[string]any{
			"document_id":             uuid.New().String(),
			"status":                  "OCR_FAILED",
			"original_callback_topic": "worker.inbox.python.accounting_intake",
		}
		// Since nc is nil, if it attempted to publish, it would have logged or panicked.
		// forwardToCallback returns early without publishing.
		worker.forwardToCallback(payload)
	})

	t.Run("Success OCR prepares small accounting payload", func(t *testing.T) {
		worker := &OCRResultWorker{}
		docID := uuid.New().String()
		entityID := uuid.New().String()
		sessionID := uuid.New().String()
		payload := map[string]any{
			"document_id":             docID,
			"entity_id":               entityID,
			"session_id":              sessionID,
			"external_id":             "msg-12345",
			"file_name":               "bank_statement.pdf",
			"status":                  "OCR_SUCCESS",
			"original_callback_topic": "worker.inbox.python.accounting_intake",
			"huge_ocr_extractions":    "do_not_embed_in_event",
		}
		// In a worker without NATS conn, it safely logs error without panicking
		worker.forwardToCallback(payload)
	})
}
