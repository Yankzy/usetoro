package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
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
	"github.com/Yankzy/usetoro/internal/services/cleanup"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/dgraph-io/ristretto"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
)

func main() {
	// 1. Setup Structured Logging (JSON)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 2. Load and Validate Configuration
	cfg, _, err := config.Load()
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

	// Provision all configured NATS JetStream streams globally for the platform
	serviceKeys := make([]string, 0, len(cfg.NATS.Services))
	for k := range cfg.NATS.Services {
		serviceKeys = append(serviceKeys, k)
	}
	sort.Slice(serviceKeys, func(i, j int) bool {
		a, b := serviceKeys[i], serviceKeys[j]
		if a == "workflows" && b != "workflows" {
			return true
		}
		if b == "workflows" && a != "workflows" {
			return false
		}
		if a == "workflow_triggers" && b != "workflow_triggers" {
			return false
		}
		if b == "workflow_triggers" && a != "workflow_triggers" {
			return true
		}
		return a < b
	})

	for _, svcName := range serviceKeys {
		srvCfg := cfg.NATS.Services[svcName]
		// 1. Provision main service stream
		if srvCfg.StreamName != "" && len(srvCfg.JetStream.Subjects) > 0 {
			streamCfg := &nats.StreamConfig{
				Name:        srvCfg.StreamName,
				Subjects:    srvCfg.JetStream.Subjects,
				Storage:     nats.FileStorage,
				MaxAge:      srvCfg.JetStream.MaxAge,
				Replicas:    srvCfg.JetStream.Replicas,
				DenyDelete:  srvCfg.JetStream.DenyDelete,
				DenyPurge:   srvCfg.JetStream.DenyPurge,
				AllowRollup: srvCfg.JetStream.AllowRollup,
				AllowDirect: srvCfg.JetStream.AllowDirect,
				AllowMsgTTL: srvCfg.JetStream.AllowMsgTTL,
			}
			if streamCfg.Replicas == 0 {
				streamCfg.Replicas = 1
			}
			if streamCfg.Name == "WORKFLOW_TRIGGERS" {
				// Subjects are reconciled dynamically by the Orchestrator; don't overwrite them here.
				if err := q.EnsureStreamExists(streamCfg); err != nil {
					logger.Warn("Failed to ensure stream exists", "service", svcName, "stream", srvCfg.StreamName, "error", err)
				}
			} else {
				if err := q.EnsureStream(streamCfg); err != nil {
					logger.Warn("Failed to ensure configured stream", "service", svcName, "stream", srvCfg.StreamName, "error", err)
				}
			}
		}

		// 2. Provision component streams
		for compName, compCfg := range srvCfg.Components {
			if compCfg.StreamName != "" && len(compCfg.JetStream.Subjects) > 0 {
				compStreamCfg := &nats.StreamConfig{
					Name:        compCfg.StreamName,
					Subjects:    compCfg.JetStream.Subjects,
					Storage:     nats.FileStorage,
					MaxAge:      compCfg.JetStream.MaxAge,
					Replicas:    compCfg.JetStream.Replicas,
					DenyDelete:  compCfg.JetStream.DenyDelete,
					DenyPurge:   compCfg.JetStream.DenyPurge,
					AllowRollup: compCfg.JetStream.AllowRollup,
					AllowDirect: compCfg.JetStream.AllowDirect,
					AllowMsgTTL: compCfg.JetStream.AllowMsgTTL,
				}
				if compStreamCfg.Replicas == 0 {
					compStreamCfg.Replicas = 1
				}
				if err := q.EnsureStream(compStreamCfg); err != nil {
					logger.Warn("Failed to ensure component stream", "service", svcName, "component", compName, "stream", compCfg.StreamName, "error", err)
				}
			}
		}
	}

	// 2.5 Ensure cluster stream consensus stabilized.
	var streamsToWait []string
	for _, srvCfg := range cfg.NATS.Services {
		if srvCfg.StreamName != "" {
			streamsToWait = append(streamsToWait, srvCfg.StreamName)
		}
		for _, compCfg := range srvCfg.Components {
			if compCfg.StreamName != "" {
				streamsToWait = append(streamsToWait, compCfg.StreamName)
			}
		}
	}
	if err := q.WaitForStreamsReady(streamsToWait); err != nil {
		logger.Error("Critical failure waiting for JetStream cluster stability", "error", err)
		return fmt.Errorf("crashing to reboot JetStream cluster stability: %w", err)
	}

	logger.Info("✅ Connected to NATS JetStream and Ensured configured streams")

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

	// DI: Create Server with cleanup exporter for Excel/PDF endpoints.
	cleanupExporter := cleanup.NewExporter(database.New(dbPool))
	srv := api.NewServer(cfg, logger, st, pub, qboConfig, authenticator, redisClient, q, cleanupExporter)

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
