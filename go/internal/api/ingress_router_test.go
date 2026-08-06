package api_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Yankzy/usetoro/internal/api"
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
