package workers

import (
	"log/slog"
	"os"
	"testing"

	know "github.com/Yankzy/usetoro/internal/erp/ase/knowledge_system"
	"github.com/stretchr/testify/assert"
)

func TestDocumentWorker_Subscriptions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	worker := &DocumentWorker{
		logger: logger,
	}

	subs := worker.Subscriptions()
	assert.Len(t, subs, 1)
	assert.Equal(t, "worker.inbox.document.create", subs[0].Subject)
}

func TestDocumentStatus_Constants(t *testing.T) {
	assert.Equal(t, "PENDING", string(know.DocStatusPending))
	assert.Equal(t, "OCR_SUCCESS", string(know.DocStatusOCRSuccess))
	assert.Equal(t, "EMBEDDINGS_SUCCESS", string(know.DocStatusEmbeddingsSuccess))
	assert.Equal(t, "FAILED", string(know.DocStatusFailed))
}
