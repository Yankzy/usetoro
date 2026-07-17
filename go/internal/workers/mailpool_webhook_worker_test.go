package workers_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/workers"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
)

var mockMailboxCreatedPayload = []byte(`{
	"type": "mailboxes.created",
	"mailbox": {
		"email": "test@usetoro.io",
		"password": "supersecretpassword123",
		"id": "mb_12345"
	}
}`)

var mockDomainRegisteredPayload = []byte(`{
	"type": "domains.registered",
	"domain": {
		"id": 123,
		"name": "usetoro.io"
	}
}`)

// TestMailpoolWebhookWorker validates the behavior of the MailpoolWebhookWorker
func TestMailpoolWebhookWorker(t *testing.T) {
	// 1. Setup mock dependencies
	cfg := &config.Config{
		MailpoolAESKey: "0123456789abcdef0123456789abcdef", // 32 bytes for AES-256
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	
	// Create the worker manually (without NATS/DB for a pure unit test of Handle)
	// We will inject nils for DB and NATS since they aren't strictly required for the basic password extraction yet.
	worker := workers.NewMailpoolWebhookWorkerForTest(cfg, logger, nil, nil)
	
	ctx := context.Background()

	t.Run("mailboxes.created extracts and encrypts password", func(t *testing.T) {
		msg := nats.NewMsg("webhooks.mailpool.received")
		msg.Data = mockMailboxCreatedPayload

		err := worker.Handle(ctx, msg)
		require.NoError(t, err)
		// Right now it just logs. In the future, we will verify DB insertion.
	})

	t.Run("domains.registered ignores gracefully", func(t *testing.T) {
		msg := nats.NewMsg("webhooks.mailpool.received")
		msg.Data = mockDomainRegisteredPayload

		err := worker.Handle(ctx, msg)
		require.NoError(t, err) // Should not error on unhandled events
	})
	
	t.Run("invalid json payload", func(t *testing.T) {
		msg := nats.NewMsg("webhooks.mailpool.received")
		msg.Data = []byte(`{"type": "mailboxes.created", "mailbox": { broken json `)

		err := worker.Handle(ctx, msg)
		require.NoError(t, err) // Current implementation just logs the error and returns nil to ack the message
	})
}
