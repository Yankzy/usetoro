Toro Technical Architecture: The Hybrid "Muscle & Brain" Model
1. High-Level Strategy
We do not try to embed Go inside Python. Instead, we run them as parallel services that share a Database (Postgres) and communicate via a Message Queue (Redis).
The Muscle (Go): Sits at the edge. Receives high-volume webhooks, verifies signatures, and pushes them into a queue. It manages the WebSocket tunnels because Go allows 100k+ concurrent connections cheaply.
The Brain (Django): Manages users, organization settings, and the complex "AI Analysis."
The Glue (Redis): Go puts data in; Python/Go workers take data out.
The Traffic Flow
Ingest: Stripe Webhook -> Go Service (api.usetoro.io).
Verify & Queue: Go verifies signature -> Pushes JSON to Redis Stream.
Process:
Path A (Fast Forward): Go Worker reads Redis -> Forwards to user's destination (or WebSocket).
Path B (AI Analysis): If Path A fails (500 Error) -> Go pushes payload to "Analysis Queue" -> Django/Celery Worker picks it up -> Runs LLM -> Saves insight to Postgres.
2. The Muscle: Receiving Stripe Webhooks in Go
This is the code that needs to be fast. It handles the POST request, checks security, and spawns a background routine immediately so we can return 200 OK to Stripe within milliseconds.
Go Implementation Guide
Prerequisites:
Library: github.com/stripe/stripe-go/v74 (for signature verification)
Library: github.com/redis/go-redis/v9
ingest_service.go (Simplified)
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

	"[github.com/redis/go-redis/v9](https://github.com/redis/go-redis/v9)"
	"[github.com/stripe/stripe-go/v74/webhook](https://github.com/stripe/stripe-go/v74/webhook)"
)

var (
	// Initialize Redis (The Glue)
	rdb = redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	// Stripe Signing Secret (Ideally fetched from Postgres per user, but env for now)
	stripeSigningSecret = os.Getenv("STRIPE_SIGNING_SECRET")
)

// WebhookPayload represents the data we push to Redis
type WebhookPayload struct {
	ID        string          `json:"id"`
	Source    string          `json:"source"`
	Body      json.RawMessage `json:"body"`
	Headers   http.Header     `json:"headers"`
	Timestamp int64           `json:"timestamp"`
}

func handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	// 1. Limit body size to prevent memory attacks (Max 1MB)
	const MaxBodyBytes = 1048576
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)

	// 2. Read the body
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusBadRequest)
		return
	}

	// 3. Verify Stripe Signature (CRITICAL SECURITY STEP)
	// This ensures the request actually came from Stripe, not a hacker.
	endpointSecret := stripeSigningSecret
	headerSig := r.Header.Get("Stripe-Signature")

	// Pass the payload and header to Stripe's library
	event, err := webhook.ConstructEvent(payload, headerSig, endpointSecret)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error verifying webhook signature: %v\n", err)
		http.Error(w, "Invalid Signature", http.StatusBadRequest)
		return
	}

	// 4. The "Spawn" - Handoff to a Goroutine
	// We respond 200 OK immediately, but process in the background.
	go processWebhook(event.ID, payload, r.Header)

	w.WriteHeader(http.StatusOK)
}

// processWebhook is the "Routine" that runs asynchronously
func processWebhook(eventID string, body []byte, headers http.Header) {
	ctx := context.Background()

	// Step A: Package the data
	data := WebhookPayload{
		ID:        eventID,
		Source:    "stripe",
		Body:      body,
		Headers:   headers,
		Timestamp: time.Now().Unix(),
	}

	jsonData, _ := json.Marshal(data)

	// Step B: Push to Redis Stream (The "Inbox" for our workers)
	// Django or Go workers can listen to "toro:webhooks:stream"
	err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: "toro:webhooks:stream",
		Values: map[string]interface{}{
			"payload": jsonData,
		},
	}).Err()

	if err != nil {
		log.Printf("Failed to enqueue webhook %s: %v", eventID, err)
		return
	}

	log.Printf("Successfully queued Stripe event: %s", eventID)
}

func main() {
	http.HandleFunc("/hooks/stripe", handleStripeWebhook)
	log.Println("Go Muscle running on :8080...")
	log.Fatal(http.ListenAndServe(":8080", nil))
}



Why this is fast:
Zero Processing on Main Thread: We verify the signature and immediately spawn go processWebhook. The HTTP connection closes instantly.
Redis Stream: We don't write to Postgres yet (which is slow). We write to Redis (RAM), which is blazing fast.
3. The Bridge: Connecting to Django (The Brain)
Now that Go has dumped the raw event into Redis, how does Django get involved?
You don't need Go to call Django via HTTP (that's slow). Instead, Django (via Celery) listens to the same Redis instance.
Scenario: The "AI Analysis" Trigger
Let's say the Go Forwarder tries to send the webhook to the user's localhost, and it fails with a 500 Error.
Go Logic (Pseudocode):
// Inside the Go Forwarder Worker
resp, err := http.Post(userUrl, "application/json", payload)
if resp.StatusCode == 500 {
    // UH OH! The user's app crashed.
    // Send this specific failure to the "AI Analysis Queue"
    rdb.LPush(ctx, "toro:analysis:queue", payload)
}



Django Logic (Celery Task):
This is where your Python muscle shines. You use celery to consume that queue.
# Django App: analysis/tasks.py
from celery import shared_task
from .ai_engine import analyze_crash # Your LangChain/OpenAI logic

@shared_task
def analyze_webhook_failure(payload_json):
    """
    This runs in a Python worker. It has access to all Django models.
    """
    # 1. Parse data
    event = json.loads(payload_json)
    
    # 2. Run AI Analysis (The "Brain")
    # "Why did this Stripe object crash the user's endpoint?"
    insight = analyze_crash(event['body'], event['error_response'])
    
    # 3. Save to Postgres (Django ORM)
    # Go reads from this same DB to show the result on the dashboard
    WebhookLog.objects.create(
        source="stripe",
        payload=event['body'],
        ai_insight=insight,  # <--- The magic value
        status="failed"
    )



4. Shared Database Strategy
Since Go needs to know where to forward webhooks (e.g., "User A has a tunnel open on port 3000"), and Django needs to manage those users, they must share a database.
The Golden Rule:
Django owns the schema (Models, Migrations).
Go treats the database as "Read-Only" mostly, or uses simple raw SQL for high-volume inserts.
Workflow:
User signs up on Django Dashboard. Django creates User and ApiKey in Postgres.
User starts CLI: toro listen.
Go accepts the connection. Go queries Postgres: SELECT * FROM api_keys WHERE key = ?.
Tip: Cache this in Redis so Go doesn't hit Postgres for every single webhook.
5. Summary of Responsibilities
| Feature | Technology | Why? |
| Public API (/hooks/*) | Go | Needs to handle 10k req/sec with low RAM. |
| CLI Tunnel Server | Go | WebSockets in Go are far more efficient than Python/Channels. |
| Forwarding Worker | Go | Raw HTTP concurrency. |
| Web Dashboard | Django | Rapid development, Admin panel, ORM. |
| AI Processing | Django (Celery) | Access to Python AI ecosystem (LangChain, HuggingFace). |
| Database | Postgres | Shared source of truth. |
| Queue | Redis | The common language between Go and Python. |
This architecture ensures you hit your performance goals without sacrificing the developer experience of building the "Brain" in Python.
