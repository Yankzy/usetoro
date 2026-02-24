package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/tap/pkg/contract"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/identity"
	"github.com/Yankzy/usetoro/tap/pkg/store"
	"github.com/Yankzy/usetoro/tap/pkg/transport"
)

const defaultStreamName = "SETTLEMENT"

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Configuration Loading Failed: %v", err)
	}

	// 1. Load oracle keypair — signs Samsara-converted proofs.
	oracleKP := loadOracleKeyPair()
	log.Printf("Oracle DID: %s", identity.CreateDID(oracleKP.Public))

	// 2. Connect to NATS
	url := cfg.NATS.URL
	if url == "" {
		url = nats.DefaultURL
	}
	nc, err := transport.Connect(url)
	if err != nil {
		log.Fatalf("❌ Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	js, err := transport.JetStream(nc)
	if err != nil {
		log.Fatalf("❌ Failed to init JetStream: %v", err)
	}

	// 3. Connect to Redis
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("❌ Invalid REDIS_URL: %v", err)
	}
	rdb := redis.NewClient(opt)

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		log.Fatalf("❌ Failed to connect to Redis: %v", err)
	}

	// 4. Dependencies
	repo := store.NewRedisStore(rdb)
	machine := contract.NewContract(repo)

	// 5. Subscribe via JetStream durable consumers (at-least-once delivery)
	settlementConfig, ok := cfg.NATS.Services["settlement"]
	if !ok {
		log.Fatalf("❌ Settlement service configuration not found")
	}

	streamName := settlementConfig.StreamName
	if streamName == "" {
		streamName = defaultStreamName
	}

	if err := ensureStream(js, streamName, &settlementConfig); err != nil {
		log.Fatalf("❌ Failed to ensure settlement stream: %v", err)
	}

	for _, subject := range settlementConfig.JetStream.Subjects {
		s := subject
		durable := "toro-settlement-" + strings.ReplaceAll(s, ".", "-")
		log.Printf("Listening on subject: %s", s)

		handler := buildHandler(s, machine, nc, oracleKP)
		if err := subscribeWithRetry(js, streamName, s, durable, handler); err != nil {
			log.Fatalf("❌ Failed to subscribe to %s: %v", s, err)
		}
	}

	log.Println("🏛️  Settlement Engine Running (JetStream + Redis Backed)...")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("🔻 Shutting down Settlement Engine...")
}

// loadOracleKeyPair loads the settlement engine's Ed25519 oracle keypair from
// ORACLE_PRIVATE_KEY_HEX. If unset, an ephemeral keypair is generated (dev only).
func loadOracleKeyPair() *identity.KeyPair {
	privHex := os.Getenv("ORACLE_PRIVATE_KEY_HEX")
	if privHex != "" {
		privBytes, err := hex.DecodeString(privHex)
		if err != nil || len(privBytes) != ed25519.PrivateKeySize {
			log.Fatalf("❌ ORACLE_PRIVATE_KEY_HEX must be %d hex-encoded bytes (Ed25519 private key)", ed25519.PrivateKeySize)
		}
		priv := ed25519.PrivateKey(privBytes)
		return &identity.KeyPair{Public: priv.Public().(ed25519.PublicKey), Private: priv}
	}
	kp, err := identity.GenerateKeyPair()
	if err != nil {
		log.Fatalf("❌ Failed to generate oracle keypair: %v", err)
	}
	log.Printf("⚠️  ORACLE_PRIVATE_KEY_HEX unset — ephemeral oracle DID: %s", identity.CreateDID(kp.Public))
	return kp
}

// ensureStream creates the JetStream stream if it doesn't already exist.
func ensureStream(js nats.JetStreamContext, name string, cfg *config.ServiceConfig) error {
	streamCfg := &nats.StreamConfig{
		Name:        name,
		Subjects:    cfg.JetStream.Subjects,
		MaxAge:      cfg.JetStream.MaxAge,
		Replicas:    cfg.JetStream.Replicas,
		DenyDelete:  cfg.JetStream.DenyDelete,
		DenyPurge:   cfg.JetStream.DenyPurge,
		AllowRollup: cfg.JetStream.AllowRollup,
		AllowDirect: cfg.JetStream.AllowDirect,
	}
	if _, err := js.AddStream(streamCfg); err != nil {
		// If the stream already exists, that's fine.
		if _, infoErr := js.StreamInfo(name); infoErr != nil {
			return fmt.Errorf("stream create: %v; stream info: %w", err, infoErr)
		}
	}
	return nil
}

// subscribeWithRetry subscribes to a JetStream subject with a durable consumer,
// recreating the consumer if a configuration mismatch is detected.
func subscribeWithRetry(js nats.JetStreamContext, streamName, subject, durable string, handler nats.MsgHandler) error {
	_, err := js.Subscribe(subject, handler, nats.Durable(durable), nats.ManualAck())
	if err == nil {
		return nil
	}
	if !strings.Contains(err.Error(), "consumer already exists") &&
		!strings.Contains(err.Error(), "name already in use") &&
		!strings.Contains(err.Error(), "subject does not match") {
		return err
	}
	log.Printf("⚠️  Consumer mismatch for %s, recreating...", durable)
	if delErr := js.DeleteConsumer(streamName, durable); delErr != nil {
		return fmt.Errorf("delete consumer %s: %w", durable, delErr)
	}
	_, err = js.Subscribe(subject, handler, nats.Durable(durable), nats.ManualAck())
	return err
}

// buildHandler returns the JetStream message handler for a given subject.
func buildHandler(subject string, machine *contract.Machine, nc *nats.Conn, oracleKP *identity.KeyPair) nats.MsgHandler {
	switch subject {
	case "contracts.request":
		return func(msg *nats.Msg) { handleContractRequest(msg, machine, nc) }
	case "proof.submit":
		return func(msg *nats.Msg) { handleProofSubmit(msg, machine, nc) }
	case "raw.ingest.samsara":
		return func(msg *nats.Msg) { handleSamsaraIngest(msg, nc, oracleKP) }
	default:
		return func(msg *nats.Msg) {
			log.Printf("Unknown subject: %s", msg.Subject)
			msg.Ack()
		}
	}
}

func handleContractRequest(msg *nats.Msg, machine *contract.Machine, nc *nats.Conn) {
	var req core.Contract
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		log.Printf("Failed to unmarshal contract request: %v", err)
		msg.Nak()
		return
	}

	lockedContract, err := machine.Lock(context.Background(), &req)
	if err != nil {
		log.Printf("Failed to lock contract: %v", err)
		msg.Nak()
		return
	}

	eventData, _ := json.Marshal(lockedContract)
	nc.Publish("events.contract.locked", eventData)
	log.Printf("🔒 Contract Locked: %s", lockedContract.ID)
	msg.Ack()
}

func handleProofSubmit(msg *nats.Msg, machine *contract.Machine, nc *nats.Conn) {
	var proof core.Proof
	if err := json.Unmarshal(msg.Data, &proof); err != nil {
		log.Printf("Failed to unmarshal proof: %v", err)
		msg.Nak()
		return
	}

	settled, settledContract, err := machine.Settle(context.Background(), &proof)
	if err != nil {
		log.Printf("Error processing proof: %v", err)
		msg.Nak()
		return
	}

	if settled {
		eventData, _ := json.Marshal(settledContract)
		nc.Publish("events.contract.settled", eventData)
		log.Printf("💰 Contract Settled: %s", settledContract.ID)
	}
	msg.Ack()
}

func handleSamsaraIngest(msg *nats.Msg, nc *nats.Conn, oracleKP *identity.KeyPair) {
	type WebhookPayload struct {
		SourceID string          `json:"source_id"`
		TaskID   string          `json:"task_id"`
		Data     json.RawMessage `json:"data"`
	}

	var hook WebhookPayload
	if err := json.Unmarshal(msg.Data, &hook); err != nil {
		log.Printf("Error unmarshalling samsara webhook: %v", err)
		msg.Nak()
		return
	}

	log.Printf("Received Webhook for Task %s via Gate", hook.TaskID)

	proof := core.Proof{
		TaskID:    hook.TaskID,
		Type:      core.ProofGPS,
		Timestamp: time.Now().Unix(),
		Data:      hook.Data,
		// Sign the raw proof data with the oracle keypair so Settle can verify it
		// against the contract's AcceptorDID (which must equal the oracle DID).
		Signature: oracleKP.Sign(hook.Data),
	}

	proofBytes, _ := json.Marshal(proof)
	if err := nc.Publish("proof.submit", proofBytes); err != nil {
		log.Printf("Error publishing proof: %v", err)
		msg.Nak()
		return
	}

	log.Printf("Proof Submitted for Task %s", hook.TaskID)
	msg.Ack()
}
