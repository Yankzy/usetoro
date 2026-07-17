package mailpool

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

// Handler handles Mailpool API requests from the frontend by securely proxying them.
// By using a reverse proxy, we instantly and safely expose all 35+ Mailpool endpoints
// without needing to maintain thousands of lines of boilerplate wrapping code.
type Handler struct {
	Client *Mailpool
	APIKey string
}

// NewHandler creates a new Mailpool handler.
func NewHandler(client *Mailpool, apiKey string) *Handler {
	return &Handler{
		Client: client,
		APIKey: apiKey,
	}
}

// Mount attaches the Mailpool reverse proxy to the provided ServeMux.
func (h *Handler) Mount(mux *http.ServeMux, authMiddleware func(http.Handler) http.Handler) {
	// The target URL of the Mailpool API
	target, _ := url.Parse("https://app.mailpool.io/v1/api")

	proxy := httputil.NewSingleHostReverseProxy(target)

	// Intercept the request to inject our secret API key and clean up the path
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
		req.Header.Set("X-Api-Authorization", h.APIKey)
	}

	// Any request to /api/v1/mailpool/* gets forwarded to https://app.mailpool.io/v1/api/*
	proxyHandler := http.StripPrefix("/api/v1/mailpool", proxy)

	// We apply our Toro authentication middleware so only logged-in users can use this proxy
	mux.Handle("/api/v1/mailpool/", authMiddleware(proxyHandler))
}
