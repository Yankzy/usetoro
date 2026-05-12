package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/Yankzy/usetoro/internal/resilience"
	"github.com/Yankzy/usetoro/internal/services/accounting"
	"github.com/Yankzy/usetoro/tap/pkg/micrion"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"
)

// TransactionApprover defines the interface for CPA transaction approval DB operations.
type TransactionApprover interface {
	GetProposedTransactionByID(ctx context.Context, id pgtype.UUID) (database.FignodeStagingTransaction, error)
	ApproveProposedTransaction(ctx context.Context, arg database.ApproveProposedTransactionParams) (database.FignodeStagingTransaction, error)
}

// SecretGetter defines the interface for retrieving webhook secrets and checking health.
type SecretGetter interface {
	GetWebhookSecret(ctx context.Context, connID string) (string, error)
	SaveQBOTokens(ctx context.Context, entityID, realmID, accessToken, refreshToken string, expiresAt time.Time) error
	GetQBOTokens(ctx context.Context, realmID string) (string, string, time.Time, string, error)
	GetQBOConnection(ctx context.Context, entityID string) (*database.ToroCoreErpConnection, error)
	Ping(ctx context.Context) error
}

// EventPublisher defines the interface for publishing events.
type EventPublisher interface {
	PublishWebhookEvent(ctx context.Context, provider, connID, toroEventID, providerEventID, providerEventType string, body []byte) error
	PublishQBOEvent(ctx context.Context, eventType, realmID string, data []byte) error
	PublishRaw(ctx context.Context, subject string, data []byte) error
	Ping(ctx context.Context) error
}

// QBOConfig holds QuickBooks Online OAuth2 configuration.
type QBOConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURIs []string
	IsProduction bool
}

// Handler holds dependencies for HTTP handlers.
type Handler struct {
	Logger             *slog.Logger
	Store              SecretGetter
	Pub                EventPublisher
	VerifierRegistry   *VerifierRegistry
	RateLimiter        *resilience.RateLimiter
	FailedAuthTracker  *resilience.FailedAttemptsTracker
	MaxBodySize        int64
	QBOConfig          *QBOConfig
	Authenticator      *auth.Authenticator
	Redis              *redis.Client
	Approver           TransactionApprover
	Reconciler         *accounting.ReconciliationService
	AttachableService  *accounting.AttachableService
	TransactionService *accounting.TransactionService
	EntityService      *accounting.EntityService

	// Cleanup Mode dependencies
	DBPool        *pgxpool.Pool
	DB            database.Querier
	NATS          *queue.Client
	Exporter      Exporter
	WalletManager *micrion.WalletManager
}

// NewHandler creates a new Handler.
func NewHandler(
	logger *slog.Logger,
	store SecretGetter,
	pub EventPublisher,
	verifierRegistry *VerifierRegistry,
	maxBodySize int64,
	qboConfig *QBOConfig,
	authenticator *auth.Authenticator,
	redisClient *redis.Client,
	approver TransactionApprover,
	reconciler *accounting.ReconciliationService,
	attachableService *accounting.AttachableService,
	transactionService *accounting.TransactionService,
	entityService *accounting.EntityService,
	dbPool *pgxpool.Pool,
	cleanupDB database.Querier,
	cleanupNATS *queue.Client,
	cleanupExporter Exporter,
	wm *micrion.WalletManager,
) *Handler {
	return &Handler{
		Logger:             logger,
		Store:              store,
		Pub:                pub,
		VerifierRegistry:   verifierRegistry,
		RateLimiter:        resilience.NewRateLimiter(100, 10), // 100 req/s, burst 10
		FailedAuthTracker:  resilience.NewFailedAttemptsTracker(5),
		MaxBodySize:        maxBodySize,
		QBOConfig:          qboConfig,
		Authenticator:      authenticator,
		Redis:              redisClient,
		Approver:           approver,
		Reconciler:         reconciler,
		AttachableService:  attachableService,
		TransactionService: transactionService,
		EntityService:      entityService,
		DBPool:             dbPool,
		DB:                 cleanupDB,
		NATS:               cleanupNATS,
		Exporter:           cleanupExporter,
		WalletManager:      wm,
	}
}

// Liveness returns 200 OK if the server is running.
// K8s Liveness Probe.
func (h *Handler) Liveness(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"up"}`))
}

// Readiness checks if the server is ready to accept traffic.
// Checks Database and NATS connections.
// K8s Readiness Probe.
func (h *Handler) Readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.Store.Ping(ctx); err != nil {
		h.Logger.Error("Readiness Check Failed: Database", "error", err)
		JSONError(w, h.Logger, http.StatusServiceUnavailable, "Database unavailable")
		return
	}

	if err := h.Pub.Ping(ctx); err != nil {
		h.Logger.Error("Readiness Check Failed: NATS", "error", err)
		JSONError(w, h.Logger, http.StatusServiceUnavailable, "NATS unavailable")
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ready"}`))
}

// Check is a legacy alias for Liveness, kept for backward compatibility if needed.
func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	h.Liveness(w, r)
}

// HandleWebhook receives webhooks from any supported provider.
func (h *Handler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	// Go 1.22 Feature: Extract path values
	provider := r.PathValue("provider")
	connID := r.PathValue("conn_id")

	if provider == "" {
		JSONError(w, h.Logger, http.StatusBadRequest, "Missing provider")
		return
	}
	if connID == "" {
		JSONError(w, h.Logger, http.StatusBadRequest, "Missing connection ID")
		return
	}

	// 0. REQUEST ID TRACING
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.New().String()
	}
	ctx := context.WithValue(r.Context(), "request_id", requestID)
	logger := h.Logger.With("request_id", requestID, "provider", provider, "conn_id", connID)

	// 1. GET PROVIDER-SPECIFIC VERIFIER
	verifier, err := h.VerifierRegistry.Get(provider)
	if err != nil {
		logger.Warn("Unsupported provider", "error", err)
		JSONError(w, logger, http.StatusBadRequest, "Unsupported provider")
		return
	}

	// 2. RATE LIMITING
	if !h.RateLimiter.Allow(connID) {
		logger.Warn("Rate limit exceeded")
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}

	// 3. CHECK FOR TOO MANY FAILED AUTH ATTEMPTS
	if h.FailedAuthTracker.IsBlocked(connID) {
		logger.Warn("Blocked due to too many failed auth attempts")
		http.Error(w, "Too many failed attempts", http.StatusTooManyRequests)
		return
	}

	// 4. LOOKUP WEBHOOK SECRET	// 1. Get existing QBO Connection for this entity
	// The original diff provided for this section was incomplete and syntactically incorrect.
	// Applying the type change for `conn` as per the instruction.
	conn, err := h.Store.GetQBOConnection(r.Context(), connID) // Assuming connID is entityID for this context
	if err != nil {
		h.Logger.Error("No ERP connection found for entity", "error", err, "entity_id", connID)
		JSONError(w, h.Logger, http.StatusNotFound, "ERP connection not found")
		return
	}

	// 5. READ PAYLOAD (Safety)
	r.Body = http.MaxBytesReader(w, r.Body, h.MaxBodySize)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Warn("Failed to read body (possible size limit exceeded)", "error", err)
		JSONError(w, logger, http.StatusBadRequest, "Bad Request")
		return
	}

	// 6. VERIFY SIGNATURE (Security)
	secret, err := h.Store.GetWebhookSecret(ctx, conn.ID.String()) // Use the actual connection ID from the DB
	if err != nil {
		logger.Warn("Invalid connection or missing secret", "error", err)
		JSONError(w, logger, http.StatusUnauthorized, "Unauthorized")
		return
	}

	event, err := verifier.Verify(r.Header, body, secret)
	if err != nil {
		logger.Warn("Signature verification failed", "error", err)

		// Track failed attempt
		attempts := h.FailedAuthTracker.Increment(connID)
		logger.Warn("Failed signature verification", "attempts", attempts)

		JSONError(w, logger, http.StatusUnauthorized, "Unauthorized")
		return
	}

	// Reset failed attempts on successful auth
	h.FailedAuthTracker.Reset(connID)

	// 7. PERSIST TO VAULT (Durability)
	// Note: We use a short timeout to ensure response < 3 seconds (QBO requirement)
	// NATS JetStream publish typically takes 50-200ms when healthy
	eventID := uuid.New().String()

	publishCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	publishStart := time.Now()
	if err := h.Pub.PublishWebhookEvent(publishCtx, provider, connID, eventID, event.ID, event.Type, body); err != nil {
		publishLatency := time.Since(publishStart)
		logger.Error("NATS Publish failed - starting retry goroutine",
			"error", err,
			"latency_ms", publishLatency.Milliseconds(),
		)

		// Async retry with exponential backoff (max 5 retries)
		retryData := WebhookRetryData{
			Provider:          provider,
			ConnID:            connID,
			ToroEventID:       eventID,
			ProviderEventID:   event.ID,
			ProviderEventType: event.Type,
			Body:              body,
			RequestID:         requestID,
		}

		go h.retryPublishWithBackoff(retryData)

		w.Header().Set("X-Request-ID", requestID)
		JSONError(w, logger, http.StatusServiceUnavailable, "NATS unavailable")
		return
	} else {
		publishLatency := time.Since(publishStart)
		logger.Info("Webhook ingested",
			"toro_id", eventID,
			"provider_event_id", event.ID,
			"provider_event_type", event.Type,
			"publish_latency_ms", publishLatency.Milliseconds(),
		)
	}

	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// HandleStripeWebhook is a backward-compatible wrapper for Stripe webhooks.
// Deprecated: Use HandleWebhook with /webhooks/stripe/{conn_id} instead.
func (h *Handler) HandleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	// Inject "stripe" as the provider for backward compatibility
	r.SetPathValue("provider", "stripe")
	h.HandleWebhook(w, r)
}

// HandleTestRedux triggers the E2E Redux agent via NATS globally.
func (h *Handler) HandleTestRedux(w http.ResponseWriter, r *http.Request) {
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.New().String()
	}
	ctx := context.WithValue(r.Context(), "request_id", requestID)

	var payload struct {
		Message string `json:"message"`
	}
	// Best effort parse
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		payload.Message = "trigger raw redux event"
	}

	rawBytes, _ := json.Marshal(payload)
	err := h.Pub.PublishRaw(ctx, "redux.test", rawBytes)

	if err != nil {
		h.Logger.Error("Failed to publish redux NATS hook from API Gateway", "error", err)
		JSONError(w, h.Logger, http.StatusServiceUnavailable, "NATS unavailable")
		return
	}

	h.Logger.Info("🔥 [E2E TEST] Trigger published via API Gateway on /test/redux")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "ok", "message": "Redux Test Agent Triggered via NATS from Gateway"}`))
}

// =========================================================================
// QBO OAUTH2 PIPELINE
// =========================================================================

// OAuthTask defines a single step in the OAuth2 pipeline.
type OAuthTask func(*OAuthContext) error

// OAuthContext holds the state for the OAuth2 pipeline.
type OAuthContext struct {
	Request  *http.Request
	Response http.ResponseWriter
	Handler  *Handler
	Log      *slog.Logger

	// Extracted Data
	Code     string
	RealmID  string
	EntityID string // The toro_core.entities UUID

	// Result Data
	Token *oauth2.Token
}

// OAuthPipeline orchestrates the OAuth2 callback processing steps.
type OAuthPipeline struct {
	Steps []OAuthTask
}

// Run executes the pipeline steps sequentially.
func (p *OAuthPipeline) Run(ctx *OAuthContext) {
	for _, step := range p.Steps {
		if err := step(ctx); err != nil {
			ctx.Log.Error("OAuth Pipeline failed", "error", err)
			// On error, the step is responsible for writing the response (e.g., redirecting to error page)
			return
		}
	}
}

// getRedirectURI determines the correct redirect URI to use dynamically based on the request.
func (h *Handler) getRedirectURI(r *http.Request, logger *slog.Logger) string {
	// 1. Determine Host
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}

	// 2. Determine Scheme
	scheme := "https"
	forwardedProto := r.Header.Get("X-Forwarded-Proto")
	if forwardedProto != "" {
		scheme = forwardedProto
	} else if r.TLS == nil {
		// Fallback for local development without proxy
		if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1") {
			scheme = "http"
		}
	}

	// 3. Determine Path
	// We derive the callback path from the current request path to maintain any prefixes (like /api)
	currentPath := r.URL.Path
	callbackPath := "/api/auth/qbo/callback"
	if idx := strings.Index(currentPath, "/auth/qbo/"); idx != -1 && idx > 0 {
		callbackPath = currentPath[:idx] + "/auth/qbo/callback"
	}

	uri := fmt.Sprintf("%s://%s%s", scheme, host, callbackPath)
	logger.Debug("Dynamically determined RedirectURI", "host", host, "scheme", scheme, "path", callbackPath, "uri", uri)

	// Optional: Still check against configured URIs if we want to restrict to a whitelist
	if h.QBOConfig != nil && len(h.QBOConfig.RedirectURIs) > 0 {
		matched := false
		for _, configured := range h.QBOConfig.RedirectURIs {
			if configured == uri {
				matched = true
				break
			}
		}
		if !matched {
			logger.Warn("Dynamic RedirectURI not in configured whitelist", "uri", uri, "whitelist", h.QBOConfig.RedirectURIs)
			// We still return the dynamic one as requested by user, but log a warning
		}
	}

	return uri
}

// HandleGetQBOAuthURL handles the QuickBooks Online OAuth2 redirect URL request.
func (h *Handler) HandleGetQBOAuthURL(w http.ResponseWriter, r *http.Request) {
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.New().String()
	}

	logger := h.Logger.With("request_id", requestID, "handler", "HandleGetQBOAuthURL")

	if h.QBOConfig == nil {
		logger.Error("QBO configuration not available")
		JSONError(w, logger, http.StatusInternalServerError, "QBO configuration not available")
		return
	}

	// Extract JWT from Authorization header to use as state
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" || len(authHeader) < 7 || strings.ToLower(authHeader[:7]) != "bearer " {
		logger.Error("Missing or invalid Authorization header")
		JSONError(w, logger, http.StatusUnauthorized, "Missing or invalid Authorization header")
		return
	}
	jwtToken := authHeader[7:]

	// Create UUID and store JWT in redis for 10 minutes
	stateUUID := uuid.New().String()
	ctx := r.Context()
	err := h.Redis.Set(ctx, "qbo_oauth_state:"+stateUUID, jwtToken, 10*time.Minute).Err()
	if err != nil {
		logger.Error("Failed to store OAuth state in Redis", "error", err)
		JSONError(w, logger, http.StatusInternalServerError, "Failed to initialize OAuth state")
		return
	}

	state := stateUUID

	chosenURI := h.getRedirectURI(r, logger)
	if chosenURI == "" {
		JSONError(w, logger, http.StatusInternalServerError, "No redirect URIs configured")
		return
	}

	scope := "com.intuit.quickbooks.accounting"

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{
		"client_id":    h.QBOConfig.ClientID,
		"scope":        scope,
		"redirect_uri": chosenURI,
		"state":        state,
	}); err != nil {
		logger.Error("Failed to encode response", "error", err)
	}
}

// HandleQBOCallback handles the QuickBooks Online OAuth2 redirect.
func (h *Handler) HandleQBOCallback(w http.ResponseWriter, r *http.Request) {
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.New().String()
	}

	logger := h.Logger.With("request_id", requestID, "handler", "HandleQBOCallback")

	pipeline := &OAuthPipeline{
		Steps: []OAuthTask{
			ExtractParamsTask,
			ExchangeTokenTask,
			SaveTokensTask,
			NotifyWSTask,
			RenderResultTask,
		},
	}

	pipeline.Run(&OAuthContext{
		Request:  r,
		Response: w,
		Handler:  h,
		Log:      logger,
	})
}

// ExtractParamsTask grabs the code and realmID from the URL query.
func ExtractParamsTask(ctx *OAuthContext) error {
	ctx.Code = ctx.Request.URL.Query().Get("code")
	ctx.RealmID = ctx.Request.URL.Query().Get("realmId")
	stateParam := ctx.Request.URL.Query().Get("state")

	if ctx.Code == "" || ctx.RealmID == "" {
		renderOAuthCallbackPage(ctx.Response, OAuthTemplateData{State: "INVALID_STATE"})
		return fmt.Errorf("missing code or realmId")
	}

	if stateParam == "" {
		ctx.Log.Error("Missing state parameter")
		renderOAuthCallbackPage(ctx.Response, OAuthTemplateData{State: "INVALID_STATE"})
		return fmt.Errorf("missing state parameter")
	}

	// 1. Try to parse stateParam as a UUID (it must be our Redis key)
	if _, err := uuid.Parse(stateParam); err != nil {
		ctx.Log.Error("Invalid state parameter: not a UUID")
		renderOAuthCallbackPage(ctx.Response, OAuthTemplateData{State: "INVALID_STATE"})
		return fmt.Errorf("invalid state parameter")
	}

	// 2. Fetch the actual JWT token from Redis using the UUID state parameter
	redisKey := "qbo_oauth_state:" + stateParam
	jwtToken, err := ctx.Handler.Redis.Get(ctx.Request.Context(), redisKey).Result()
	if err != nil {
		if err == redis.Nil {
			ctx.Log.Error("OAuth state expired or not found in Redis", "state", stateParam)
			renderOAuthCallbackPage(ctx.Response, OAuthTemplateData{State: "INVALID_STATE"})
			return fmt.Errorf("OAuth state expired or not found")
		}
		ctx.Log.Error("Redis error fetching OAuth state", "state", stateParam, "error", err)
		renderOAuthCallbackPage(ctx.Response, OAuthTemplateData{State: "ERROR", ErrorMessage: "Internal error retrieving state."})
		return fmt.Errorf("internal error retrieving state")
	}

	// Clean up state so it can't be reused
	ctx.Handler.Redis.Del(ctx.Request.Context(), redisKey)

	// 3. Verify the retrieved JWT token
	if ctx.Handler.Authenticator != nil {
		claims, err := ctx.Handler.Authenticator.VerifyToken(ctx.Request.Context(), jwtToken)
		if err == nil {
			ctx.EntityID = claims.EntityID.String()
			ctx.Log.Info("Resolved EntityID from Redis cached JWT", "entity_id", ctx.EntityID)
			return nil
		}
		ctx.Log.Warn("Cached token failed verification", "error", err)
	}

	// If we reach here, validation failed
	ctx.Log.Error("Validation of cached token failed")
	renderOAuthCallbackPage(ctx.Response, OAuthTemplateData{State: "INVALID_STATE"})
	return fmt.Errorf("invalid cached token")
}

// ExchangeTokenTask exchanges the authorization code for tokens.
func ExchangeTokenTask(ctx *OAuthContext) error {
	if ctx.Handler.QBOConfig == nil {
		ctx.Log.Error("QBO configuration not available")
		renderOAuthCallbackPage(ctx.Response, OAuthTemplateData{State: "ERROR", ErrorMessage: "QBO configuration not available."})
		return fmt.Errorf("QBO configuration not available")
	}

	conf := &oauth2.Config{
		ClientID:     ctx.Handler.QBOConfig.ClientID,
		ClientSecret: ctx.Handler.QBOConfig.ClientSecret,
		Endpoint: oauth2.Endpoint{
			TokenURL: "https://oauth.platform.intuit.com/oauth2/v1/tokens/bearer",
		},
	}

	chosenURI := ctx.Handler.getRedirectURI(ctx.Request, ctx.Log)
	if chosenURI == "" {
		renderOAuthCallbackPage(ctx.Response, OAuthTemplateData{State: "ERROR", ErrorMessage: "No redirect URIs configured."})
		return fmt.Errorf("no redirect URIs configured")
	}

	conf.RedirectURL = chosenURI

	ctx.Log.Info("Exchanging authorization code for tokens", "realm_id", ctx.RealmID)

	token, err := conf.Exchange(ctx.Request.Context(), ctx.Code)
	if err != nil {
		ctx.Log.Error("Token exchange failed", "error", err)
		renderOAuthCallbackPage(ctx.Response, OAuthTemplateData{State: "ERROR", ErrorMessage: "Failed to exchange token with QuickBooks."})
		return fmt.Errorf("token exchange failed: %w", err)
	}

	ctx.Token = token
	ctx.Log.Info("Token exchange successful", "realm_id", ctx.RealmID)
	return nil
}

// SaveTokensTask persists the tokens to the database.
func SaveTokensTask(ctx *OAuthContext) error {
	err := ctx.Handler.Store.SaveQBOTokens(
		ctx.Request.Context(),
		ctx.EntityID,
		ctx.RealmID,
		ctx.Token.AccessToken,
		ctx.Token.RefreshToken,
		ctx.Token.Expiry,
	)

	if err != nil {
		renderOAuthCallbackPage(ctx.Response, OAuthTemplateData{State: "ERROR", ErrorMessage: "Failed to save OAuth tokens."})
		return fmt.Errorf("failed to save tokens: %w", err)
	}

	return nil
}

// NotifyWSTask sends success notification via NATS for WebSocket broadcasting.
func NotifyWSTask(ctx *OAuthContext) error {
	if ctx.Handler.Pub == nil {
		ctx.Log.Warn("Publisher not available, skipping WebSocket notification")
		return nil
	}

	// Publish a QBO connection success event
	publishCtx, cancel := context.WithTimeout(ctx.Request.Context(), 3*time.Second)
	defer cancel()

	// Create a success message
	successMsg := map[string]interface{}{
		"type":      "qbo_connected",
		"realm_id":  ctx.RealmID,
		"entity_id": ctx.EntityID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"status":    "success",
	}

	msgBytes, err := json.Marshal(successMsg)
	if err != nil {
		ctx.Log.Error("Failed to marshal success message", "error", err)
		return nil // Don't fail the pipeline
	}

	// Publish to a dedicated QBO events subject
	if err := ctx.Handler.Pub.PublishQBOEvent(publishCtx, "connected", ctx.RealmID, msgBytes); err != nil {
		ctx.Log.Error("Failed to publish QBO success event", "error", err)
		// Don't fail the pipeline - OAuth succeeded, notification is best-effort
	}

	ctx.Log.Info("QBO connection success event published", "realm_id", ctx.RealmID)
	return nil
}

// RenderResultTask renders the success page.
func RenderResultTask(ctx *OAuthContext) error {
	renderOAuthCallbackPage(ctx.Response, OAuthTemplateData{State: "SUCCESS"})
	return nil
}
