package main

// trigger.go — integration test harness for the intent-extractor-agent.
//
// Invoked via main.go when the --trigger flag is the first argument:
//
//	NATS_URL=nats://localhost:4222 go run ./tap/cmd/intent-extractor/ --trigger
//	NATS_URL=nats://localhost:4222 go run ./tap/cmd/intent-extractor/ --trigger \
//	    -phone "+12025550147" \
//	    -text  "Hi, I need a quote for about 2400 sqft of shingles ASAP"
//
// Flow:
//  1. Connects to NATS.
//  2. Subscribes to orchestrator.inbox to capture the INFORM proof.
//  3. Publishes a CFP envelope to tasks.intent.1.classify.
//  4. Waits up to 30 s, then pretty-prints the extracted intent JSON.

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// triggerPayload mirrors intentextractor.taskPayload — kept local so this file
// has zero dependency on the agent package.
type triggerPayload struct {
	Text        string `json:"text"`
	PhoneNumber string `json:"phone_number"`
}

func runTrigger() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	textFlag := flag.String("text", "Hi, I need a quote for about 2400 sqft of shingles — it's kind of an emergency", "SMS text to classify")
	phoneFlag := flag.String("phone", "+12025550100", "Sender phone number")
	timeoutFlag := flag.Duration("timeout", 30*time.Second, "How long to wait for the proof reply")
	flag.Parse()

	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}

	nc, err := nats.Connect(natsURL,
		nats.Name("intent-extractor-trigger"),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		logger.Error("failed to connect to NATS", "url", natsURL, "error", err)
		os.Exit(1)
	}
	defer nc.Drain()
	logger.Info("✅ Connected to NATS", "url", natsURL)

	// Subscribe BEFORE publishing so we never miss the reply.
	proofCh := make(chan *nats.Msg, 1)
	sub, err := nc.Subscribe("orchestrator.inbox", func(msg *nats.Msg) {
		proofCh <- msg
	})
	if err != nil {
		logger.Error("failed to subscribe to orchestrator.inbox", "error", err)
		os.Exit(1)
	}
	defer sub.Unsubscribe()

	conversationID := uuid.New().String()
	taskID := uuid.New().String()

	rawPayload, err := json.Marshal(triggerPayload{
		Text:        *textFlag,
		PhoneNumber: *phoneFlag,
	})
	if err != nil {
		logger.Error("failed to marshal payload", "error", err)
		os.Exit(1)
	}

	task := core.TaskDefinition{
		ID:         taskID,
		Domain:     "intent",
		Complexity: core.ComplexityEntry,
		Reward:     core.RewardEntryMicrions,
		Currency:   core.DefaultTaskCurrency,
		Payload:    rawPayload,
		ExpiresAt:  time.Now().Add(60 * time.Second).Unix(),
	}

	cfpEnv, err := core.NewEnvelope(
		uuid.New().String(),
		"did:toro:trigger:integration-test",
		"did:toro:agent:intent_extractor",
		conversationID,
		core.CFP,
		task,
	)
	if err != nil {
		logger.Error("failed to create CFP envelope", "error", err)
		os.Exit(1)
	}

	cfpBytes, err := json.Marshal(cfpEnv)
	if err != nil {
		logger.Error("failed to marshal CFP envelope", "error", err)
		os.Exit(1)
	}

	// tasks.intent.1.classify — derived from activity_type "agents.intent.classify" at complexity 1.
	const taskQueue = "tasks.intent.1.classify"
	if err := nc.Publish(taskQueue, cfpBytes); err != nil {
		logger.Error("failed to publish CFP", "error", err)
		os.Exit(1)
	}

	logger.Info("📤 CFP published",
		"topic", taskQueue,
		"conversation_id", conversationID,
		"task_id", taskID,
		"phone", *phoneFlag,
		"text", *textFlag,
	)
	logger.Info("⏳ Waiting for INFORM proof on orchestrator.inbox …", "timeout", *timeoutFlag)

	select {
	case msg := <-proofCh:
		logger.Info("📨 Received message on orchestrator.inbox")
		triggerPrintProof(logger, msg.Data)

	case <-time.After(*timeoutFlag):
		logger.Error("⏰ Timed out — is the intent-extractor agent running?",
			"hint", fmt.Sprintf("NATS_URL=%s go run ./tap/cmd/intent-extractor/", natsURL),
		)
		os.Exit(1)
	}
}

func triggerPrintProof(logger *slog.Logger, data []byte) {
	var env core.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		logger.Error("failed to parse INFORM envelope", "error", err)
		fmt.Printf("raw: %s\n", data)
		return
	}

	logger.Info("📋 INFORM Envelope",
		"id", env.ID,
		"performative", env.Performative,
		"sender", env.SenderDID,
		"conversation_id", env.ConversationID,
	)

	var proof core.Proof
	if err := json.Unmarshal(env.Body, &proof); err != nil {
		logger.Warn("body is not a Proof, printing raw", "error", err)
		fmt.Printf("\nbody (raw):\n%s\n", triggerPrettyJSON(env.Body))
		return
	}

	logger.Info("🏆 Proof",
		"task_id", proof.TaskID,
		"type", proof.Type,
		"timestamp", time.Unix(proof.Timestamp, 0).Format(time.RFC3339),
	)

	fmt.Printf("\n─── Extracted Intent (Raw) ─────────────────────────────\n")
	fmt.Println(triggerPrettyJSON(proof.Data))
	fmt.Printf("────────────────────────────────────────────────────────────\n")
}

func triggerPrettyJSON(raw json.RawMessage) string {
	var buf any
	if err := json.Unmarshal(raw, &buf); err != nil {
		return string(raw)
	}
	b, err := json.MarshalIndent(buf, "", "  ")
	if err != nil {
		return string(raw)
	}
	return string(b)
}
