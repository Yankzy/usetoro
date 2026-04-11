package main

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/queue"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/internal/wshandler"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

func main() {
	// Setup structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Load Configuration
	cfg, _, err := config.Load()
	if err != nil {
		logger.Error("Configuration Loading Failed", "error", err)
		os.Exit(1)
	}

	// Get configuration from environment (or config if mapped)
	addr := os.Getenv("WS_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	// QBO Configuration
	rawRedirectURIs := strings.Split(os.Getenv("QBO_REDIRECT_URI"), ",")
	var redirectURIs []string
	for _, uri := range rawRedirectURIs {
		if trimmed := strings.TrimSpace(uri); trimmed != "" {
			redirectURIs = append(redirectURIs, trimmed)
		}
	}

	qboConfig := &wshandler.Config{
		QBOClientID:     os.Getenv("QBO_CLIENT_ID"),
		QBOClientSecret: os.Getenv("QBO_CLIENT_SECRET"),
		QBORedirectURIs: redirectURIs,
		QBOIsProduction: os.Getenv("QBO_IS_PRODUCTION") == "true",
	}

	// Default redirect URI if not set
	if len(qboConfig.QBORedirectURIs) == 0 {
		qboConfig.QBORedirectURIs = []string{"http://localhost/api/auth/qbo/callback"}
	}

	// Auth Configuration
	authPublicKeyPath := os.Getenv("AUTH_PUBLIC_KEY")
	if authPublicKeyPath == "" {
		authPublicKeyPath = "keys/public.pem"
	} else if _, err := os.Stat(authPublicKeyPath); os.IsNotExist(err) {
		// If provided path doesn't exist, assume it might be the content or try default
		// For now, let's stick to file path as per convention in cmd/auth
	}

	pubKeyBytes, err := os.ReadFile(authPublicKeyPath)
	if err != nil {
		logger.Error("Failed to read public key", "path", authPublicKeyPath, "error", err)
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

	// Redis Configuration
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}

	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		logger.Error("Invalid Redis URL", "url", redisURL, "error", err)
		os.Exit(1)
	}

	rdb := redis.NewClient(redisOpts)
	// We'll check connectivity in a moment...

	authenticator := &auth.Authenticator{
		PublicKey: edPubKey,
		Redis:     rdb,
	}

	logger.Info("Starting WebSocket server",
		"addr", addr,
		"qbo_configured", qboConfig.QBOClientID != "",
		"auth_configured", true,
		"redis_url", redisURL,
	)

	// Check Redis connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Error("Failed to connect to Redis", "url", redisURL, "error", err)
		os.Exit(1)
	}
	logger.Info("Connected to Redis")
	defer rdb.Close()

	// Connect to Database
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		logger.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	dbConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		logger.Error("Failed to parse database url", "error", err)
		os.Exit(1)
	}

	dbPool, err := pgxpool.NewWithConfig(context.Background(), dbConfig)
	if err != nil {
		logger.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(context.Background()); err != nil {
		logger.Error("Failed to ping database", "error", err)
		os.Exit(1)
	}
	logger.Info("Connected to PostgreSQL")

	queries := database.New(dbPool)

	// Initialize NATS connection
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://nats:4222"
	}

	// Initialize NATS connection with retries
	var queueClient *queue.Client
	maxRetries := 15
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		logger.Info("Connecting to NATS", "url", natsURL, "attempt", i+1)
		queueClient, err = queue.NewClient(
			natsURL,
			nats.Name("ws-service"),
			nats.MaxReconnects(-1),
			nats.ReconnectWait(2*time.Second),
		)

		if err == nil {

			// Ensure all required streams exist (config-driven)
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
				if srvCfg.StreamName != "" {
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
					}
					if streamCfg.Replicas == 0 {
						streamCfg.Replicas = 1
					}
					if streamCfg.Name == "WORKFLOW_TRIGGERS" {
						if err := queueClient.EnsureStreamExists(streamCfg); err != nil {
							logger.Warn("Failed to ensure stream exists", "service", svcName, "stream", srvCfg.StreamName, "error", err)
						}
					} else {
						if err := queueClient.EnsureStream(streamCfg); err != nil {
							logger.Warn("Failed to ensure stream", "service", svcName, "stream", srvCfg.StreamName, "error", err)
						}
					}
				}

				for compName, compCfg := range srvCfg.Components {
					if compCfg.StreamName != "" {
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
						}
						if compStreamCfg.Replicas == 0 {
							compStreamCfg.Replicas = 1
						}
						if err := queueClient.EnsureStream(compStreamCfg); err != nil {
							logger.Warn("Failed to ensure component stream", "component", compName, "stream", compCfg.StreamName, "error", err)
						}
					}
				}
			}

			// Hardcode wait for QBO_EVENTS since ws-1 explicitly binds to them.
			if err := queueClient.WaitForStreamsReady([]string{"QBO_EVENTS"}); err != nil {
				logger.Error("Critical failure waiting for JetStream cluster stability", "error", err)
				os.Exit(1)
			}

			logger.Info("Connected to NATS and JetStream streams ensured")
			lastErr = nil
			break
		} else {
			lastErr = err
			logger.Warn("Failed to connect to NATS, retrying...", "error", err)
		}
		time.Sleep(2 * time.Second)
	}

	if lastErr != nil {
		logger.Error("Failed to initialize NATS/JetStream after multiple attempts", "error", lastErr)
		os.Exit(1)
	}
	defer queueClient.Close()

	// Create WebSocket hub & LLM Context Wrapper
	apiKey := os.Getenv("OPENAI_API_KEY")
	llmClient, _ := ai.NewLLMClient(apiKey, "")
	hub := wshandler.NewHub(logger, queueClient, queries, llmClient)
	go hub.Run()

	// Create and start QBO event consumer
	qboConsumer := wshandler.NewQBOEventConsumer(queueClient, hub, logger, cfg)
	if err := qboConsumer.Start(); err != nil {
		logger.Error("Failed to start QBO event consumer", "error", err)
		os.Exit(1)
	}
	defer qboConsumer.Stop()

	// Create and start Workflow event consumer
	workflowConsumer := wshandler.NewWorkflowEventConsumer(queueClient, hub, logger, cfg)
	if err := workflowConsumer.Start(); err != nil {
		logger.Error("Failed to start workflow event consumer", "error", err)
		os.Exit(1)
	}
	defer workflowConsumer.Stop()

	// Create message handler
	messageHandler := wshandler.NewMessageHandler(logger, qboConfig)

	// Create WebSocket handler
	wsHandler := wshandler.NewHandler(hub, logger, messageHandler)

	// Setup HTTP routes
	mux := http.NewServeMux()
	// Wrap handleWebSocket with QueryMiddleware
	mux.Handle("/ws", authenticator.QueryMiddleware(http.HandlerFunc(wsHandler.ServeWS)))

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","clients":` + string(rune(hub.ClientCount())) + `}`))
	})

	// Create HTTP server
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("WebSocket server listening", "addr", addr)
		serverErrors <- server.ListenAndServe()
	}()

	// Setup graceful shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	// Wait for either error or shutdown signal
	select {
	case err := <-serverErrors:
		logger.Error("Server error", "error", err)
		os.Exit(1)

	case sig := <-shutdown:
		logger.Info("Shutdown signal received", "signal", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			logger.Error("Could not stop server gracefully", "error", err)
			server.Close()
			os.Exit(1)
		}

		logger.Info("Server stopped gracefully")
	}
}
