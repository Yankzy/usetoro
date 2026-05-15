package main

import (
	"encoding/json"
	"flag"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func main() {
	natsURL := flag.String("nats", "nats://localhost:4222", "NATS server URL")
	topic := flag.String("topic", "events.accounting.10.categorize_batch", "NATS topic to publish to")
	entityID := flag.String("entity", "00000000-0000-0000-0000-000000000000", "Entity ID (UUID)")
	flag.Parse()

	// Connect to NATS
	nc, err := nats.Connect(*natsURL)
	if err != nil {
		log.Fatalf("❌ Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	// 1. Prepare the inner payload (the rows + entity_id)
	rows := []map[string]interface{}{
		{
			"id":          "test_tx_001",
			"date":        time.Now().Format("2006-01-02"),
			"description": "Amazon.com*12345",
			"amount":      -125.50,
			"vendor":      "Amazon",
		},
		{
			"id":          "test_tx_002",
			"date":        time.Now().Format("2006-01-02"),
			"description": "STRIPE PAYOUT",
			"amount":      2500.00,
			"vendor":      "Stripe",
		},
	}

	innerPayload := map[string]interface{}{
		"entity_id": *entityID,
		"rows":      rows,
	}

	innerBytes, err := json.Marshal(innerPayload)
	if err != nil {
		log.Fatalf("❌ Failed to marshal inner payload: %v", err)
	}

	// 2. Wrap in TaskDefinition
	taskDef := core.TaskDefinition{
		ID:         uuid.New().String(),
		Domain:     "accounting",
		Complexity: core.ComplexityEntry,
		Payload:    json.RawMessage(innerBytes),
	}

	// 3. Wrap in Envelope
	env, err := core.NewEnvelope(
		uuid.New().String(),
		"did:toro:manual-trigger",
		"did:toro:orchestrator",
		uuid.New().String(),
		core.INFORM, // Triggers often use INFORM or REQUEST
		taskDef,
	)
	if err != nil {
		log.Fatalf("❌ Failed to create envelope: %v", err)
	}

	data, err := json.Marshal(env)
	if err != nil {
		log.Fatalf("❌ Failed to marshal envelope: %v", err)
	}

	log.Printf("🚀 Launching Batch Workflow on topic: %s", *topic)
	log.Printf("🏢 Entity ID: %s", *entityID)

	if err := nc.Publish(*topic, data); err != nil {
		log.Fatalf("❌ Failed to publish: %v", err)
	}

	log.Println("✅ Successfully published trigger event!")
}
