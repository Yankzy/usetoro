package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/Yankzy/usetoro/internal/resilience"
	"github.com/google/uuid"
)

// SecretGetter defines the interface for retrieving webhook secrets and checking health.
type SecretGetter interface {
	GetWebhookSecret(ctx context.Context, connID string) (string, error)
	Ping(ctx context.Context) error
}

// EventPublisher defines the interface for publishing events.
type EventPublisher interface {
	PublishWebhookEvent(ctx context.Context, provider, connID, toroEventID, providerEventID, providerEventType string, body []byte) error
	Ping(ctx context.Context) error
}

// Handler holds dependencies for HTTP handlers.
type Handler struct {
	Logger            *slog.Logger
	Store             SecretGetter
	Pub               EventPublisher
	VerifierRegistry  *VerifierRegistry
	RateLimiter       *resilience.RateLimiter
	FailedAuthTracker *resilience.FailedAttemptsTracker
	MaxBodySize       int64
}

// NewHandler creates a new Handler.
func NewHandler(logger *slog.Logger, store SecretGetter, pub EventPublisher, verifierRegistry *VerifierRegistry, maxBodySize int64) *Handler {
	return &Handler{
		Logger:            logger,
		Store:             store,
		Pub:               pub,
		VerifierRegistry:  verifierRegistry,
		RateLimiter:       resilience.NewRateLimiter(100, 10), // 100 req/s, burst 10
		FailedAuthTracker: resilience.NewFailedAttemptsTracker(5),
		MaxBodySize:       maxBodySize,
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

	// 4. LOOKUP WEBHOOK SECRET (Hot Path Optimization)
	secret, err := h.Store.GetWebhookSecret(ctx, connID)
	if err != nil {
		logger.Warn("Invalid connection or missing secret", "error", err)
		JSONError(w, logger, http.StatusUnauthorized, "Unauthorized")
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
	eventID := uuid.New().String()

	publishCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := h.Pub.PublishWebhookEvent(publishCtx, provider, connID, eventID, event.ID, event.Type, body); err != nil {
		logger.Error("NATS Publish failed", "error", err)
		JSONError(w, logger, http.StatusServiceUnavailable, "Service Temporarily Unavailable")
		return
	}

	logger.Info("Webhook ingested", "toro_id", eventID, "provider_event_id", event.ID, "provider_event_type", event.Type)
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
