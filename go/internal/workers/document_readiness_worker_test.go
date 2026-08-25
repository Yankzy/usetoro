package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocumentReadinessWorker_Subscriptions(t *testing.T) {
	cfg := &config.Config{
		Workers: config.WorkerSubjects{
			"document_readiness": {
				ActivityType: "workers.document_readiness",
			},
		},
	}
	config.SetGlobal(cfg)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	worker := &DocumentReadinessWorker{
		logger: logger,
		cfg:    cfg,
	}

	subs := worker.Subscriptions()
	require.Len(t, subs, 1)
	assert.Equal(t, "worker.inbox.document_readiness", subs[0].Subject)
}

func TestDocumentReadinessWorker_NoDocuments(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	worker := &DocumentReadinessWorker{
		logger: logger,
		cfg:    &config.Config{},
		pool:   nil,
		nc:     nil,
	}

	env := core.Envelope{
		ID:             "env-123",
		ConversationID: "conv-123",
		Performative:   core.REQUEST,
		Body:           []byte(`{"payload":{"data":{"input":{"session_id":"sess-1"}}}}`),
	}
	data, err := json.Marshal(env)
	require.NoError(t, err)

	msg := &nats.Msg{Data: data}
	err = worker.Handle(context.Background(), msg)
	require.ErrorContains(t, err, "NATS connection unavailable")
}
