package workers

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all for local debugging
	},
}

// startDebugServer starts a local HTTP and WebSocket server for real-time visualization.
func (w *AseBridgeWorker) startDebugServer() {
	mux := http.NewServeMux()

	// Serve the visualizer HTML page
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat("go/internal/erp/ase/debug/index.html"); err == nil {
			http.ServeFile(w, r, "go/internal/erp/ase/debug/index.html")
		} else {
			http.ServeFile(w, r, "internal/erp/ase/debug/index.html")
		}
	})

	// Handle WebSocket connections
	mux.HandleFunc("/ws/dag", func(rw http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(rw, r, nil)
		if err != nil {
			w.logger.Error("debug_server: failed to upgrade connection", "error", err)
			return
		}
		defer conn.Close()

		w.logger.Info("debug_server: client connected to /ws/dag")

		// Read incoming messages from client
		go func() {
			for {
				_, msgBytes, err := conn.ReadMessage()
				if err != nil {
					return
				}
				
				var req struct {
					Type    string `json:"type"`
					NodeID  string `json:"node_id"`
					Message string `json:"message"`
				}
				if err := json.Unmarshal(msgBytes, &req); err == nil && req.Type == "chat" {
					w.dagsMu.RLock()
					defaultDag := w.dags["default"]
					w.dagsMu.RUnlock()
					
					if defaultDag != nil {
						if node, ok := defaultDag.Nodes[req.NodeID]; ok {
							if agent := node.PopHoldingAgent(); agent != nil {
								// Set the manual chat text as context override reason
								agent.AppendExecutionStep(ase.NodeExecutionStep{
									DAGNodeID:   node.ID,
									Kind:        string(node.Kind),
									PropertyKey: node.PromptKey,
									Timestamp:   time.Now().UTC(),
								})
								
								// We put the chat message in context updates
								agent.AppendContextUpdate("User override/clarification via Debug UI: " + req.Message)
								
								agent.ApproveAndResume(r.Context(), defaultDag, w.store, req.NodeID)
								w.logger.Info("debug_server: resumed holding agent", "node_id", req.NodeID, "agent_id", agent.NodeID)
							}
						}
					}
				}
			}
		}()

		// Send updates every 500ms
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				w.dagsMu.RLock()
				defaultDag := w.dags["default"]
				w.dagsMu.RUnlock()
				
				if defaultDag != nil {
					stats := defaultDag.Stats()
					if stats != nil {
						msg, err := json.Marshal(stats)
						if err != nil {
							continue
						}
						if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
							w.logger.Info("debug_server: client disconnected")
							return
						}
					}
				}
			}
		}
	})

	// Start the server in the background
	go func() {
		w.logger.Info("🛠️  ASE Debug Server running on http://localhost:8084")
		if err := http.ListenAndServe(":8084", mux); err != nil {
			w.logger.Error("debug_server: failed to start", "error", err)
		}
	}()
}
