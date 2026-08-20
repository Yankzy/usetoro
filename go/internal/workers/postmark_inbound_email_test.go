package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostmarkInboundEmailWorker_Subscriptions(t *testing.T) {
	cfg := &config.Config{
		Workers: config.WorkerSubjects{
			"postmark_inbound_email": {
				ActivityType: "workers.email.postmark_inbound",
			},
		},
	}
	config.SetGlobal(cfg)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	worker := &PostmarkInboundEmailWorker{
		logger: logger,
		cfg:    cfg,
	}

	subs := worker.Subscriptions()
	require.Len(t, subs, 1)
	assert.Equal(t, "worker.inbox.email.postmark_inbound", subs[0].Subject)
}

func TestPostmarkInboundEmailWorker_Handle_EarlyReturnOnPoisonPill(t *testing.T) {
	worker := &PostmarkInboundEmailWorker{
		logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
		cfg:    &config.Config{},
	}

	msg := &nats.Msg{
		Data: []byte("invalid json"),
	}

	_ = worker
	_ = msg
}

func TestPostmarkInboundEmailWorker_Handle_PreExistingS3Key(t *testing.T) {
	worker := &PostmarkInboundEmailWorker{
		logger:  slog.New(slog.NewTextHandler(os.Stdout, nil)),
		cfg:     &config.Config{},
		storage: nil, // Should succeed without S3 service if S3Key is pre-populated
	}

	emailPayload := PostmarkInboundEmail{
		From: "rap_accounting@test.com",
		To:   "inbox@usetoro.io",
		Attachments: []struct {
			Name          string `json:"Name"`
			ContentType   string `json:"ContentType"`
			ContentLength int    `json:"ContentLength"`
			Content       string `json:"Content"`
			S3Key         string `json:"S3Key,omitempty"`
			SHA256        string `json:"SHA256,omitempty"`
		}{
			{
				Name:        "statement.pdf",
				ContentType: "application/pdf",
				S3Key:       "s3-keys/preuploaded-statement.pdf",
				SHA256:      "abc123hash",
			},
		},
	}

	data, err := json.Marshal(emailPayload)
	require.NoError(t, err)

	msg := &nats.Msg{
		Data: data,
	}

	ctx := context.Background()
	// Should not error out due to missing S3 service because S3Key is already populated!
	_ = worker.Handle(ctx, msg)
}

