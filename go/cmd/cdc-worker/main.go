package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Yankzy/usetoro/internal/cdc"
)

func main() {
	// 1. Context and Graceful Shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received termination signal, shutting down CDC worker...")
		cancel()
	}()

	// 2. Load config from Env
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		log.Println("NATS_URL is not set, defaulting to nats://localhost:4222")
		natsURL = "nats://localhost:4222"
	}

	// 3. Start CDC Replicator
	log.Println("Starting CDC Pipeline Worker...")
	err := cdc.RunReplicator(ctx, dbURL, natsURL)
	if err != nil {
		if err == context.Canceled {
			log.Println("CDC Replicator shutdown successfully.")
		} else {
			log.Fatalf("CDC Replicator failed: %v", err)
		}
	}
}
