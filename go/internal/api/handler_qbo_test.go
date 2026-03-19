package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/go-redis/redismock/v9"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestExtractParamsTask(t *testing.T) {
	// Setup Keys
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	authenticator := &auth.Authenticator{PublicKey: pub}

	// Create a valid token
	entityID := uuid.New().String()
	userID := uuid.New()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, &auth.UserClaims{
		UserID:   userID,
		EntityID: uuid.MustParse(entityID),
		Role:     "user",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		},
	})
	validTokenString, _ := token.SignedString(priv)

	validStateUUID := uuid.New().String()
	invalidTokenStateUUID := uuid.New().String()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	tests := []struct {
		name           string
		code           string
		realmID        string
		state          string
		redisVal       string
		redisErr       error
		expectedTenant string
		expectError    bool
	}{
		{
			name:           "Valid State mapping to JWT",
			code:           "auth_code",
			realmID:        "12345",
			state:          validStateUUID,
			redisVal:       validTokenString,
			expectedTenant: entityID,
			expectError:    false,
		},
		{
			name:        "Valid State mapping to invalid token",
			code:        "auth_code",
			realmID:     "12345",
			state:       invalidTokenStateUUID,
			redisVal:    "invalid.jwt.token",
			expectError: true,
		},
		{
			name:        "Invalid State (Not UUID)",
			code:        "auth_code",
			realmID:     "12345",
			state:       "invalid_state",
			expectError: true,
		},
		{
			name:        "Missing State",
			code:        "auth_code",
			realmID:     "12345",
			state:       "",
			expectError: true,
		},
		{
			name:        "State Not Found in Redis",
			code:        "auth_code",
			realmID:     "12345",
			state:       uuid.New().String(),
			redisErr:    redis.Nil,
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?code="+tc.code+"&realmId="+tc.realmID+"&state="+tc.state, nil)
			w := httptest.NewRecorder()

			redisClient, mock := redismock.NewClientMock()
			if tc.state != "" && tc.state != "invalid_state" {
				if tc.redisErr != nil {
					mock.ExpectGet("qbo_oauth_state:" + tc.state).SetErr(tc.redisErr)
				} else {
					mock.ExpectGet("qbo_oauth_state:" + tc.state).SetVal(tc.redisVal)
					mock.ExpectDel("qbo_oauth_state:" + tc.state).SetVal(1)
				}
			}

			handler := NewHandler(
				logger,
				nil, // store (not fully tested here)
				nil, // publisher
				nil, // verifierRegistry
				1<<20,
				nil,           // qboConfig (mocked internally where needed or tested separately)
				authenticator, // using our real authenticator configured with test keys
				redisClient,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
				nil,
			)
			ctx := &OAuthContext{
				Request:  req,
				Response: w,
				Handler:  handler,
				Log:      logger,
			}

			err := ExtractParamsTask(ctx)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if ctx.EntityID != tc.expectedTenant {
					t.Errorf("Expected EntityID %s, got %s", tc.expectedTenant, ctx.EntityID)
				}
			}

			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("there were unfulfilled expectations: %s", err)
			}
		})
	}
}

func TestHandleGetQBOAuthURL(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	tests := []struct {
		name         string
		authHeader   string
		host         string
		qboConfig    *QBOConfig
		expectedCode int
		expectedBody string
	}{
		{
			name:       "Success - with Bearer Token",
			authHeader: "Bearer valid-jwt-token-123",
			host:       "localhost:8080",
			qboConfig: &QBOConfig{
				ClientID:     "test-client-id",
				RedirectURIs: []string{"http://localhost:8080/auth/qbo/callback"},
				IsProduction: false,
			},
			expectedCode: http.StatusOK,
			expectedBody: "state", // Should return JSON containing 'state', 'client_id', 'redirect_uri', etc.
		},
		{
			name:       "Missing Auth Header",
			authHeader: "",
			host:       "localhost:8080",
			qboConfig: &QBOConfig{
				ClientID:     "test-client-id",
				RedirectURIs: []string{"http://localhost:8080/auth/qbo/callback"},
				IsProduction: false,
			},
			expectedCode: http.StatusUnauthorized,
			expectedBody: "Missing or invalid Authorization header",
		},
		{
			name:         "Missing QBO Config",
			authHeader:   "Bearer valid-jwt-token-123",
			host:         "localhost:8080",
			qboConfig:    nil,
			expectedCode: http.StatusInternalServerError,
			expectedBody: "QBO configuration not available",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/auth/qbo/url", nil)
			req.Host = tc.host
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}

			w := httptest.NewRecorder()

			redisClient, mock := redismock.NewClientMock()
			if tc.expectedCode == http.StatusOK {
				mock.Regexp().ExpectSet("qbo_oauth_state:.*", "valid-jwt-token-123", 10*time.Minute).SetVal("OK")
			}

			handler := NewHandler(logger, nil, nil, nil, 0, tc.qboConfig, nil, redisClient, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			handler.HandleGetQBOAuthURL(w, req)

			if w.Code != tc.expectedCode {
				t.Errorf("Expected status code %d, got %d", tc.expectedCode, w.Code)
			}

			if tc.expectedBody != "" {
				if !strings.Contains(w.Body.String(), tc.expectedBody) {
					t.Errorf("Expected body to contain %q, but got %q", tc.expectedBody, w.Body.String())
				}
			}

			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("there were unfulfilled expectations: %s", err)
			}
		})
	}
}
