package api_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/internal/api"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockPublisher struct {
	publishedRawSubjects []string
}

func (m *mockPublisher) PublishWebhookEvent(ctx context.Context, provider, connID, toroEventID, providerEventID, providerEventType string, body []byte) error {
	return nil
}

func (m *mockPublisher) PublishQBOEvent(ctx context.Context, eventType, realmID string, data []byte) error {
	return nil
}

func (m *mockPublisher) PublishRaw(ctx context.Context, subject string, data []byte) error {
	m.publishedRawSubjects = append(m.publishedRawSubjects, subject)
	return nil
}

func (m *mockPublisher) Ping(ctx context.Context) error {
	return nil
}

func TestHandleMailpoolWebhook(t *testing.T) {
	cfg := &config.Config{
		MailpoolWebhookSecret: "test-secret",
	}
	config.SetGlobal(cfg)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	mockPub := &mockPublisher{}

	handler := &api.Handler{
		Logger: logger,
		Pub:    mockPub,
	}

	payload := []byte(`{"type":"mailboxes.created","mailbox":{"email":"test@usetoro.io"}}`)
	mac := hmac.New(sha256.New, []byte("test-secret"))
	mac.Write(payload)
	validSig := hex.EncodeToString(mac.Sum(nil))

	t.Run("Valid Signature", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/mailpool", bytes.NewBuffer(payload))
		req.Header.Set("X-Signature", validSig)
		rec := httptest.NewRecorder()

		handler.HandleMailpoolWebhook(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "received")
		
		// Wait for the goroutine to publish
		time.Sleep(50 * time.Millisecond)
		require.Contains(t, mockPub.publishedRawSubjects, "webhooks.mailpool.received")
	})

	t.Run("Invalid Signature", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/mailpool", bytes.NewBuffer(payload))
		req.Header.Set("X-Signature", "invalid-sig")
		rec := httptest.NewRecorder()

		handler.HandleMailpoolWebhook(rec, req)

		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("Missing Signature", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/mailpool", bytes.NewBuffer(payload))
		rec := httptest.NewRecorder()

		handler.HandleMailpoolWebhook(rec, req)

		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}
