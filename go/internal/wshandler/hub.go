package wshandler

import (
	"log/slog"
	"sync"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/Yankzy/usetoro/internal/services/ai"
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

	// Logger
	logger *slog.Logger

	// JetStream access
	queueClient *queue.Client

	// Database access
	db *database.Queries

	// OpenAI Client
	llm *ai.LLMClient

	// Mutex for thread-safe operations
	mu sync.RWMutex
}

type broadcastMessage struct {
	roomID string // empty for global
	data   []byte
}

// NewHub creates a new Hub instance
func NewHub(logger *slog.Logger, queueClient *queue.Client, db *database.Queries, llm *ai.LLMClient) *Hub {
	return &Hub{
		broadcast:   make(chan broadcastMessage, 256),
		register:    make(chan *Client),
		unregister:  make(chan *Client),
		rooms:       make(map[string]map[*Client]bool),
		logger:      logger,
		queueClient: queueClient,
		db:          db,
		llm:         llm,
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
			h.mu.Unlock()
			h.logger.Info("Client registered", "room", client.entityID, "total_clients", h.ClientCount())

		case client := <-h.unregister:
			h.mu.Lock()
			if room, ok := h.rooms[client.entityID]; ok {
				if _, exists := room[client]; exists {
					delete(room, client)
					close(client.send)
					if len(room) == 0 {
						delete(h.rooms, client.entityID)
					}
				}
			}
			h.mu.Unlock()
			h.logger.Info("Client unregistered", "room", client.entityID, "total_clients", h.ClientCount())

		case bm := <-h.broadcast:
			h.mu.RLock()
			if bm.roomID != "" {
				// Targeted broadcast to a specific room (entity_id)
				if room, ok := h.rooms[bm.roomID]; ok {
					for client := range room {
						h.sendToClient(client, bm.data)
					}
				}
			} else {
				// Global broadcast to everyone
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
	default:
		close(client.send)
		h.mu.Lock()
		delete(h.rooms[client.entityID], client)
		if len(h.rooms[client.entityID]) == 0 {
			delete(h.rooms, client.entityID)
		}
		h.mu.Unlock()
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

// ClientCount returns the number of connected clients
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	count := 0
	for _, room := range h.rooms {
		count += len(room)
	}
	return count
}
