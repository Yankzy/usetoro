package main

import (
	"cmp"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/infra/vector"
	"github.com/Yankzy/usetoro/internal/services/fignode"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(logger); err != nil {
		logger.Error("fatal startup error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// =========================================================================
	// Configuration
	// =========================================================================
	port := envOr("PORT", "8083")
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	// =========================================================================
	// PostgreSQL
	// =========================================================================
	dbConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return fmt.Errorf("db config error: %w", err)
	}
	dbConfig.MaxConns = 50
	dbConfig.MinConns = 5

	pool, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		return fmt.Errorf("db connection error: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("db ping failed: %w", err)
	}
	logger.Info("connected to PostgreSQL")

	db := database.New(pool)

	// =========================================================================
	// Redis
	// =========================================================================
	redisURL := envOr("REDIS_URL", "redis://redis:6379")
	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("redis url parse error: %w", err)
	}
	redisClient := redis.NewClient(redisOpts)

	if err := redisClient.Ping(ctx).Err(); err != nil {
		logger.Warn("redis connection failed, running without cache", "error", err)
		redisClient = nil
	} else {
		logger.Info("connected to Redis")
	}

	cache := fignode.NewCache(redisClient)

	// =========================================================================
	// NATS
	// =========================================================================
	natsURL := envOr("NATS_URL", "nats://localhost:4222")
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return fmt.Errorf("nats connection error: %w", err)
	}
	defer nc.Close()
	logger.Info("connected to NATS")

	// =========================================================================
	// Ed25519 Keys (for JWT signing & verification)
	// =========================================================================
	privKey, pubKey, err := loadEdKeys()
	if err != nil {
		return fmt.Errorf("key loading error: %w", err)
	}
	logger.Info("loaded Ed25519 key pair")

	authenticator := &auth.Authenticator{
		PublicKey: pubKey,
		Redis:     redisClient,
		DB:        db,
	}

	// =========================================================================
	// Services
	// =========================================================================
	leaderboardSvc := fignode.NewLeaderboardService(db, logger)

	// =========================================================================
	// Email Sender
	// =========================================================================
	emailSender := auth.NewSMTPEmailSender(auth.EmailConfig{
		Host:     cmp.Or(os.Getenv("EMAIL_HOST"), "smtp.gmail.com"),
		Port:     cmp.Or(os.Getenv("EMAIL_PORT"), "587"),
		User:     os.Getenv("EMAIL_HOST_USER"),
		Password: os.Getenv("EMAIL_HOST_PASSWORD"),
		From:     cmp.Or(os.Getenv("DEFAULT_FROM_EMAIL"), "noreply@usetoro.com"),
	})

	// =========================================================================
	// HTTP Handler
	// =========================================================================
	handler := fignode.NewHandler(
		logger,
		db,
		pool,
		redisClient,
		cache,
		privKey, // Passed to both Authenticator & API for token verification vs minting
		authenticator,
		emailSender,
		leaderboardSvc,
	)
	router := fignode.NewRouter(handler)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	streakSvc := fignode.NewStreakService(db, logger)
	fignodeWorkers := fignode.NewWorkers(db, leaderboardSvc, streakSvc, logger)
	fignodeWorkers.Start(ctx)

	// AI CoAMapper initially used for FignodeWorker
	pineconeKey := os.Getenv("PINECONE_API_KEY")
	pineconeIndex := os.Getenv("PINECONE_INDEX")
	openaiKey := os.Getenv("OPENAI_API_KEY")

	if pineconeKey != "" && openaiKey != "" {
		embeddingModel := envOr("EMBEDDING_MODEL", "text-embedding-3-large")
		// Using 3072 as default based on text-embedding-3-large
		_, err := vector.NewPineconeClient(pineconeKey, pineconeIndex, 3072)
		if err != nil {
			logger.Warn("Failed to init Pinecone client", "error", err)
		} else {
			_, err := vector.NewEmbedder(openaiKey, embeddingModel, 3072)
			if err != nil {
				logger.Warn("Failed to init Embedder", "error", err)
			}
		}
	} else {
		logger.Warn("AI keys missing")
	}

	// =========================================================================
	// Startup & Graceful Shutdown
	// =========================================================================
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("fignode service started", "addr", srv.Addr)
		serverErrors <- srv.ListenAndServe()
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)

	case sig := <-shutdown:
		logger.Info("shutdown signal received", "signal", sig)
		cancel() // stops background workers

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			srv.Close()
			return fmt.Errorf("could not stop server gracefully: %w", err)
		}
	}

	return nil
}

func loadEdKeys() (ed25519.PrivateKey, ed25519.PublicKey, error) {
	privPath := envOr("AUTH_PRIVATE_KEY", "keys/private.pem")
	pubPath := envOr("AUTH_PUBLIC_KEY", "keys/public.pem")

	privBytes, err := os.ReadFile(privPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read private key: %w", err)
	}
	block, _ := pem.Decode(privBytes)
	if block == nil {
		return nil, nil, fmt.Errorf("failed to decode private key PEM")
	}
	parsedPriv, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse private key: %w", err)
	}
	privKey, ok := parsedPriv.(ed25519.PrivateKey)
	if !ok {
		return nil, nil, fmt.Errorf("key is not Ed25519 private key")
	}

	pubBytes, err := os.ReadFile(pubPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read public key: %w", err)
	}
	pubBlock, _ := pem.Decode(pubBytes)
	if pubBlock == nil {
		return nil, nil, fmt.Errorf("failed to decode public key PEM")
	}
	parsedPub, err := x509.ParsePKIXPublicKey(pubBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse public key: %w", err)
	}
	pubKey, ok := parsedPub.(ed25519.PublicKey)
	if !ok {
		return nil, nil, fmt.Errorf("key is not Ed25519 public key")
	}

	return privKey, pubKey, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
