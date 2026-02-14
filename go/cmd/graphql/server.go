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
	"strings"
	"syscall"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/Yankzy/usetoro/cmd/graphql/dataloader"
	"github.com/Yankzy/usetoro/cmd/graphql/graph"
	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/infrastructure/vector"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/vektah/gqlparser/v2/ast"
)

const defaultPort = "8080"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(logger); err != nil {
		logger.Error("Fatal startup error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx := context.Background()
	port := cmp.Or(os.Getenv("PORT"), defaultPort)

	// 1. Load Config (Env Vars)
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	redisURL := cmp.Or(os.Getenv("REDIS_URL"), "redis://localhost:6379")
	privateKeyPath := cmp.Or(os.Getenv("AUTH_PRIVATE_KEY"), "keys/private.pem")

	// 2. Load Private Key
	var privKeyBytes []byte
	var err error
	if strings.Contains(privateKeyPath, "-----BEGIN PRIVATE KEY") {
		privKeyBytes = []byte(privateKeyPath)
	} else {
		privKeyBytes, err = os.ReadFile(privateKeyPath)
		if err != nil {
			// For dev/demo if keys are missing we might want to error or generate temp?
			// We'll error for now as it's critical for auth.
			return fmt.Errorf("failed to read private key: %w", err)
		}
	}
	block, _ := pem.Decode(privKeyBytes)
	if block == nil || block.Type != "PRIVATE KEY" {
		return fmt.Errorf("failed to decode PEM block containing private key")
	}
	parsedKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse private key: %w", err)
	}
	edInternalKey, ok := parsedKey.(ed25519.PrivateKey)
	if !ok {
		return fmt.Errorf("key is not an Ed25519 private key")
	}

	// 3. Connect to DB
	dbPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("db connection error: %w", err)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		return fmt.Errorf("db ping failed: %w", err)
	}
	logger.Info("Connected to PostgreSQL")

	// 4. Connect to Redis
	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		return fmt.Errorf("invalid redis url: %w", err)
	}
	rdb := redis.NewClient(redisOpts)
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping failed: %w", err)
	}
	logger.Info("Connected to Redis")

	// 4.5 Initialize Store dependencies (Cache + Encryption)
	// We need an encryption key from env
	encryptionKey := os.Getenv("ENCRYPTION_KEY")
	if encryptionKey == "" {
		// For dev, verify length or default?
		// Ensure it's 32 bytes if we use AES-256
		logger.Warn("ENCRYPTION_KEY is missing, using dummy key for dev (INSECURE)")
		encryptionKey = "12345678901234567890123456789012"
	}

	// Initialize Ristretto Cache (using nil for now since we don't strictly need it for QBO yet or use simple default)
	// Store requires *ristretto.Cache. NewStore takes it.
	// We'll import ristretto and init it.

	// Create Store
	storeObj, err := store.NewStore(dbPool, nil, []byte(encryptionKey))
	if err != nil {
		return fmt.Errorf("failed to create store: %w", err)
	}

	// Initialize Email Sender
	emailSender := auth.NewSMTPEmailSender(auth.EmailConfig{
		Host:     cmp.Or(os.Getenv("EMAIL_HOST"), "smtp.gmail.com"),
		Port:     cmp.Or(os.Getenv("EMAIL_PORT"), "587"),
		User:     os.Getenv("EMAIL_HOST_USER"),
		Password: os.Getenv("EMAIL_HOST_PASSWORD"),
		From:     cmp.Or(os.Getenv("DEFAULT_FROM_EMAIL"), "noreply@usetoro.com"),
	})

	// 4.6 Initialize AI Services
	pineconeAPIKey := os.Getenv("PINECONE_API_KEY")
	pineconeIndex := cmp.Or(os.Getenv("PINECONE_INDEX"), "toro-ai")
	openaiAPIKey := os.Getenv("OPENAI_API_KEY")
	embeddingModel := cmp.Or(os.Getenv("EMBEDDING_MODEL"), "text-embedding-3-small")
	embeddingDimsStr := cmp.Or(os.Getenv("EMBEDDING_DIMENSIONS"), "1536")

	var embeddingDims int
	fmt.Sscanf(embeddingDimsStr, "%d", &embeddingDims)

	var coaMapper *ai.CoAMapper
	var entityResolver *ai.EntityResolver

	if pineconeAPIKey != "" && openaiAPIKey != "" {
		pc, err := vector.NewPineconeClient(pineconeAPIKey, pineconeIndex, embeddingDims)
		if err != nil {
			logger.Warn("Failed to initialize Pinecone client", "error", err)
		} else {
			emb, err := vector.NewEmbedder(openaiAPIKey, embeddingModel, embeddingDims)
			if err != nil {
				logger.Warn("Failed to initialize OpenAI embedder", "error", err)
			} else {
				coaMapper = ai.NewCoAMapper(pc, emb, 0.7) // Default threshold 0.7
				entityResolver = ai.NewEntityResolver(storeObj, pc, emb, 0.7)
				logger.Info("AI services initialized successfully")
			}
		}
	} else {
		logger.Warn("AI services skipped: PINECONE_API_KEY or OPENAI_API_KEY mission")
	}

	// 5. Setup GraphQL Server
	srv := handler.New(graph.NewExecutableSchema(graph.Config{
		Resolvers: &graph.Resolver{
			DB:             dbPool,
			Redis:          rdb,
			PrivateKey:     edInternalKey,
			Logger:         logger,
			Store:          storeObj,
			EmailSender:    emailSender,
			CoAMapper:      coaMapper,
			EntityResolver: entityResolver,
		},
	}))

	srv.AddTransport(transport.Options{})
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})

	srv.SetQueryCache(lru.New[*ast.QueryDocument](1000))

	srv.Use(extension.Introspection{})
	srv.Use(extension.AutomaticPersistedQuery{
		Cache: lru.New[string](100),
	})

	// Setup Auth Middleware
	authenticator := &auth.Authenticator{
		PublicKey: edInternalKey.Public().(ed25519.PublicKey),
		Redis:     rdb,
		DB:        database.New(dbPool),
	}

	// Setup Dataloader Middleware
	loaderMiddleware := dataloader.Middleware(dbPool)

	// Chain middlewares: Auth -> Dataloader -> GraphQL
	// Note: Dataloader might need Auth info if we had permission-based loading,
	// but usually Auth is outer-most to protect everything.
	// However, here we want Dataloaders available to resolvers, so Dataloader wraps the handler?
	// Actually, the handler is the leaf. The outer-most middleware executes FIRST.
	// We want Auth running first to populate context, then Dataloader (can use auth if needed), then Handler.
	// So: Auth(Dataloader(Handler))

	// mux.Handle("/api", authenticator.Middleware(loaderMiddleware(srv)))

	mux := http.NewServeMux()
	mux.Handle("/playground", playground.Handler("GraphQL playground", "/api"))
	mux.Handle("/api", authenticator.Middleware(loaderMiddleware(srv)))

	server := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	// 6. Start Server
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("GraphQL server listening", "port", port)
		serverErrors <- server.ListenAndServe()
	}()

	// 7. Graceful Shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)
	case sig := <-shutdown:
		logger.Info("Shutdown signal received", "signal", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			return fmt.Errorf("server shutdown error: %w", err)
		}
	}

	return nil
}
