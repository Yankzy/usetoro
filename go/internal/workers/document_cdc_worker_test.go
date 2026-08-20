package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
)

func TestDocumentCDCWorker_Subscriptions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	worker := &DocumentCDCWorker{
		logger: logger,
	}

	subs := worker.Subscriptions()
	assert.Len(t, subs, 1)
	assert.Equal(t, "ledger.toro_core.documents.>", subs[0].Subject)
}

func TestDocumentCDCWorker_Handle_IgnoresNonPending(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	worker := &DocumentCDCWorker{
		logger: logger,
	}

	cdcPayload := map[string]any{
		"table":  "toro_core.documents",
		"action": "INSERT",
		"data": map[string]any{
			"id":         uuid.New().String(),
			"ocr_status": "EMBEDDINGS_SUCCESS",
			"file_name":  "test.pdf",
		},
	}

	bytes, _ := json.Marshal(cdcPayload)

	msg := &nats.Msg{
		Data: bytes,
	}

	err := worker.Handle(context.Background(), msg)
	assert.NoError(t, err)
}

func TestDocumentCDCWorker_Handle_InvalidJSON(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	worker := &DocumentCDCWorker{
		logger: logger,
	}

	msg := &nats.Msg{
		Data: []byte("invalid json"),
	}

	err := worker.Handle(context.Background(), msg)
	assert.NoError(t, err)
}

func TestDocumentCDCWorker_Handle_NilStorage_ReturnsError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	worker := &DocumentCDCWorker{
		logger:  logger,
		storage: nil,
	}

	docID := uuid.New()
	cdcPayload := map[string]any{
		"table":  "toro_core.documents",
		"action": "INSERT",
		"data": map[string]any{
			"id":         docID.String(),
			"ocr_status": "PENDING",
			"file_name":  "invoice.pdf",
			"s3_url":     "attachments/invoice.pdf",
		},
	}
	bytes, _ := json.Marshal(cdcPayload)

	msg := &nats.Msg{
		Data: bytes,
	}

	err := worker.Handle(context.Background(), msg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "S3 storage service is nil")
}
