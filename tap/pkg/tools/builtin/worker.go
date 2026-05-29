package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
)

// CallWorkerTool allows an LLM agent to directly execute a deterministic worker pipeline
// over NATS Request/Reply, bypassing the Orchestrator for synchronous execution.
type CallWorkerTool struct {
	bus core.EventBus
}

func NewCallWorkerTool(bus core.EventBus) tools.Tool {
	return &CallWorkerTool{
		bus: bus,
	}
}

func (t *CallWorkerTool) Name() string {
	return "CallWorker"
}

func (t *CallWorkerTool) Description() string {
	return "Execute a specialized, deterministic background worker synchronously and return its output. Supply the worker's 'activity_type' (e.g. 'workers.database.insert_rows') and a generic JSON 'payload'."
}

func (t *CallWorkerTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"activity_type": {
				"type": "string",
				"description": "The exact activity_type of the worker to invoke."
			},
			"payload": {
				"type": "object",
				"description": "The JSON payload to send to the worker."
			}
		},
		"required": ["activity_type", "payload"]
	}`)
}

func (t *CallWorkerTool) Call(ctx context.Context, input map[string]any) (string, error) {
	activityType, _ := input["activity_type"].(string)
	if activityType == "" {
		return "", fmt.Errorf("activity_type is required")
	}

	payloadRaw, ok := input["payload"]
	if !ok {
		return "", fmt.Errorf("payload is required")
	}

	payloadBytes, err := json.Marshal(payloadRaw)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Retrieve context variables
	convID, _ := ctx.Value(tools.ConversationIDKey{}).(string)
	if convID == "" {
		convID = uuid.New().String()
	}

	// 1. Derive Worker Inbox
	targetSubject, err := core.BuildWorkerInboxFromActivity(activityType)
	if err != nil {
		return "", fmt.Errorf("failed to derive target subject: %w", err)
	}

	// 2. Wrap Request in TAP Envelope
	reqEnv := core.Envelope{
		ID:             uuid.New().String(),
		Timestamp:      time.Now(),
		SenderDID:      "did:toro:call-worker-tool",
		ReceiverDID:    "did:toro:worker", // Generic
		Performative:   core.REQUEST,
		ConversationID: convID,
		Body:           payloadBytes,
	}

	reqBytes, err := json.Marshal(reqEnv)
	if err != nil {
		return "", fmt.Errorf("failed to marshal envelope: %w", err)
	}

	// 3. Make Synchronous NATS Request
	// The worker must be updated to route its reply to msg.Reply!
	replyMsg, err := t.bus.RequestWithContext(ctx, targetSubject, reqBytes)
	if err != nil {
		return "", fmt.Errorf("worker execution failed or timed out: %w", err)
	}

	// 4. Unwrap Response Envelope
	var replyEnv map[string]interface{}
	if err := json.Unmarshal(replyMsg.Data, &replyEnv); err != nil {
		return "", fmt.Errorf("failed to parse worker reply envelope: %w", err)
	}

	// Extract the actual inner payload (usually 'body' or 'data')
	if body, exists := replyEnv["body"]; exists {
		bodyBytes, _ := json.Marshal(body)
		return string(bodyBytes), nil
	}

	return string(replyMsg.Data), nil
}
