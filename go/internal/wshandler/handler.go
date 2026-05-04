package wshandler

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Allow all origins for development
		// In production, you should check the origin
		return true
	},
}

// Handler handles WebSocket upgrade requests
type Handler struct {
	hub            *Hub
	logger         *slog.Logger
	messageHandler *MessageHandler
}

// NewHandler creates a new WebSocket handler
func NewHandler(hub *Hub, logger *slog.Logger, messageHandler *MessageHandler) *Handler {
	return &Handler{
		hub:            hub,
		logger:         logger,
		messageHandler: messageHandler,
	}
}

// ServeWS handles websocket requests from the peer.
func (h *Handler) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("Failed to upgrade connection", "error", err)
		return
	}

	// Extract connection details for dynamic redirects
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}

	scheme := "https"
	if forwardedProto := r.Header.Get("X-Forwarded-Proto"); forwardedProto != "" {
		scheme = forwardedProto
	} else if r.TLS == nil {
		if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1") {
			scheme = "http"
		}
	}

	// Detect path prefix (e.g. /api)
	pathPrefix := ""
	if idx := strings.Index(r.URL.Path, "/ws"); idx != -1 {
		pathPrefix = r.URL.Path[:idx]
	}

	client := NewClient(h.hub, conn, h.logger, h.messageHandler, host, scheme, pathPrefix, r.Context())
	h.hub.register <- client

	// Start the client's write and active workflows pumps in background
	go client.writePump()
	go client.blastActiveWorkflows()

	// Block the handler with readPump. When the websocket closes, this returns,
	// and the HTTP server will automatically cancel r.Context().
	client.readPump()
}
