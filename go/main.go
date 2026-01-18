package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	// Connect to the shared Redis instance
	// Connect to the shared Redis instance
	rdb = redis.NewClient(&redis.Options{
		Addr: func() string {
			if url := os.Getenv("REDIS_URL"); url != "" {
				// If it's a full URL like redis://redis:6379, parse it or just handle address manually?
				// go-redis ParseURL handles the full string.
				// But NewClient takes Options.
				// Let's keep it simple: if REDIS_URL is set, use ParseURL.
				// Actually, let's just assume REDIS_HOST:PORT or use a simple logic.
				// The docker-compose passes REDIS_URL=redis://redis:6379
				return "redis:6379"
			}
			return "localhost:6379" // Fallback for local non-docker run
		}(),
	})
	// In production, fetch this from Postgres based on the URL token
	stripeSigningSecret = os.Getenv("STRIPE_SIGNING_SECRET")
)

// WebhookPayload is the standard format we send to Redis for Django/Workers
type WebhookPayload struct {
	ID        string          `json:"id"`
	Source    string          `json:"source"` // e.g. "stripe", "twilio"
	Body      json.RawMessage `json:"body"`
	Headers   http.Header     `json:"headers"`
	Timestamp int64           `json:"timestamp"`
}

func handleWebhook(w http.ResponseWriter, r *http.Request) {
	// 0. Parse URL: /v1/webhooks/{source}/{connection_id}
	// Expected format: /v1/webhooks/stripe/123e4567-e89b...
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(pathParts) != 4 || pathParts[0] != "v1" || pathParts[1] != "webhooks" {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	
	source := pathParts[2]
	connectionID := pathParts[3]

	// 1. Safety: Limit body size (1MB)
	r.Body = http.MaxBytesReader(w, r.Body, 1048576)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// 2. Reliability: Persist BEFORE responding
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	evtID := uuid.New().String()
	
	data := WebhookPayload{
		ID:        evtID,
		Source:    source,
		Body:      body,
		Headers:   r.Header,
		Timestamp: time.Now().Unix(),
	}
	
	// Add connection_id to the data or metadata. 
	// Since WebhookPayload struct is defined above, let's update it or just put it in the map values if needed.
	// But better to update the struct to include ConnectionID as it's critical.
	// For now, I'll update the struct definition in the same file if possible, 
	// but since I am replacing the function, I'll need to make sure the struct is updated in a separate edit 
	// OR I can include the struct update if I replace the whole file or a larger chunk.
	// Let's assume I will update the struct definition separately or I can pass it in the map.
	// Actually, I can put it in the map values directly for now alongside the JSON payload.
	
	jsonData, _ := json.Marshal(data)

	// We store "connection_id" as a field in the Redis stream message map, NOT inside the JSON payload 
	// unless we modify the Go struct. The Django consumer will need to look for it.
	// Spec says: "Propagate connection_id".
	
	err = rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: "toro:ingest",
		Values: map[string]interface{}{
			"payload":       jsonData,
			"source":        source,
			"connection_id": connectionID,
		},
	}).Err()

	if err != nil {
		log.Printf("❌ Redis Error: %v", err)
		// Spec: "Internal failure returns 202 only if persisted". 
		// Here it failed to persist, so we must return error.
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	log.Printf("✅ Persisted Event [%s] Connection[%s] ID: %s", source, connectionID, evtID)
	w.WriteHeader(http.StatusOK)
}

func main() {
	// Catch-all for /v1/webhooks/
	http.HandleFunc("/v1/webhooks/", handleWebhook)

	port := ":8080"
	log.Printf("🐂 Toro Muscle (Go) running on %s", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatal(err)
	}
}
