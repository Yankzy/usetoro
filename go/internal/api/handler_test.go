package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
)

// --- Mocks ---

type MockStore struct {
	Secret string
	Err    error
}

func (m *MockStore) GetWebhookSecret(ctx context.Context, connID string) (string, error) {
	if m.Err != nil {
		return "", m.Err
	}
	return m.Secret, nil
}

func (m *MockStore) Ping(ctx context.Context) error {
	return nil
}

func (m *MockStore) SaveQBOTokens(ctx context.Context, entityID, realmID, accessToken, refreshToken string, expiresAt time.Time) error {
	return nil
}

func (m *MockStore) GetQBOTokens(ctx context.Context, realmID string) (string, string, time.Time, string, error) {
	return "access", "refresh", time.Now().Add(time.Hour), "entity_id", nil
}
func (m *MockStore) GetQBOConnection(ctx context.Context, entityID string) (*database.ToroCoreErpConnection, error) {
	return &database.ToroCoreErpConnection{RealmID: "test-realm"}, nil
}

type MockPublisher struct {
	PublishErr error
	Events     []WebhookEvent
}

func (m *MockPublisher) PublishWebhookEvent(ctx context.Context, provider, connID, toroEventID, providerEventID, providerEventType string, body []byte) error {
	if m.PublishErr != nil {
		return m.PublishErr
	}
	m.Events = append(m.Events, WebhookEvent{
		Provider: provider,
		ID:       providerEventID,
		Type:     providerEventType,
		RawBody:  body,
	})
	return nil
}

func (m *MockPublisher) PublishRaw(ctx context.Context, subject string, data []byte) error {
	if m.PublishErr != nil {
		return m.PublishErr
	}
	return nil
}

func (m *MockPublisher) PublishQBOEvent(ctx context.Context, eventType, realmID string, data []byte) error {
	if m.PublishErr != nil {
		return m.PublishErr
	}
	// Just a mock implementation - doesn't need to do anything
	return nil
}

func (m *MockPublisher) Ping(ctx context.Context) error {
	return nil
}

// --- Tests ---

func TestHandleStripeWebhook(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	secret := "whsec_test_secret"

	tests := []struct {
		name           string
		connID         string
		payload        string
		signature      string
		mockSecret     string
		mockStoreErr   error
		mockPubErr     error
		expectedStatus int
	}{
		{
			name:           "Success",
			connID:         "conn_123",
			payload:        `{"id": "evt_123", "object": "event", "api_version": "2023-10-16"}`,
			signature:      generateSignature(t, `{"id": "evt_123", "object": "event", "api_version": "2023-10-16"}`, secret),
			mockSecret:     secret,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Missing Connection ID",
			connID:         "",
			payload:        `{}`,
			expectedStatus: http.StatusBadRequest, // Handler checks r.PathValue("conn_id")
		},
		{
			name:           "Store Lookup Failed",
			connID:         "conn_123",
			payload:        `{}`,
			mockStoreErr:   errors.New("db error"),
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Invalid Signature",
			connID:         "conn_123",
			payload:        `{"id": "evt_123"}`,
			signature:      "t=123,v1=invalid_sig",
			mockSecret:     secret,
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Publish Failed",
			connID:         "conn_123",
			payload:        `{"id": "evt_123", "object": "event", "api_version": "2023-10-16"}`,
			signature:      generateSignature(t, `{"id": "evt_123", "object": "event", "api_version": "2023-10-16"}`, secret),
			mockSecret:     secret,
			mockPubErr:     errors.New("nats error"),
			expectedStatus: http.StatusServiceUnavailable, // Changed to 503
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockStore := &MockStore{Secret: tc.mockSecret, Err: tc.mockStoreErr}
			mockPublisher := &MockPublisher{PublishErr: tc.mockPubErr}

			// Create verifier registry with Stripe verifier
			registry := NewVerifierRegistry()
			registry.Register(NewStripeVerifier())

			handler := NewHandler(
				logger,
				mockStore,
				mockPublisher,
				registry,
				1<<20,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil, // Added an extra nil argument here
			)

			// Construct request
			req := httptest.NewRequest(http.MethodPost, "/webhook/stripe/"+tc.connID, bytes.NewBuffer([]byte(tc.payload)))
			// Set the header manually (httptest doesn't route so PathValue isn't set automatically)
			req.Header.Set("Stripe-Signature", tc.signature)

			// Inject PathValue manually
			if tc.connID != "" {
				req.SetPathValue("conn_id", tc.connID)
			}

			w := httptest.NewRecorder()
			handler.HandleStripeWebhook(w, req)

			if w.Code != tc.expectedStatus {
				t.Errorf("expected status %d, got %d. Body: %s", tc.expectedStatus, w.Code, w.Body.String())
			}
		})
	}
}

func generateSignature(t *testing.T, payload, secret string) string {
	t.Helper()
	ts := time.Now().Unix()

	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(fmt.Sprintf("%d", ts)))
	h.Write([]byte("."))
	h.Write([]byte(payload))
	sig := hex.EncodeToString(h.Sum(nil))

	return fmt.Sprintf("t=%d,v1=%s", ts, sig)
}
