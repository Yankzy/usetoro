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
	// 1. Safety: Limit body size (1MB)
	r.Body = http.MaxBytesReader(w, r.Body, 1048576)

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Extract source from URL path (e.g. /hooks/stripe -> source=stripe)
	// Simple parsing for now
	pathParts := strings.Split(r.URL.Path, "/")
	source := "unknown"
	if len(pathParts) > 2 {
		source = pathParts[2]
	}

	// 2. Speed: Spawn a goroutine to queue the data immediately
	go func(evtID string, src string, body []byte, headers http.Header) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		data := WebhookPayload{
			ID:        evtID,
			Source:    src,
			Body:      body,
			Headers:   headers,
			Timestamp: time.Now().Unix(),
		}

		jsonData, _ := json.Marshal(data)

		err := rdb.XAdd(ctx, &redis.XAddArgs{
			Stream: "toro:ingest",
			Values: map[string]interface{}{"payload": jsonData},
		}).Err()

		if err != nil {
			log.Printf("❌ Redis Error: %v", err)
		} else {
			log.Printf("✅ Queued Event [%s]: %s", src, evtID)
		}
	}(uuid.New().String(), source, payload, r.Header)

	w.WriteHeader(http.StatusOK)
}

func main() {
	// Catch-all for /hooks/
	http.HandleFunc("/hooks/", handleWebhook)

	port := ":8080"
	log.Printf("🐂 Toro Muscle (Go) running on %s", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatal(err)
	}
}
