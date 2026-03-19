package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/infra/vector"
	"github.com/Yankzy/usetoro/internal/services/ai"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/Yankzy/usetoro/tap/agents/cleanup"
	"github.com/Yankzy/usetoro/tap/agents/enrichment"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/transport"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logger := slog.Default()
	logger.Info("🤖 Starting TAP Internal Agents Runner")

	// 1. Load Config
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 2. Connect to Database & Vector DBs
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	db := database.New(pool)

	pineconeKey := os.Getenv("PINECONE_API_KEY")
	openaiKey := os.Getenv("OPENAI_API_KEY")
	
	if pineconeKey == "" {
		logger.Warn("PINECONE_API_KEY is skipping AI Entity Resolvers")
	}

	pcObj, _ := vector.NewPineconeClient(pineconeKey, cfg.PineconeIndex, cfg.EmbeddingDimensions)
	embedder, _ := vector.NewEmbedder(openaiKey, cfg.EmbeddingModel, cfg.EmbeddingDimensions)
	
	st, _ := store.NewStore(nil, nil, cfg.EncryptionKey)
	entityResolver := ai.NewEntityResolver(st, pcObj, embedder, cfg.AIThreshold)
	coaMapper := ai.NewCoAMapper(pcObj, embedder, cfg.AIThreshold)

	// 3. Connect to NATS
	nc, err := transport.Connect(cfg.NATS.URL)
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		log.Fatalf("Failed to get JetStream context: %v", err)
	}

	bus := agent.NewNatsAdapter(nc, js)

	// 4. Initialize Supervisor
	// Passing nil for MemoryStore for now as per internal structure
	supervisor := agent.NewSupervisor(logger, bus, nil)

	// 5. Register Internal Modules
	supervisor.RegisterInternalAgent("cleanup-agent", func(l *slog.Logger, b agent.EventBus, c agent.AgentConfig, m agent.MemoryStore) agent.Runnable {
		return cleanup.NewAgent(l, b, c, m)
	})

	supervisor.RegisterInternalAgent("enrichment-agent", func(l *slog.Logger, b agent.EventBus, c agent.AgentConfig, m agent.MemoryStore) agent.Runnable {
		return enrichment.NewAgent(l, b, c, m, db, entityResolver, coaMapper)
	})

	// 6. Load Agents from Config
	if err := supervisor.LoadAgents(cfg.Agents); err != nil {
		log.Fatalf("Failed to load agents: %v", err)
	}

	// 7. Run Heartbeat Loop (Blocking)
	go func() {
		if err := supervisor.Run(ctx); err != nil {
			logger.Error("Supervisor loop failed", "error", err)
		}
	}()

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	logger.Info("Received shutdown signal")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	supervisor.Shutdown(shutdownCtx)
}
