package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/store"
	"github.com/dgraph-io/ristretto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	fmt.Println("📝 QBO Webhook Secret Manager")
	fmt.Println("==============================")

	ctx := context.Background()

	// Load configuration
	cfg, err := config.Load()
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

	// Generate a new UUID for the connection
	connID := uuid.New().String()
	fmt.Printf("\n🔑 Generated Connection ID: %s\n\n", connID)

	// Read webhook secret
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Enter Webhook Secret: ")
	secret, err := reader.ReadString('\n')
	if err != nil {
		log.Fatalf("Failed to read secret: %v", err)
	}
	secret = strings.TrimSpace(secret)

	if secret == "" {
		log.Fatal("❌ Webhook secret cannot be empty")
	}

	// Store the secret
	fmt.Println("\n🔐 Encrypting and storing webhook secret...")
	err = st.StoreWebhookSecret(ctx, connID, secret)
	if err != nil {
		log.Fatalf("❌ Failed to store webhook secret: %v", err)
	}

	fmt.Println("✅ Webhook secret stored successfully!")
	fmt.Printf("\n📍 Webhook URL: POST /webhooks/qbo/%s\n", connID)
	fmt.Println("📍 Header: intuit-signature")
	fmt.Println("\n💡 Save this Connection ID for your records!")
}
