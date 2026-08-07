package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/jackc/pgx/v5/pgtype"
)

// MockQuerier is a mock implementation of database.Querier.
// We only implement the methods we need for the tests.
type MockQuerier struct {
	database.Querier // Embed to satisfy interface
	CreateLeadFormFunc func(ctx context.Context, arg database.CreateLeadFormParams) (database.MarketingLeadForm, error)
}

func (m *MockQuerier) CreateLeadForm(ctx context.Context, arg database.CreateLeadFormParams) (database.MarketingLeadForm, error) {
	if m.CreateLeadFormFunc != nil {
		return m.CreateLeadFormFunc(ctx, arg)
	}
	return database.MarketingLeadForm{}, nil
}

// MockEventPublisher captures events published during testing.
type MockEventPublisher struct {
	PublishedRaw []struct {
		Subject string
		Data    []byte
	}
}

func (m *MockEventPublisher) PublishWebhookEvent(ctx context.Context, provider, connID, toroEventID, providerEventID, providerEventType string, body []byte) error {
	return nil
}

func (m *MockEventPublisher) PublishQBOEvent(ctx context.Context, eventType, realmID string, data []byte) error {
	return nil
}

func (m *MockEventPublisher) PublishRaw(ctx context.Context, subject string, data []byte) error {
	m.PublishedRaw = append(m.PublishedRaw, struct {
		Subject string
		Data    []byte
	}{Subject: subject, Data: data})
	return nil
}

func (m *MockEventPublisher) Ping(ctx context.Context) error {
	return nil
}

func TestHandleCaptureForm(t *testing.T) {
	mockPub := &MockEventPublisher{}
	handler := &Handler{
		Logger: testLogger(),
		Pub:    mockPub,
	}

	tests := []struct {
		name           string
		website        string
		payload        interface{}
		mockFunc       func(ctx context.Context, arg database.CreateLeadFormParams) (database.MarketingLeadForm, error)
		expectedStatus int
	}{
		{
			name:    "Success",
			website: "example.com",
			payload: map[string]string{"name": "John Doe", "email": "john@example.com"},
			mockFunc: func(ctx context.Context, arg database.CreateLeadFormParams) (database.MarketingLeadForm, error) {
				if arg.Website != "example.com" {
					return database.MarketingLeadForm{}, errors.New("wrong website")
				}
				return database.MarketingLeadForm{
					ID: pgtype.UUID{Bytes: [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}, Valid: true},
				}, nil
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name:           "Missing Website",
			website:        "",
			payload:        map[string]string{"foo": "bar"},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Invalid JSON",
			website:        "test.com",
			payload:        "invalid-json", // Raw invalid json bytes below
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:    "Database Error",
			website: "fail.com",
			payload: map[string]string{"foo": "bar"},
			mockFunc: func(ctx context.Context, arg database.CreateLeadFormParams) (database.MarketingLeadForm, error) {
				return database.MarketingLeadForm{}, errors.New("db error")
			},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockPub.PublishedRaw = nil // reset
			mockDB := &MockQuerier{
				CreateLeadFormFunc: tc.mockFunc,
			}
			handler.DB = mockDB

			var body []byte
			if tc.name == "Invalid JSON" {
				body = []byte("{ invalid: json }")
			} else {
				body, _ = json.Marshal(tc.payload)
			}

			req := httptest.NewRequest(http.MethodPost, "/forms/"+tc.website, bytes.NewBuffer(body))
			if tc.website != "" {
				req.SetPathValue("website", tc.website)
			}

			w := httptest.NewRecorder()
			handler.HandleCaptureForm(w, req)

			if w.Code != tc.expectedStatus {
				t.Errorf("Expected status %d, got %d. Body: %s", tc.expectedStatus, w.Code, w.Body.String())
			}

			if tc.expectedStatus == http.StatusCreated {
				var resp map[string]interface{}
				if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
					t.Fatalf("Failed to decode response: %v", err)
				}
				if resp["status"] != "success" {
					t.Errorf("Expected status 'success', got %v", resp["status"])
				}
				if resp["id"] == nil {
					t.Error("Expected ID in response, got nil")
				}

				// Verify NATS event published to proof.outgoing.chat
				if len(mockPub.PublishedRaw) != 1 {
					t.Fatalf("Expected 1 NATS raw event published, got %d", len(mockPub.PublishedRaw))
				}
				pubEvent := mockPub.PublishedRaw[0]
				if pubEvent.Subject != "proof.outgoing.chat" {
					t.Errorf("Expected NATS subject 'proof.outgoing.chat', got '%s'", pubEvent.Subject)
				}

				var env core.Envelope
				if err := json.Unmarshal(pubEvent.Data, &env); err != nil {
					t.Fatalf("Failed to unmarshal envelope: %v", err)
				}
				if env.Performative != core.INFORM {
					t.Errorf("Expected performative INFORM, got %v", env.Performative)
				}

				var proof core.Proof
				if err := json.Unmarshal(env.Body, &proof); err != nil {
					t.Fatalf("Failed to unmarshal proof: %v", err)
				}

				var response map[string]interface{}
				if err := json.Unmarshal(proof.Data, &response); err != nil {
					t.Fatalf("Failed to unmarshal proof response data: %v", err)
				}

				if response["source"] != "email" {
					t.Errorf("Expected source 'email', got %v", response["source"])
				}
				if response["from_handle"] != "forms@usetoro.io" {
					t.Errorf("Expected from_handle 'forms@usetoro.io', got %v", response["from_handle"])
				}
				if response["to_handle"] != "yankz@usetoro.io" {
					t.Errorf("Expected to_handle 'yankz@usetoro.io', got %v", response["to_handle"])
				}
				if !strings.Contains(response["subject"].(string), "example.com") {
					t.Errorf("Expected subject to contain 'example.com', got %v", response["subject"])
				}
				if !strings.Contains(response["body_text"].(string), "john@example.com") {
					t.Errorf("Expected body_text to contain 'john@example.com', got %v", response["body_text"])
				}
			}
		})
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil))
}

