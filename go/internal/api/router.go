package api

import "net/http"

// NewRouter sets up the HTTP routes for the application.
func NewRouter(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.Check)
	mux.HandleFunc("POST /webhooks/stripe/{conn_id}", h.HandleStripeWebhook)
	return mux
}
