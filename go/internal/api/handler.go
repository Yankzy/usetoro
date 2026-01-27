package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/Yankzy/usetoro/internal/ingest"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v76/webhook"
)

// Handler holds dependencies for HTTP handlers.
type Handler struct {
	Logger *slog.Logger
	Store  *store.Store
	Pub    *ingest.Publisher
}

// NewHandler creates a new Handler.
func NewHandler(logger *slog.Logger, store *store.Store, pub *ingest.Publisher) *Handler {
	return &Handler{
		Logger: logger,
		Store:  store,
		Pub:    pub,
	}
}

// Check checks if the server is up.
func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// HandleStripeWebhook receives webhooks from Stripe.
func (h *Handler) HandleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	// Go 1.22 Feature: Extract path value
	connID := r.PathValue("conn_id")
	if connID == "" {
		http.Error(w, "Missing connection ID", http.StatusBadRequest)
		return
	}

	// 1. LOOKUP (Hot Path Optimization)
	secret, err := h.Store.GetWebhookSecret(r.Context(), connID)
	if err != nil {
		h.Logger.Warn("Invalid connection or missing secret", "id", connID, "error", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// 2. READ PAYLOAD (Safety)
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.Logger.Warn("Failed to read body (possible size limit exceeded)", "id", connID, "error", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// 3. VERIFY SIGNATURE (Security)
	signature := r.Header.Get("Stripe-Signature")
	event, err := webhook.ConstructEvent(body, signature, secret)
	if err != nil {
		h.Logger.Warn("Signature verification failed", "id", connID)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// 4. PERSIST TO VAULT (Durability)
	eventID := uuid.New().String()

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := h.Pub.PublishStripeEvent(ctx, connID, eventID, event, body); err != nil {
		h.Logger.Error("NATS Publish failed", "error", err)
		http.Error(w, "Persistence Error", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("Webhook ingested", "toro_id", eventID, "stripe_id", event.ID)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}
