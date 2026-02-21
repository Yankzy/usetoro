package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/tap/pkg/contract"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/store"
	"github.com/Yankzy/usetoro/tap/pkg/transport"
)

func main() {
	// Load Configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Configuration Loading Failed: %v", err)
	}

	// 1. Connect to NATS
	url := cfg.NATS.URL
	if url == "" {
		url = nats.DefaultURL
	}
	nc, err := transport.Connect(url)
	if err != nil {
		log.Fatalf("❌ Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	// 2. Connect to Redis
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("❌ Invalid REDIS_URL: %v", err)
	}
	rdb := redis.NewClient(opt)

	// Ping Redis to ensure connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("❌ Failed to connect to Redis: %v", err)
	}

	// 3. Dependencies
	repo := store.NewRedisStore(rdb)
	contract := contract.NewContract(repo)

	// 4. Handlers

	// 4. Handlers (Config-driven)
	settlementConfig, ok := cfg.NATS.Services["settlement"]
	if !ok {
		log.Fatalf("❌ Settlement service configuration not found")
	}

	for _, subject := range settlementConfig.JetStream.Subjects {
		log.Printf("Listening on subject: %s", subject)
		nc.Subscribe(subject, func(msg *nats.Msg) {
			// Router logic based on subject match
			// This is a simple router. In a real app, we might want a map or cleaner router.
			// But since we are looping, we need to dispatch based on actual subject.

			switch msg.Subject {
			case "contracts.request":
				handleContractRequest(msg, contract, nc)
			case "proof.submit":
				handleProofSubmit(msg, contract, nc)
			case "raw.ingest.samsara":
				handleSamsaraIngest(msg, nc)
			default:
				log.Printf("Unknown subject: %s", msg.Subject)
			}
		})
	}
}

func handleContractRequest(msg *nats.Msg, contract *contract.Machine, nc *nats.Conn) {
	var req core.Contract
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		return
	}

	lockedContract, err := contract.Lock(&req)
	if err != nil {
		log.Printf("Failed to lock: %v", err)
		return
	}

	// Publish Event
	eventData, _ := json.Marshal(lockedContract)
	nc.Publish("events.contract.locked", eventData)
	log.Printf("🔒 Contract Locked: %s", lockedContract.ID)
}

func handleProofSubmit(msg *nats.Msg, contract *contract.Machine, nc *nats.Conn) {
	var proof core.Proof
	if err := json.Unmarshal(msg.Data, &proof); err != nil {
		return
	}

	compContract, settled, err := contract.Settle(&proof)
	if err != nil {
		log.Printf("Error processing proof: %v", err)
		return
	}

	if settled {
		eventData, _ := json.Marshal(compContract)
		nc.Publish("events.contract.settled", eventData)
		log.Printf("💰 Contract Settled: %s", compContract.ID)
	}
}

func handleSamsaraIngest(msg *nats.Msg, nc *nats.Conn) {
	// 1. Parse Raw Webhook
	type WebhookPayload struct {
		SourceID string          `json:"source_id"`
		TaskID   string          `json:"task_id"`
		Data     json.RawMessage `json:"data"`
	}

	var hook WebhookPayload
	if err := json.Unmarshal(msg.Data, &hook); err != nil {
		log.Printf("Error unmarshalling samsara webhook: %v", err)
		return
	}

	log.Printf("Received Webhook for Task %s via Gate", hook.TaskID)

	// 2. Transform to TAP Proof
	proof := core.Proof{
		TaskID:    hook.TaskID,
		Type:      core.ProofGPS,
		Timestamp: time.Now().Unix(),
		Data:      hook.Data,
		Signature: "sig_hive_converted_123",
	}

	// 3. Publish to Proof Funnel
	proofBytes, _ := json.Marshal(proof)
	if err := nc.Publish("proof.submit", proofBytes); err != nil {
		log.Printf("Error publishing proof: %v", err)
		return
	}

	log.Printf("Proof Submitted for Task %s", hook.TaskID)

	log.Println("🏛️  Settlement Engine Running (Redis Backed)...")

	// Wait
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("🔻 Shutting down Settlement Engine...")
}
