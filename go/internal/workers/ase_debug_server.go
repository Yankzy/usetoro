package workers

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

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

		// Send updates every 500ms
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				if w.dag != nil {
					stats := w.dag.Stats()
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
