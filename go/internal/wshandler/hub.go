package wshandler

import (
	"log/slog"
	"sync"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// Hub maintains the set of active clients and broadcasts messages to the clients.
type Hub struct {
	// Rooms: map[entity_id] -> map[*Client]bool
	rooms map[string]map[*Client]bool

	// Inbound messages from the clients.
	broadcast chan broadcastMessage

	// Register requests from the clients.
	register chan *Client

	// Unregister requests from clients.
	unregister chan *Client

	// Active clients tracking to prevent double-close
	clients map[*Client]bool

	// JoinRoom requests from clients.
	joinRoom chan joinRoomRequest

	// Logger
	logger *slog.Logger

	// JetStream access
	queueClient *queue.Client

	// Database access
	db *database.Queries

	rt *agent.Runtime

	// Mutex for thread-safe operations
	mu sync.RWMutex
}

type broadcastMessage struct {
	roomID string // empty for global
	data   []byte
}

type joinRoomRequest struct {
	client *Client
	roomID string
}

// NewHub creates a new Hub instance
func NewHub(logger *slog.Logger, queueClient *queue.Client, db *database.Queries) *Hub {
	adapter := agent.NewNatsAdapter(queueClient.Conn(), queueClient.JetStream())
	rt := agent.NewRuntime(logger, adapter, core.AgentConfig{
		Model: "gpt-4o",
		DID:   "did:toro:wshandler:hub",
	})
	return &Hub{
		broadcast:   make(chan broadcastMessage, 256),
		register:    make(chan *Client, 256),
		unregister:  make(chan *Client, 256),
		joinRoom:    make(chan joinRoomRequest, 256),
		rooms:       make(map[string]map[*Client]bool),
		clients:     make(map[*Client]bool),
		logger:      logger,
		queueClient: queueClient,
		db:          db,
		rt:          rt,
	}
}

// Run starts the hub's main loop
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			if h.rooms[client.entityID] == nil {
				h.rooms[client.entityID] = make(map[*Client]bool)
			}
			h.rooms[client.entityID][client] = true
			h.clients[client] = true
			h.mu.Unlock()
			h.logger.Info("Client registered", "room", client.entityID, "total_clients", h.ClientCount())

		case client := <-h.unregister:
			h.mu.Lock()
			if !h.clients[client] {
				h.mu.Unlock()
				continue
			}
			delete(h.clients, client)

			// Unregister from all rooms this client might belong to
			for roomID, room := range h.rooms {
				if _, exists := room[client]; exists {
					delete(room, client)
					if len(room) == 0 {
						delete(h.rooms, roomID)
					}
				}
			}
			close(client.send)
			h.mu.Unlock()
			h.logger.Info("Client unregistered from all rooms", "entity_id", client.entityID, "total_clients", h.ClientCount())

		case req := <-h.joinRoom:
			h.mu.Lock()
			if h.rooms[req.roomID] == nil {
				h.rooms[req.roomID] = make(map[*Client]bool)
			}
			h.rooms[req.roomID][req.client] = true
			h.mu.Unlock()
			h.logger.Info("Client joined additional room", "room", req.roomID, "entity_id", req.client.entityID)

		case bm := <-h.broadcast:
			h.mu.RLock()
			if bm.roomID != "" {
				// Targeted broadcast to a specific room (entity_id)
				if room, ok := h.rooms[bm.roomID]; ok {
					h.logger.Info("📡 Hub: broadcasting to room", "room", bm.roomID, "clients", len(room))
					for client := range room {
						h.sendToClient(client, bm.data)
					}
				} else {
					h.logger.Warn("📡 Hub: room not found for broadcast", "room", bm.roomID)
				}
			} else {
				// Global broadcast to everyone
				h.logger.Info("📡 Hub: global broadcast", "rooms", len(h.rooms))
				for _, room := range h.rooms {
					for client := range room {
						h.sendToClient(client, bm.data)
					}
				}
			}
			h.mu.RUnlock()
		}
	}
}

func (h *Hub) sendToClient(client *Client, data []byte) {
	select {
	case client.send <- data:
		h.logger.Debug("📡 Hub: sent to client", "entity_id", client.entityID)
	default:
		h.logger.Warn("📡 Hub: client send buffer full, unregistering", "entity_id", client.entityID)
		select {
		case h.unregister <- client:
		default:
		}
	}
}

// Broadcast sends a message to all connected clients
func (h *Hub) Broadcast(message []byte) {
	h.broadcast <- broadcastMessage{data: message}
}

// BroadcastToRoom sends a message only to clients in a specific room (entity_id).
func (h *Hub) BroadcastToRoom(roomID string, message []byte) {
	h.broadcast <- broadcastMessage{roomID: roomID, data: message}
}

// JoinRoom allows a client to join an additional broadcast room (e.g. realm_id)
func (h *Hub) JoinRoom(client *Client, roomID string) {
	h.joinRoom <- joinRoomRequest{client: client, roomID: roomID}
}

// ClientCount returns the number of connected clients
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
