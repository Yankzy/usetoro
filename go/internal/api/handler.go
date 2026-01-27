package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/Yankzy/usetoro/internal/resilience"
	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/webhook"
)

// SecretGetter defines the interface for retrieving webhook secrets and checking health.
type SecretGetter interface {
	GetWebhookSecret(ctx context.Context, connID string) (string, error)
	Ping(ctx context.Context) error
}

// EventPublisher defines the interface for publishing events.
type EventPublisher interface {
	PublishStripeEvent(ctx context.Context, connID, toroEventID string, event stripe.Event, body []byte) error
	Ping(ctx context.Context) error
}

// Handler holds dependencies for HTTP handlers.
type Handler struct {
	Logger            *slog.Logger
	Store             SecretGetter
	Pub               EventPublisher
	RateLimiter       *resilience.RateLimiter
	FailedAuthTracker *resilience.FailedAttemptsTracker
	MaxBodySize       int64
}

// NewHandler creates a new Handler.
func NewHandler(logger *slog.Logger, store SecretGetter, pub EventPublisher, maxBodySize int64) *Handler {
	return &Handler{
		Logger:            logger,
		Store:             store,
		Pub:               pub,
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

// HandleStripeWebhook receives webhooks from Stripe.
func (h *Handler) HandleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	// Go 1.22 Feature: Extract path value
	connID := r.PathValue("conn_id")
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
	logger := h.Logger.With("request_id", requestID, "conn_id", connID)

	// 1. RATE LIMITING
	if !h.RateLimiter.Allow(connID) {
		logger.Warn("Rate limit exceeded")
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}

	// 2. CHECK FOR TOO MANY FAILED AUTH ATTEMPTS
	if h.FailedAuthTracker.IsBlocked(connID) {
		logger.Warn("Blocked due to too many failed auth attempts")
		http.Error(w, "Too many failed attempts", http.StatusTooManyRequests)
		return
	}

	// 3. LOOKUP (Hot Path Optimization)
	secret, err := h.Store.GetWebhookSecret(ctx, connID)
	if err != nil {
		logger.Warn("Invalid connection or missing secret", "error", err)
		JSONError(w, logger, http.StatusUnauthorized, "Unauthorized")
		return
	}

	// 4. READ PAYLOAD (Safety)
	r.Body = http.MaxBytesReader(w, r.Body, h.MaxBodySize)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Warn("Failed to read body (possible size limit exceeded)", "error", err)
		JSONError(w, logger, http.StatusBadRequest, "Bad Request")
		return
	}

	// 5. VERIFY SIGNATURE (Security)
	signature := r.Header.Get("Stripe-Signature")
	event, err := webhook.ConstructEvent(body, signature, secret)
	if err != nil {
		logger.Warn("Signature verification failed")

		// Track failed attempt
		attempts := h.FailedAuthTracker.Increment(connID)
		logger.Warn("Failed signature verification", "attempts", attempts)

		JSONError(w, logger, http.StatusUnauthorized, "Unauthorized")
		return
	}

	// Reset failed attempts on successful auth
	h.FailedAuthTracker.Reset(connID)

	// 6. PERSIST TO VAULT (Durability)
	eventID := uuid.New().String()

	publishCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := h.Pub.PublishStripeEvent(publishCtx, connID, eventID, event, body); err != nil {
		logger.Error("NATS Publish failed", "error", err)
		JSONError(w, logger, http.StatusServiceUnavailable, "Service Temporarily Unavailable")
		return
	}

	logger.Info("Webhook ingested", "toro_id", eventID, "stripe_id", event.ID)
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}
