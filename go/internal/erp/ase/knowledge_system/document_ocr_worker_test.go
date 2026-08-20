package knowledge_system

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestDocumentOCRWorker_ProcessPendingDocument_NilValidation(t *testing.T) {
	worker := NewDocumentOCRWorker(nil, nil, nil, nil, nil, nil)

	extraction, err := worker.ProcessPendingDocument(context.Background(), nil)
	assert.Error(t, err)
	assert.Nil(t, extraction)
	assert.Contains(t, err.Error(), "document is nil or not in PENDING state")
}

func TestDocumentOCRWorker_ProcessPendingDocument_NonPending(t *testing.T) {
	worker := NewDocumentOCRWorker(nil, nil, nil, nil, nil, nil)

	doc := &Document{
		ID:        uuid.New(),
		FileName:  "statement.pdf",
		OCRStatus: "OCR_SUCCESS",
	}

	extraction, err := worker.ProcessPendingDocument(context.Background(), doc)
	assert.Error(t, err)
	assert.Nil(t, extraction)
	assert.Contains(t, err.Error(), "document is nil or not in PENDING state")
}
