package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
)

const (
	triggerTopic = "events.accounting.1.pcm_workflow"
)

type Attachment struct {
	ContentLength int    `json:"content_length"`
	ContentType   string `json:"content_type"`
	DocumentID    string `json:"document_id"`
	Name          string `json:"name"`
	S3Key         string `json:"s3_key"`
	SHA256        string `json:"sha256"`
}

type TriggerPayload struct {
	AgentAlias     string       `json:"agent_alias"`
	Attachments    []Attachment `json:"attachments"`
	BodyText       string       `json:"body_text"`
	DocumentIDs    []string     `json:"document_ids"`
	EntityID       string       `json:"entity_id"`
	ExternalID     string       `json:"external_id"`
	FromHandle     string       `json:"from_handle"`
	ReplyTo        string       `json:"reply_to"`
	RequestedAgent string       `json:"requested_agent"`
	SessionID      string       `json:"session_id"`
	Subject        string       `json:"subject"`
	ToHandle       string       `json:"to_handle"`
	TriggeredAt    string       `json:"triggered_at"`
}

func main() {
	logger := slog.New(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}),
	)

	logger.Info(
		"starting TriggerWorkflowTool replay",
		"trigger_topic",
		triggerTopic,
	)

	if os.Getenv("NATS_URL") == "" {
		os.Setenv("NATS_URL", "nats://localhost:4222")
	}

	cfg, _, err := config.Load()
	if err != nil {
		logger.Error(
			"failed to load config",
			"error",
			err,
		)
		os.Exit(1)
	}

	nc, err := nats.Connect(
		cfg.NATS.URL,
		nats.Name("test-trigger-workflow-replay"),
		nats.Timeout(5*time.Second),
		nats.PingInterval(20*time.Second),
		nats.MaxPingsOutstanding(3),
		nats.ReconnectWait(time.Second),
		nats.MaxReconnects(3),
	)
	if err != nil {
		logger.Error(
			"failed to connect to NATS",
			"url",
			cfg.NATS.URL,
			"error",
			err,
		)
		os.Exit(1)
	}
	defer nc.Close()

	payload := TriggerPayload{
		AgentAlias: "sarah",

		Attachments: []Attachment{
			{
				ContentLength: 2431,
				ContentType:   "application/pdf",
				DocumentID:    "6d2f765a-2422-404e-a75d-a94c5a14b7d9",
				Name:          "releve_bancaire.pdf",
				S3Key:         "ca7ebd8a-0d48-4a74-a429-7b764f96f22d-releve_bancaire.pdf",
				SHA256:        "5e46b256ee01d073e80f779dbd24ff7fc1b1c9cc53717f60d1e2bd7279d6056b",
			},
		},

		BodyText: "Run PCM Workflow",

		DocumentIDs: []string{
			"6d2f765a-2422-404e-a75d-a94c5a14b7d9",
		},

		EntityID:       "a7f28377-579e-442e-b46e-db676f044caa",
		ExternalID:     "c7ff8bcb-1fcd-4e7e-af3d-088c775a106e",
		FromHandle:     "yankz@fignode.com",
		ReplyTo:        "",
		RequestedAgent: "dynamic-agent",
		SessionID:      "c8d416ac-69f2-429c-ac0c-9956e145fe9f",
		Subject:        "Run payload test",
		ToHandle:       "sarah@a.usetoro.io",

		// Exact timestamp from captured TriggerWorkflowTool.Call().
		TriggeredAt: "2026-08-19T15:31:43Z",
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		logger.Error(
			"failed to marshal trigger payload",
			"error",
			err,
		)
		os.Exit(1)
	}

	// Log it using the same semantic field as TriggerWorkflowTool.Call().
	logger.Info(
		"Triggering workflow",
		"trigger_topic",
		triggerTopic,
		"TRIGGER_PAYLOAD",
		payload,
	)

	msg := nats.NewMsg(triggerTopic)
	msg.Data = payloadBytes

	if err := nc.PublishMsg(msg); err != nil {
		logger.Error(
			"failed to publish workflow trigger",
			"trigger_topic",
			triggerTopic,
			"error",
			err,
		)
		os.Exit(1)
	}

	if err := nc.FlushTimeout(5 * time.Second); err != nil {
		logger.Error(
			"failed to flush NATS connection",
			"error",
			err,
		)
		os.Exit(1)
	}

	if err := nc.LastError(); err != nil {
		logger.Error(
			"NATS connection reported an error",
			"error",
			err,
		)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("=======================================================")
	fmt.Println("  ✅ TRIGGER WORKFLOW EVENT REPLAYED")
	fmt.Printf("  Topic:      %s\n", triggerTopic)
	fmt.Printf("  Session ID: %s\n", payload.SessionID)
	fmt.Printf("  Entity ID:  %s\n", payload.EntityID)
	fmt.Printf("  Documents:  %d\n", len(payload.DocumentIDs))
	fmt.Printf("  Bytes:      %d\n", len(payloadBytes))
	fmt.Println("=======================================================")
}
