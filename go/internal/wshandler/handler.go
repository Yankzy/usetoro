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
	pathPrefix := "/api"
	if idx := strings.Index(r.URL.Path, "/ws"); idx != -1 && idx > 0 {
		pathPrefix = r.URL.Path[:idx]
	}

	client := NewClient(h.hub, conn, h.logger, h.messageHandler, host, scheme, pathPrefix, r.Context())

	// ── Room membership at startup ─────────────────────────────────────────
	//
	// A client belongs to two broadcast rooms after connect:
	//
	// 1. entity_id room — joined via hub.register above.
	//    This is the auth entity UUID (e.g. 84644747-...). Used as a fallback
	//    when workflow events lack a realm_id.
	//
	// 2. realm_id room — joined via joinRealmRoom below.
	//    This is the QBO realm ID (e.g. 9341456276406470), resolved from the
	//    erp_connections table. This is the PRIMARY room for real-time workflow
	//    events — the WorkflowEventConsumer targets realm_id first when
	//    broadcasting status updates.
	//
	// The realm_id room join MUST happen at connect time (not deferred until
	// subscribe_cards) so the frontend receives real-time events immediately.
	// blastActiveWorkflows sends the initial snapshot directly over the
	// client's send channel, so it works regardless of room membership.
	// ────────────────────────────────────────────────────────────────────────
	h.hub.register <- client
	go client.joinRealmRoom()
	go client.writePump()
	go client.blastActiveWorkflows()

	// Block the handler with readPump. When the websocket closes, this returns,
	// and the HTTP server will automatically cancel r.Context().
	client.readPump()
}
