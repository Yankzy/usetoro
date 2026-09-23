package api_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/internal/api"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleIngressWorker_MetaChallenge(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := &api.Handler{
		Logger: logger,
	}

	t.Run("Meta WhatsApp Challenge via dot params", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ingress?activity-type=whatsapp_inbound&hub.mode=subscribe&hub.challenge=196745988&hub.verify_token=token123", nil)
		rec := httptest.NewRecorder()

		handler.HandleIngressWorker(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "text/plain", rec.Header().Get("Content-Type"))
		assert.Equal(t, "196745988", rec.Body.String())
	})

	t.Run("Meta WhatsApp Challenge via underscore params", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ingress?activity-type=whatsapp_inbound&hub_mode=subscribe&hub_challenge=889457691&hub_verify_token=token123", nil)
		rec := httptest.NewRecorder()

		handler.HandleIngressWorker(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "text/plain", rec.Header().Get("Content-Type"))
		assert.Equal(t, "889457691", rec.Body.String())
	})

	t.Run("Missing activity-type", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ingress?hub.mode=subscribe&hub.challenge=196745988", nil)
		rec := httptest.NewRecorder()

		handler.HandleIngressWorker(rec, req)

		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestHandleIngressWorker_PostmarkAuth(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := &api.Handler{
		Logger: logger,
	}

	cfg := &config.Config{
		PostmarkInboundWebhookSecret: "test-inbound-secret-123",
		PostmarkInboundUsername:      "toro_user",
		PostmarkInboundPassword:      "toro_pass",
	}
	config.SetGlobal(cfg)

	t.Run("Rejected with 401 when unauthenticated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/ingress?activity-type=email.postmark_inbound", strings.NewReader(`{"MessageID":"msg-1"}`))
		rec := httptest.NewRecorder()

		handler.HandleIngressWorker(rec, req)

		require.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Contains(t, rec.Header().Get("WWW-Authenticate"), "Basic")
	})

	t.Run("Rejected with 401 with invalid credentials", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/ingress?activity-type=email.postmark_inbound", strings.NewReader(`{"MessageID":"msg-1"}`))
		req.SetBasicAuth("wrong_user", "wrong_pass")
		rec := httptest.NewRecorder()

		handler.HandleIngressWorker(rec, req)

		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("Accepted via valid Custom Header (passes auth boundary)", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/ingress?activity-type=email.postmark_inbound", strings.NewReader(`{"MessageID":"msg-1"}`))
		req.Header.Set("X-Postmark-Webhook-Secret", "test-inbound-secret-123")
		rec := httptest.NewRecorder()

		defer func() {
			r := recover()
			assert.NotNil(t, r, "Auth passed boundary and proceeded to NATS execution")
		}()
		handler.HandleIngressWorker(rec, req)
	})

	t.Run("Accepted via valid Basic Auth (passes auth boundary)", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/ingress?activity-type=email.postmark_inbound", strings.NewReader(`{"MessageID":"msg-1"}`))
		req.SetBasicAuth("toro_user", "toro_pass")
		rec := httptest.NewRecorder()

		defer func() {
			r := recover()
			assert.NotNil(t, r, "Auth passed boundary and proceeded to NATS execution")
		}()
		handler.HandleIngressWorker(rec, req)
	})
}

func TestDerivePostmarkInboundNatsMsgID(t *testing.T) {
	t.Run("Deterministic MessageID mapping", func(t *testing.T) {
		payload := []byte(`{"MessageID":"2b93855a-355b-4ec9-866a-b28669e48937","Subject":"Invoice"}`)
		id1 := api.DerivePostmarkInboundNatsMsgID(payload)
		id2 := api.DerivePostmarkInboundNatsMsgID(payload)

		require.Equal(t, "postmark-inbound:2b93855a-355b-4ec9-866a-b28669e48937", id1)
		assert.Equal(t, id1, id2, "Same MessageID must generate identical deterministic NATS Message ID")
	})

	t.Run("Strips angle brackets from SMTP format", func(t *testing.T) {
		payload := []byte(`{"MessageID":"<unique-msg-id-456@mtasv.net>"}`)
		id := api.DerivePostmarkInboundNatsMsgID(payload)
		assert.Equal(t, "postmark-inbound:unique-msg-id-456@mtasv.net", id)
	})

	t.Run("Fallback on empty or missing MessageID", func(t *testing.T) {
		payload := []byte(`{"Subject":"No ID"}`)
		id := api.DerivePostmarkInboundNatsMsgID(payload)
		assert.True(t, strings.HasPrefix(id, "ingress-"))
	})
}
