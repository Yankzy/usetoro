package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SlackPKCEState is the state stored in Redis during the Slack OAuth flow.
type SlackPKCEState struct {
	TenantID     string `json:"tenant_id"`
	CodeVerifier string `json:"code_verifier"`
	RedirectURI  string `json:"redirect_uri"`
}

// GenerateCodeVerifier generates a 32-byte secure random string, URL-safe base64 encoded without padding.
func GenerateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// GenerateCodeChallenge generates a SHA256 hash of the code verifier, URL-safe base64 encoded without padding.
func GenerateCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

// HandleGetSlackAuthURL handles the Slack OAuth2 redirect URL request with PKCE.
func (h *Handler) HandleGetSlackAuthURL(w http.ResponseWriter, r *http.Request) {
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.New().String()
	}

	logger := h.Logger.With("request_id", requestID, "handler", "HandleGetSlackAuthURL")

	slackClientID := os.Getenv("SLACK_CLIENT_ID")
	if slackClientID == "" {
		logger.Error("SLACK_CLIENT_ID is not configured")
		JSONError(w, logger, http.StatusInternalServerError, "Slack configuration not available")
		return
	}

	redirectURI := os.Getenv("SLACK_REDIRECT_URI")
	if redirectURI == "" {
		host := r.Header.Get("X-Forwarded-Host")
		if host == "" {
			host = r.Host
		}
		scheme := "https"
		if fProto := r.Header.Get("X-Forwarded-Proto"); fProto != "" {
			scheme = fProto
		} else if r.TLS == nil && (strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1")) {
			scheme = "http"
		}
		redirectURI = scheme + "://" + host + "/api/ingress?activity-type=slack.oauth"
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" || len(authHeader) < 7 || strings.ToLower(authHeader[:7]) != "bearer " {
		logger.Error("Missing or invalid Authorization header")
		JSONError(w, logger, http.StatusUnauthorized, "Missing or invalid Authorization header")
		return
	}
	jwtToken := authHeader[7:]

	var tenantID string
	if h.Authenticator != nil {
		claims, err := h.Authenticator.VerifyToken(r.Context(), jwtToken)
		if err != nil {
			logger.Error("Failed to verify token", "error", err)
			JSONError(w, logger, http.StatusUnauthorized, "Invalid token")
			return
		}
		tenantID = claims.EntityID.String()
	} else {
		logger.Error("Authenticator is not initialized")
		JSONError(w, logger, http.StatusInternalServerError, "Auth system not initialized")
		return
	}

	// PKCE setup
	codeVerifier, err := GenerateCodeVerifier()
	if err != nil {
		logger.Error("Failed to generate code verifier", "error", err)
		JSONError(w, logger, http.StatusInternalServerError, "Failed to initialize PKCE")
		return
	}
	codeChallenge := GenerateCodeChallenge(codeVerifier)

	stateUUID := uuid.New().String()
	pkceState := SlackPKCEState{
		TenantID:     tenantID,
		CodeVerifier: codeVerifier,
		RedirectURI:  redirectURI,
	}
	
	stateBytes, err := json.Marshal(pkceState)
	if err != nil {
		logger.Error("Failed to marshal PKCE state", "error", err)
		JSONError(w, logger, http.StatusInternalServerError, "Failed to encode state")
		return
	}

	ctx := r.Context()
	err = h.Redis.Set(ctx, "slack_pkce_state:"+stateUUID, string(stateBytes), 10*time.Minute).Err()
	if err != nil {
		logger.Error("Failed to store OAuth state in Redis", "error", err)
		JSONError(w, logger, http.StatusInternalServerError, "Failed to initialize OAuth state")
		return
	}

	// Requested scopes for the workspace bot
	scopes := []string{
		"app_mentions:read",
		"channels:history",
		"channels:join",
		"channels:read",
		"chat:write",
		"chat:write.public",
		"groups:history",
		"groups:read",
		"im:history",
		"im:read",
		"im:write",
		"mpim:history",
		"mpim:read",
		"users:read",
		"users:read.email",
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{
		"client_id":             slackClientID,
		"scope":                 strings.Join(scopes, ","),
		"redirect_uri":          redirectURI,
		"state":                 stateUUID,
		"code_challenge":        codeChallenge,
		"code_challenge_method": "S256",
	}); err != nil {
		logger.Error("Failed to encode response", "error", err)
	}
}
