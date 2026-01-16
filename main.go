package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stripe/stripe-go/v74/webhook"
)

var (
	// Connect to the shared Redis instance
	rdb = redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
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

func handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	// 1. Safety: Limit body size (1MB)
	r.Body = http.MaxBytesReader(w, r.Body, 1048576)

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// 2. Security: Verify Stripe Signature
	// This happens in Go (fast) before we even bother Django
	event, err := webhook.ConstructEvent(payload, r.Header.Get("Stripe-Signature"), stripeSigningSecret)
	if err != nil {
		fmt.Printf("⚠️  Invalid Signature: %v\n", err)
		http.Error(w, "Invalid Signature", http.StatusBadRequest)
		return
	}

	// 3. Speed: Spawn a goroutine to queue the data
	// We return 200 OK immediately so Stripe doesn't timeout
	go func(evtID string, body []byte, headers http.Header) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		data := WebhookPayload{
			ID:        evtID,
			Source:    "stripe",
			Body:      body,
			Headers:   headers,
			Timestamp: time.Now().Unix(),
		}

		jsonData, _ := json.Marshal(data)

		// Push to Redis Stream "toro:ingest"
		// Django Celery or Go Workers will consume this
		err := rdb.XAdd(ctx, &redis.XAddArgs{
			Stream: "toro:ingest",
			Values: map[string]interface{}{"payload": jsonData},
		}).Err()

		if err != nil {
			log.Printf("❌ Redis Error: %v", err)
		} else {
			log.Printf("✅ Queued Event: %s", evtID)
		}
	}(event.ID, payload, r.Header)

	w.WriteHeader(http.StatusOK)
}

func main() {
	http.HandleFunc("/hooks/stripe", handleStripeWebhook)
	
	port := ":8080"
	log.Printf("🐂 Toro Muscle (Go) running on %s", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatal(err)
	}
}
