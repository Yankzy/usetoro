package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Yankzy/usetoro/internal/database"
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

func TestHandleCaptureForm(t *testing.T) {
	handler := &Handler{
		Logger: testLogger(),
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
					ID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
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
			payload:        "invalid-json", // This will be sent as a string, still valid JSON if quoted, but we'll send it as raw bytes
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
			}
		})
	}
}

func testLogger() *slog.Logger {
    return slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil))
}
