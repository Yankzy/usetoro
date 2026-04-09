package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/dgraph-io/ristretto"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	fmt.Println("📝 QBO Webhook Secret Manager")
	fmt.Println("==============================")

	ctx := context.Background()

	// Load configuration
	cfg, _, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize PostgreSQL connection pool
	dbConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to parse database config: %v", err)
	}

	dbPool, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer dbPool.Close()

	// Initialize Ristretto cache
	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 1e7,
		MaxCost:     100 << 20, // 100 MB
		BufferItems: 64,
	})
	if err != nil {
		log.Fatalf("Failed to initialize cache: %v", err)
	}

	// Initialize store with encryption
	st, err := store.NewStore(dbPool, cache, cfg.EncryptionKey)
	if err != nil {
		log.Fatalf("Failed to initialize store: %v", err)
	}

	fmt.Println("✅ Connected to database")

	connID := os.Getenv("QBO_WEBHOOK_SECRET")
	if connID == "" {
		log.Fatal("❌ QBO_WEBHOOK_SECRET environment variable cannot be empty")
	}

	secret := os.Getenv("QBO_VERIFIER_TOKEN")
	if secret == "" {
		log.Fatal("❌ QBO_VERIFIER_TOKEN environment variable cannot be empty")
	}

	fmt.Println("✅ Found QBO_WEBHOOK_SECRET and QBO_VERIFIER_TOKEN in environment")

	// Store the secret
	fmt.Println("\n🔐 Encrypting and storing webhook secret...")
	err = st.StoreWebhookSecret(ctx, connID, secret)
	if err != nil {
		log.Fatalf("❌ Failed to store webhook secret: %v", err)
	}

	fmt.Println("✅ Webhook secret stored successfully!")
}
