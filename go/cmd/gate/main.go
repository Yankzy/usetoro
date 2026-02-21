package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"

	"github.com/Yankzy/usetoro/internal/api"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/redis/go-redis/v9"

	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/ingest"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/dgraph-io/ristretto"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
)

func main() {
	// 1. Setup Structured Logging (JSON)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 2. Load and Validate Configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Error("Configuration Loading Failed", "error", err)
		os.Exit(1)
	}

	// 3. Start the Application
	if err := run(cfg, logger); err != nil {
		logger.Error("Fatal startup error", "error", err)
		os.Exit(1)
	}
}

func run(cfg *config.Config, logger *slog.Logger) error {
	ctx := context.Background()

	// =========================================================================
	// INFRASTRUCTURE INITIALIZATION
	// =========================================================================

	// 1. PostgreSQL (Metadata Store)
	dbConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db config error: %w", err)
	}
	dbConfig.MaxConns = int32(cfg.DBMaxConns)
	dbConfig.MinConns = int32(cfg.DBMinConns)

	dbPool, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		return fmt.Errorf("db connection error: %w", err)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		logger.Error("db ping failed",
			"error", err,
			"host", dbConfig.ConnConfig.Host,
			"port", dbConfig.ConnConfig.Port,
			"database", dbConfig.ConnConfig.Database,
		)
		return fmt.Errorf("db ping failed: %w", err)
	}
	logger.Info("✅ Connected to PostgreSQL")

	// 2. NATS JetStream (Durability Layer)
	q, err := queue.NewClient(cfg.NATS.URL,
		nats.Name("toro-ingress"),
		nats.MaxReconnects(10),
		nats.ReconnectWait(2*time.Second),
		nats.ReconnectJitter(500*time.Millisecond, 2*time.Second),
	)
	if err != nil {
		return fmt.Errorf("queue client init error: %w", err)
	}
	defer q.Close()

	logger.Info("✅ Connected to NATS JetStream")

	// 3. Ristretto Cache (L1 Cache)
	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 1e7,
		MaxCost:     100 << 20, // 100 * 1MiB
		BufferItems: 64,
	})
	if err != nil {
		return fmt.Errorf("cache init error: %w", err)
	}
	logger.Info("✅ Initialized Cache")

	// 4. Authenticator (For future Admin routes)
	// We load the public key to verify internal JWTs.
	// Webhooks from Stripe/Plaid do NOT use this.
	authPublicKeyPath := os.Getenv("AUTH_PUBLIC_KEY")
	if authPublicKeyPath == "" {
		authPublicKeyPath = "keys/public.pem"
	}
	var authenticator *auth.Authenticator

	// Only try to load if file exists or env var is explicitly set
	// This prevents crashing if gate is deployed without keys (pure webhook mode)
	if _, err := os.Stat(authPublicKeyPath); err == nil {
		pubKeyBytes, err := os.ReadFile(authPublicKeyPath)
		if err != nil {
			logger.Error("Failed to read public key", "error", err)
			os.Exit(1)
		}
		block, _ := pem.Decode(pubKeyBytes)
		if block == nil || block.Type != "PUBLIC KEY" {
			logger.Error("Failed to decode PEM block containing public key")
			os.Exit(1)
		}
		parsedKey, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			logger.Error("Failed to parse public key", "error", err)
			os.Exit(1)
		}
		edPubKey, ok := parsedKey.(ed25519.PublicKey)
		if !ok {
			logger.Error("Key is not an Ed25519 public key")
			os.Exit(1)
		}
		authenticator = &auth.Authenticator{
			PublicKey: edPubKey,
			// Gate might not need Redis for simple signature checks,
			// or we can reuse `q` if it was a redis client, but `q` is Nats here.
			// `cache` is local ristretto.
			// For now, no revocation check in Gate to keep it simple/fast?
			// The instructions didn't force Redis in Gate.
			DB: database.New(dbPool),
		}
		logger.Info("✅ Initialized Authenticator")
	} else {
		logger.Warn("Auth Public Key not found, admin routes will not be secured if added later", "path", authPublicKeyPath)
	}

	// TODO: Use app.Auth.Middleware for future internal admin routes
	// Example: mux.Handle("/api/admin", authenticator.Middleware(adminHandler))
	_ = authenticator // Suppress unused error

	// =========================================================================
	// APPLICATION WIRING
	// =========================================================================

	st, err := store.NewStore(dbPool, cache, cfg.EncryptionKey)
	if err != nil {
		return fmt.Errorf("store init error: %w", err)
	}
	pub := ingest.NewPublisher(q)

	// Load QBO OAuth2 configuration from environment
	rawRedirectURIs := strings.Split(os.Getenv("QBO_REDIRECT_URI"), ",")
	var redirectURIs []string
	for _, uri := range rawRedirectURIs {
		if trimmed := strings.TrimSpace(uri); trimmed != "" {
			redirectURIs = append(redirectURIs, trimmed)
		}
	}

	qboConfig := &api.QBOConfig{
		ClientID:     os.Getenv("QBO_CLIENT_ID"),
		ClientSecret: os.Getenv("QBO_CLIENT_SECRET"),
		RedirectURIs: redirectURIs,
		IsProduction: os.Getenv("QBO_IS_PRODUCTION") == "true" || os.Getenv("QBO_IS_PRODUCTION") == "1",
	}

	// Validate QBO config
	if qboConfig.ClientID == "" || qboConfig.ClientSecret == "" {
		logger.Warn("QBO credentials not configured - OAuth will not work")
	}

	if len(qboConfig.RedirectURIs) == 0 {
		qboConfig.RedirectURIs = []string{"http://localhost/api/auth/qbo/callback"}
		logger.Info("Using default QBO redirect URI", "uri", qboConfig.RedirectURIs[0])
	}

	logger.Info("QBO configuration loaded",
		"client_id_configured", qboConfig.ClientID != "",
		"redirect_uris", qboConfig.RedirectURIs,
		"is_production", qboConfig.IsProduction,
	)

	// 5. Initialize Redis Client (for dead-letter queue)
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://redis:6379"
	}
	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("failed to parse REDIS_URL: %w", err)
	}
	redisClient := redis.NewClient(redisOpts)

	// Test Redis connection
	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Warn("Redis connection failed - webhook retries will not persist to Redis", "error", err)
		// Don't fail startup - Redis is optional for webhook retry persistence
		redisClient = nil
	} else {
		logger.Info("✅ Connected to Redis")
	}

	// DI: Create Server
	srv := api.NewServer(cfg, logger, st, pub, qboConfig, authenticator, redisClient)

	// =========================================================================
	// STARTUP & GRACEFUL SHUTDOWN
	// =========================================================================

	serverErrors := srv.Start()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	// We wait for either a server error or a shutdown signal
	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)

	case sig := <-shutdown:
		logger.Info("Shutdown signal received", "signal", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			srv.Close()
			return fmt.Errorf("could not stop server gracefully: %w", err)
		}
	}

	return nil
}
