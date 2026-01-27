package api

import "net/http"

// NewRouter sets up the HTTP routes for the application.
func NewRouter(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.Check) // Legacy
	mux.HandleFunc("GET /health/live", h.Liveness)
	mux.HandleFunc("GET /health/ready", h.Readiness)
	mux.HandleFunc("POST /webhooks/stripe/{conn_id}", h.HandleStripeWebhook)
	return mux
}
