package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/dgraph-io/ristretto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/stripe/stripe-go/v76/webhook"
)

// Config holds all configuration variables from the environment.
// GO CONCEPT: Centralized Config
// Instead of calling os.Getenv() all over the code, we load it once into a struct.
// Benefit: We can validation all inputs at startup (Fail Fast) and pass 'cfg' around cleanly.
type Config struct {
	Port        string
	DatabaseURL string
	NatsURL     string
}

// App holds the application state and dependencies.
//
// GO CONCEPT: Dependency Injection
// Global variables are the enemy of testing. By putting our dependencies (DB, Logger, NATS)
// into a struct, we can create an 'App' with mock dependencies for unit tests.
// Example: In a test, we can pass a 'MockDB' that returns fake errors to see if the handler handles them.
type App struct {
	Logger  *slog.Logger          // Structured logger for JSON logs
	DB      *pgxpool.Pool         // Postgres connection pool (pgx is the modern driver)
	Queries *database.Queries     // SQLC Queries (Type-safe SQL)
	NATS    nats.JetStreamContext // NATS JetStream (The "Write-Ahead Log" for our events)
	Cache   *ristretto.Cache      // in-memory cache for secrets (The "Hot Path" optimization)
}

func main() {
	// 1. Setup Structured Logging (JSON)
	// GO CONCEPT: Structured Logging (slog)
	// Standard 'log.Println' prints text. 'slog' prints JSON.
	// JSON is machine-readable. It allows tools like Datadog/Splunk/CloudWatch to
	// index fields like "error", "conn_id", or "duration" for searching and graphing.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 2. Load and Validate Configuration
	cfg := Config{
		Port:        getEnv("PORT", "8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		NatsURL:     getEnv("NATS_URL", nats.DefaultURL),
	}

	// GO CONCEPT: Fail Fast
	// It is better to crash immediately at startup than to run for hours and
	// fail when a user tries to do something.
	if cfg.DatabaseURL == "" {
		logger.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	// 3. Start the Application
	// We delegate the "real" work to a 'run' function.
	// This keeps main() tiny and allows 'run' to return errors (which act as exit codes).
	if err := run(cfg, logger); err != nil {
		logger.Error("Fatal startup error", "error", err)
		os.Exit(1)
	}
}

// run encapsulates the startup logic, dependency initialization, and server lifecycle.
func run(cfg Config, logger *slog.Logger) error {
	ctx := context.Background()

	// =========================================================================
	// INFRASTRUCTURE INITIALIZATION
	// =========================================================================

	// 1. PostgreSQL (Metadata Store)
	// GO CONCEPT: Connection Pooling (pgxpool)
	// Opening a TCP connection to Postgres is slow (DNS, Handshake, Auth).
	// A 'Pool' keeps a bunch of open connections ready to go.
	// - MaxConns=25: Never open more than 25 connections (prevents crashing the DB).
	// - MinConns=5: Keep 5 ready even if idle (for burst performance).
	dbConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db config error: %w", err)
	}
	dbConfig.MaxConns = 25
	dbConfig.MinConns = 5

	dbPool, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		return fmt.Errorf("db connection error: %w", err)
	}
	defer dbPool.Close() // GO CONCEPT: defer (Cleanup this resource when 'run' exits)

	// Verify connection immediately
	if err := dbPool.Ping(ctx); err != nil {
		return fmt.Errorf("db ping failed: %w", err)
	}
	logger.Info("✅ Connected to PostgreSQL")

	// 2. NATS JetStream (Durability Layer)
	// NATS is a messaging system. JetStream is its persistence layer (like Kafka).
	// We write webhooks here FIRST. Even if our API crashes, the data is safe in NATS.
	// MaxReconnects(-1) means "Try forever". We never want to give up connecting to our nervous system.
	nc, err := nats.Connect(cfg.NatsURL,
		nats.Name("toro-ingress"),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		return fmt.Errorf("nats connect error: %w", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		return fmt.Errorf("jetstream init error: %w", err)
	}
	logger.Info("✅ Connected to NATS JetStream")

	// 3. Ristretto Cache (L1 Cache)
	// A high-performance local cache.
	// We use this to cache webhook secrets.
	// - NumCounters: Optimization for TinyLFU eviction policy.
	// - MaxCost: 100MB hard limit. It handles eviction automatically.
	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 1e7,
		MaxCost:     100 << 20, // 100 * 1MiB
		BufferItems: 64,
	})
	if err != nil {
		return fmt.Errorf("cache init error: %w", err)
	}
	logger.Info("✅ Initialized Cache")

	// =========================================================================
	// HTTP SERVER SETUP
	// =========================================================================

	app := &App{
		Logger:  logger,
		DB:      dbPool,
		Queries: database.New(dbPool), // Initialize SQLC
		NATS:    js,
		Cache:   cache,
	}

	// GO CONCEPT: ServeMux (Router)
	// As of Go 1.22, the standard library router is very powerful.
	// We can match methods ("POST") and paths.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", app.handleHealth)

	// Pattern "{conn_id}" is a wildcard.
	// URL: /webhooks/stripe/acc_123 -> conn_id = "acc_123"
	mux.HandleFunc("POST /webhooks/stripe/{conn_id}", app.handleStripeWebhook)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: mux,
		// GO CONCEPT: Timeouts (Defense in Depth)
		// "Slowloris" attack: malicious user sends 1 byte every 10 seconds to keep connection open.
		// These timeouts enforce "Speak up or hang up".
		ReadTimeout:  5 * time.Second,   // Time to read the request body
		WriteTimeout: 10 * time.Second,  // Time to write the response
		IdleTimeout:  120 * time.Second, // Time to keep Keep-Alive connections open
	}

	// =========================================================================
	// GRACEFUL SHUTDOWN
	// =========================================================================

	// GO CONCEPT: Goroutines & Channels
	// We start the server in a goroutine (background thread) effectively.
	// If we ran it in the main thread (srv.ListenAndServe), the code would BLOCK there forever.
	// We need main to continue so it can listen for shutdown signals.
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("🐂 Toro Ingress started", "port", cfg.Port)
		serverErrors <- srv.ListenAndServe()
	}()

	// GO CONCEPT: Signal Handling
	// When you hit CTRL+C or Kubernetes kills a pod, it sends a SIGTERM.
	// We catch this signal to clean up properly instead of vanishing instantly.
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	// Block here until either:
	// 1. The server crashes (serverErrors)
	// 2. We get a shutdown signal (shutdown)
	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)

	case sig := <-shutdown:
		logger.Info("Shutdown signal received", "signal", sig)

		// GO CONCEPT: Context Cancellation
		// We create a context with a timeout. This tells the server:
		// "You have 10 seconds to finish currently running requests, then DIE."
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel() // Always call cancel to release context resources

		if err := srv.Shutdown(ctx); err != nil {
			// If it takes longer than 10s, we force close.
			srv.Close()
			return fmt.Errorf("could not stop server gracefully: %w", err)
		}
	}

	return nil
}

// =============================================================================
// HANDLERS
// =============================================================================

// handleHealth checks if the server is up.
// K8s Liveness Probe hits this 24/7. Use it to check "Am I stuck/deadlocked?".
func (app *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// handleStripeWebhook receives webhooks from Stripe.
// GOAL: Receive -> Validate -> Persist (NATS) -> Respond 200.
// We DO NOT process the webhook here. Processing is async (Workqueue).
func (app *App) handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	// Go 1.22 Feature: Extract path value
	connID := r.PathValue("conn_id")
	if connID == "" {
		http.Error(w, "Missing connection ID", http.StatusBadRequest)
		return
	}

	// 1. LOOKUP (Hot Path Optimization)
	// We need the "Signing Secret" to verify the request is real.
	// Querying the DB is slow (5-50ms). Checking RAM is fast (microseconds).
	// We check RAM first.
	secret, err := app.getWebhookSecret(r.Context(), connID)
	if err != nil {
		app.Logger.Warn("Invalid connection or missing secret", "id", connID, "error", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// 2. READ PAYLOAD (Safety)
	// We wrap the request body to enforce a hard limit on the payload size.
	//
	// GO CONCEPT: Bitwise Shift (1 << 20)
	// "1 << 20" means "shift the number 1 to the left by 20 bits".
	// In binary: 1 becomes 100000000000000000000 (20 zeros).
	// Calculation: 2^20 = 1,048,576 bytes which is exactly 1 Binary Megabyte (1 MiB).
	// Cheat Sheet:
	//   1<<10 = 1 KiB (1024 bytes)
	//   1<<20 = 1 MiB (1024 * 1024 bytes)
	//   1<<30 = 1 GiB (1024 * 1024 * 1024 bytes)
	//
	// GO CONCEPT: http.MaxBytesReader
	// This works like a middleware for the IO stream. It wraps the original r.Body.
	// As we read from it, it counts the bytes. If the client sends one byte more
	// than 1MB, it stops reading and returns an error immediately.
	//
	// Security Benefit: This protects the server from "Memory Exhaustion" (DoS) attacks
	// where a malicious actor sends a 10GB payload to crash your server.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		app.Logger.Warn("Failed to read body (possible size limit exceeded)", "id", connID, "error", err)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// 3. VERIFY SIGNATURE (Security)
	// Stripe signs every request with a secret. We hash our body + secret
	// and compare it to their signature. If they don't match, it's a hacker.
	signature := r.Header.Get("Stripe-Signature")
	event, err := webhook.ConstructEvent(body, signature, secret)
	if err != nil {
		app.Logger.Warn("Signature verification failed", "id", connID)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// 4. PERSIST TO VAULT (Durability)
	// We put the event into JetStream "Stream" (persistence).
	// Once NATS says "ACK" (received), we are safe to tell Stripe "200 OK".
	eventID := uuid.New().String()
	msg := nats.NewMsg("raw.ingest.stripe")
	msg.Data = body
	msg.Header.Set("Toro-Conn-ID", connID)
	msg.Header.Set("Toro-Event-ID", eventID)
	msg.Header.Set("Stripe-Event-ID", event.ID)
	msg.Header.Set("Stripe-Event-Type", string(event.Type))
	msg.Header.Set("Timestamp", fmt.Sprintf("%d", time.Now().Unix()))

	// GO CONCEPT: Context Timeouts
	// Publishing over network can hang (packet loss).
	// We use a context to say "Try for 5 seconds, then give up".
	pubCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_, err = app.NATS.PublishMsg(msg, nats.Context(pubCtx))
	if err != nil {
		app.Logger.Error("NATS Publish failed", "error", err)
		// Return 500. Stripe will retry later (usually with exponential backoff).
		http.Error(w, "Persistence Error", http.StatusInternalServerError)
		return
	}

	app.Logger.Info("Webhook ingested", "toro_id", eventID, "stripe_id", event.ID)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// =============================================================================
// HELPERS
// =============================================================================

// getWebhookSecret implements "Cache-Aside" pattern.
// 1. Check Cache... Hit? Return.
// 2. Miss? Query DB... Return.
// 3. Write to Cache.
func (app *App) getWebhookSecret(ctx context.Context, connID string) (string, error) {
	cacheKey := "secret:" + connID

	// L1: Check Ristretto Cache
	if val, found := app.Cache.Get(cacheKey); found {
		return val.(string), nil
	}

	// L2: Check Database (Cold Path)
	// GO CONCEPT: SQLC (Type-Safe SQL)
	// Instead of writing raw strings like "SELECT ...", we use methods generated from our SQL files.
	// Benefit: If we rename a column in DB but forget to update code, the compiler screams at us.
	secret, err := app.Queries.GetWebhookSecret(ctx, connID)
	if err != nil {
		return "", err
	}

	// Cache for 5 minutes.
	// Tradeoff: If user changes secret, it takes 5 mins to update.
	app.Cache.SetWithTTL(cacheKey, secret, 1, 5*time.Minute)
	return secret, nil
}

func getEnv(key, fallback string) string {
	if v, exists := os.LookupEnv(key); exists {
		return v
	}
	return fallback
}
