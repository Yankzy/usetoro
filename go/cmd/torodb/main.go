package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Yankzy/usetoro/internal/cdc"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/knowledge_system"
)

func main() {
	log.Println("Starting ToroDB Engine (Standalone Agentic Database)...")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Database connection (Embedded AlloyDB Omni runs on localhost:5432)
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://toro:toro_password@localhost:5432/toro?sslmode=disable"
	}

	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}

	// 2. Wait for the embedded AlloyDB database to be ready
	var pool *pgxpool.Pool
	var err error
	maxRetries := 30
	for i := 0; i < maxRetries; i++ {
		pool, err = pgxpool.New(ctx, dbURL)
		if err == nil {
			err = pool.Ping(ctx)
			if err == nil {
				log.Println("Successfully connected to embedded AlloyDB Omni engine.")
				break
			}
		}
		log.Printf("Waiting for AlloyDB to start... (%d/%d): %v", i+1, maxRetries, err)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		log.Fatalf("Failed to connect to AlloyDB after %d attempts: %v", maxRetries, err)
	}
	defer pool.Close()

	logger := slog.Default().With("component", "torodb_engine")

	// 3. Initialize Knowledge System Core (Epistemology)
	graphStore := knowledge_system.NewGraphStore(pool, logger)
	docStore := knowledge_system.NewDocumentStore(pool, logger)
	vectorStore := ase.NewVectorStore(pool, nil, logger)
	ingestionEngine := knowledge_system.NewKnowledgeIngestionEngine(docStore, graphStore, vectorStore, logger)

	// Register GraphContextProvider for ASE DAG micro-agent node execution
	graphProvider := knowledge_system.NewGraphContextProvider(graphStore)
	ase.RegisterContextProvider(graphProvider)

	// Start VectorHydrator background worker
	hydrator := ase.NewVectorHydrator(vectorStore, pool, nil, logger)
	go hydrator.Run(ctx)

	logger.Info("Knowledge System Epistemological Core initialized.", "provider", graphProvider.Name())

	// 4. Start Native CDC Replicator in background goroutine
	go func() {
		logger.Info("Starting native ToroDB CDC Replicator WAL tailer...", "nats_url", natsURL)
		if err := cdc.RunReplicator(ctx, dbURL, natsURL); err != nil {
			if err != context.Canceled {
				logger.Error("Native CDC Replicator stopped with error", "error", err)
			}
		}
	}()

	_ = ingestionEngine

	logger.Info("ToroDB Engine fully operational (AlloyDB Omni + Harness + CDC + Epistemology).")

	// 5. Setup graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	sig := <-quit
	logger.Info("Received shutdown signal. Stopping ToroDB Engine...", "signal", sig)
	cancel()
	time.Sleep(1 * time.Second)
}
