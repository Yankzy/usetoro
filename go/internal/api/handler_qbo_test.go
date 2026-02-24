package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
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

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	tests := []struct {
		name           string
		code           string
		realmID        string
		state          string
		expectedTenant string
		expectError    bool
	}{
		{
			name:           "Valid UUID State (Legacy)",
			code:           "auth_code",
			realmID:        "12345",
			state:          entityID,
			expectedTenant: entityID,
			expectError:    false,
		},
		{
			name:           "Valid JWT State",
			code:           "auth_code",
			realmID:        "12345",
			state:          validTokenString,
			expectedTenant: entityID,
			expectError:    false,
		},
		{
			name:        "Invalid State (Random String)",
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?code="+tc.code+"&realmId="+tc.realmID+"&state="+tc.state, nil)
			w := httptest.NewRecorder()

			handler := NewHandler(logger, nil, nil, nil, 0, nil, authenticator, nil, nil, nil)
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
		})
	}
}
