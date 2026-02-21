package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestQueryMiddleware(t *testing.T) {
	// Generate a test key
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	auth := &Authenticator{
		PublicKey: pub,
		// No Redis needed for this test as we're testing the middleware logic, not revocation
	}

	// Create a valid token
	userID := uuid.New()
	entityID := uuid.New()
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, &UserClaims{
		UserID:   userID,
		EntityID: entityID,
		Role:     "user",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		},
	})
	validTokenString, err := token.SignedString(priv)
	if err != nil {
		t.Fatalf("Failed to sign token: %v", err)
	}

	tests := []struct {
		name           string
		setupRequest   func(*http.Request)
		expectedStatus int
	}{
		{
			name: "Valid Authorization Header",
			setupRequest: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+validTokenString)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "Valid Query Parameter",
			setupRequest: func(r *http.Request) {
				q := r.URL.Query()
				q.Set("token", validTokenString)
				r.URL.RawQuery = q.Encode()
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "Missing Token",
			setupRequest: func(r *http.Request) {
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "Invalid Token",
			setupRequest: func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer invalid-token")
			},
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/ws", nil)
			tt.setupRequest(req)

			rr := httptest.NewRecorder()
			handler := auth.QueryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			handler.ServeHTTP(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code)
		})
	}
}
