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

	"github.com/Yankzy/usetoro/tap/pkg/contract"
	"github.com/Yankzy/usetoro/tap/pkg/store"
	"github.com/Yankzy/usetoro/tap/pkg/tap"
	"github.com/Yankzy/usetoro/tap/pkg/transport"
)

func main() {
	// 1. Connect to NATS
	url := os.Getenv("NATS_URL")
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

	// Handler: Contract Locking
	nc.Subscribe("contracts.request", func(msg *nats.Msg) {
		var req tap.Contract
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
	})

	// Handler: Proof / Settlement
	nc.Subscribe("proof.submit", func(msg *nats.Msg) {
		var proof tap.Proof
		if err := json.Unmarshal(msg.Data, &proof); err != nil {
			return
		}

		contract, settled, err := contract.Settle(&proof)
		if err != nil {
			log.Printf("Error processing proof: %v", err)
			return
		}

		if settled {
			eventData, _ := json.Marshal(contract)
			nc.Publish("events.contract.settled", eventData)
			log.Printf("💰 Contract Settled: %s", contract.ID)
		}
	})

	// Handler: Raw Samsara Ingest (Replaces Oracle Gateway)
	nc.Subscribe("raw.ingest.samsara", func(msg *nats.Msg) {
		// 1. Parse Raw Webhook
		// We define the struct inline or reuse a shared one if available.
		// Matching oracle-gateway's expectation:
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
		// Hive Engine acts as the trusted oracle/signer here.
		proof := tap.Proof{
			TaskID:    hook.TaskID,
			Type:      tap.ProofGPS, // Mapping /ingest/samsara to GPS proof type
			Timestamp: time.Now().Unix(),
			Data:      hook.Data,
			Signature: "sig_hive_converted_123", // Signed by Hive (as Oracle)
		}

		// 3. Publish to Proof Funnel
		proofBytes, _ := json.Marshal(proof)
		if err := nc.Publish("proof.submit", proofBytes); err != nil {
			log.Printf("Error publishing proof: %v", err)
			return
		}

		log.Printf("Proof Submitted for Task %s", hook.TaskID)
	})

	log.Println("🏛️  Settlement Engine Running (Redis Backed)...")

	// Wait
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("🔻 Shutting down Settlement Engine...")
}
