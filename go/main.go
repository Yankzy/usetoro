package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dgraph-io/ristretto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/stripe/stripe-go/v76/webhook"
)

// Global infrastructure (initialized once)
var (
	nc           *nats.Conn            // NATS connection
	js           nats.JetStreamContext // JetStream context
	secretsCache *ristretto.Cache      // In-memory secrets cache
	dbPool       *pgxpool.Pool         // PostgreSQL connection pool
)

func init() {
	log.Println("🚀 Initializing Toro Ingress Service...")

	// 1. Initialize NATS JetStream
	initNATS()

	// 2. Initialize Ristretto Cache (100MB, 5min TTL)
	initCache()

	// 3. Initialize PostgreSQL Connection Pool
	initDatabase()

	log.Println("✅ Toro Ingress Service ready")
}

func initNATS() {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}

	var err error
	nc, err = nats.Connect(natsURL,
		nats.Name("toro-ingress"),
		nats.ReconnectWait(2*time.Second),
		nats.MaxReconnects(-1), // Infinite reconnects
	)
	if err != nil {
		log.Fatalf("❌ Failed to connect to NATS: %v", err)
	}

	js, err = nc.JetStream()
	if err != nil {
		log.Fatalf("❌ Failed to create JetStream context: %v", err)
	}

	log.Println("✅ Connected to NATS JetStream")
}

func initCache() {
	var err error
	secretsCache, err = ristretto.NewCache(&ristretto.Config{
		NumCounters: 1_000_000, // 1M counters for frequency tracking
		MaxCost:     100 << 20, // 100MB max cache size
		BufferItems: 64,
	})
	if err != nil {
		log.Fatalf("❌ Failed to create cache: %v", err)
	}

	log.Println("✅ Initialized Ristretto cache (100MB)")
}

func initDatabase() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://toro:toro_password@db:5432/toro?sslmode=disable"
	}

	config, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		log.Fatalf("❌ Failed to parse DATABASE_URL: %v", err)
	}

	// Configure connection pool for high concurrency
	config.MaxConns = 25
	config.MinConns = 5
	config.MaxConnLifetime = 5 * time.Minute
	config.MaxConnIdleTime = 1 * time.Minute

	dbPool, err = pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		log.Fatalf("❌ Failed to create connection pool: %v", err)
	}

	// Verify connection
	if err := dbPool.Ping(context.Background()); err != nil {
		log.Fatalf("❌ Failed to ping database: %v", err)
	}

	log.Printf("✅ Connected to PostgreSQL (pool: %d-%d conns)", config.MinConns, config.MaxConns)
}

// getWebhookSecret retrieves webhook secret using cache-aside pattern
// This is the HOT PATH - optimized for zero DB hits on cached secrets
func getWebhookSecret(connID string) (string, error) {
	cacheKey := fmt.Sprintf("secret:%s", connID)

	// STEP 1: Check L1 Cache (SPEED - no DB hit!)
	if val, found := secretsCache.Get(cacheKey); found {
		return val.(string), nil
	}

	// STEP 2: Cache miss - fetch from database (COLD PATH)
	log.Printf("🔍 Cache miss for connection: %s - querying DB", connID)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var secret string
	query := `
		SELECT webhook_secret 
		FROM webhookks_providerconnection 
		WHERE connection_id = $1 AND is_active = true
	`

	err := dbPool.QueryRow(ctx, query, connID).Scan(&secret)
	if err != nil {
		return "", fmt.Errorf("invalid connection or DB error: %w", err)
	}

	// STEP 3: Cache fill (TTL: 5 minutes)
	secretsCache.SetWithTTL(cacheKey, secret, 1, 5*time.Minute)
	log.Printf("💾 Cached secret for connection: %s", connID)

	return secret, nil
}

// handleStripeWebhook processes incoming Stripe webhooks
// URL: POST /webhooks/stripe/{conn_id}
func handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	// Extract connection ID from URL path
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(pathParts) != 3 || pathParts[0] != "webhooks" || pathParts[1] != "stripe" {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	connID := pathParts[2]

	// 1. LOOKUP: Get webhook secret (cache-aside pattern)
	secret, err := getWebhookSecret(connID)
	if err != nil {
		log.Printf("❌ Invalid connection: %s - %v", connID, err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// 2. READ BODY: Buffered read (required for signature verification)
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("❌ Failed to read body: %v", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// 3. VERIFY: Stripe signature verification
	signature := r.Header.Get("Stripe-Signature")
	if signature == "" {
		log.Printf("❌ Missing Stripe-Signature header for connection: %s", connID)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Use Stripe's official webhook verification
	event, err := webhook.ConstructEvent(body, signature, secret)
	if err != nil {
		log.Printf("❌ Invalid signature for connection: %s - %v", connID, err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// 4. PERSIST TO THE VAULT (NATS JetStream)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventID := uuid.New().String()
	subject := "raw.ingest.stripe"

	msg := nats.NewMsg(subject)
	msg.Data = body // Store raw Stripe payload
	msg.Header.Set("Toro-Conn-ID", connID)
	msg.Header.Set("Toro-Event-ID", eventID)
	msg.Header.Set("Stripe-Event-Type", string(event.Type))
	msg.Header.Set("Stripe-Event-ID", event.ID)
	msg.Header.Set("Timestamp", fmt.Sprintf("%d", time.Now().Unix()))

	_, err = js.PublishMsg(msg, nats.Context(ctx))
	if err != nil {
		log.Printf("❌ NATS Publish Error [stripe]: %v", err)
		// Return 500 to force Stripe retry
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	log.Printf(
		"✅ Stripe webhook persisted | conn=%s | type=%s | stripe_id=%s | toro_id=%s",
		connID, event.Type, event.ID, eventID,
	)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// healthCheck provides a simple health endpoint
func healthCheck(w http.ResponseWriter, r *http.Request) {
	// Check NATS connection
	if nc.Status() != nats.CONNECTED {
		http.Error(w, "NATS disconnected", http.StatusServiceUnavailable)
		return
	}

	// Check DB connection
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := dbPool.Ping(ctx); err != nil {
		http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"healthy","service":"toro-ingress"}`))
}

func main() {
	// Register handlers
	http.HandleFunc("/webhooks/stripe/", handleStripeWebhook)
	http.HandleFunc("/health", healthCheck)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	addr := ":" + port
	log.Printf("🐂 Toro Ingress running on %s", addr)
	log.Printf("📊 Cache: 100MB | DB Pool: 5-25 conns | TTL: 5min")
	log.Printf("🔗 Endpoints: POST /webhooks/stripe/{conn_id}")

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("❌ Server failed: %v", err)
	}
}
