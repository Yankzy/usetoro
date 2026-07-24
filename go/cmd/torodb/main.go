package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	log.Println("Starting ToroDB Stream Engine...")

	// Database connection (Embedded AlloyDB Omni runs on localhost:5432)
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://toro:toro_password@localhost:5432/toro?sslmode=disable"
	}

	// Wait for the local database to be ready
	var pool *pgxpool.Pool
	var err error
	maxRetries := 30
	for i := 0; i < maxRetries; i++ {
		pool, err = pgxpool.New(context.Background(), dbURL)
		if err == nil {
			err = pool.Ping(context.Background())
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

	log.Println("ToroDB DAG Orchestration and Sync Layer Initialized.")
	log.Println("Listening for incoming streams...")

	// Setup graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Keep the main process running
	sig := <-quit
	log.Printf("Received signal %v. Shutting down ToroDB Stream Engine...", sig)
}
