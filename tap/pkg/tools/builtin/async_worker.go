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

// AsyncWorkerTool dispatches tasks to workers over JetStream without blocking the LLM.
// It is dynamically instantiated from defaults.yml definitions.
type AsyncWorkerTool struct {
	Bus          core.EventBus
	AgentDID     string
	ToolName     string
	ToolDesc     string
	Schema       json.RawMessage
	ActivityType string
}

func (t *AsyncWorkerTool) Name() string { return t.ToolName }
func (t *AsyncWorkerTool) Description() string { return t.ToolDesc }
func (t *AsyncWorkerTool) InputSchema() json.RawMessage { return t.Schema }

func (t *AsyncWorkerTool) Call(ctx context.Context, input map[string]any) (string, error) {
	// Inject the agent's inbox so the worker knows where to send the result directly.
	input["return_subject"] = core.BuildAgentInbox(t.AgentDID)

	payloadBytes, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	convID, _ := ctx.Value(tools.ConversationIDKey{}).(string)
	if convID == "" {
		convID = uuid.New().String()
	}

	targetSubject, err := core.BuildWorkerInboxFromActivity(t.ActivityType)
	if err != nil {
		return "", fmt.Errorf("failed to derive target subject: %w", err)
	}

	reqEnv := core.Envelope{
		ID:             uuid.New().String(),
		Timestamp:      time.Now(),
		SenderDID:      t.AgentDID,
		ReceiverDID:    "did:toro:worker", // Generic
		Performative:   core.REQUEST,
		ConversationID: convID,
		Body:           payloadBytes,
	}

	reqBytes, err := json.Marshal(reqEnv)
	if err != nil {
		return "", fmt.Errorf("failed to marshal envelope: %w", err)
	}

	if err := t.Bus.Publish(targetSubject, reqBytes); err != nil {
		return "", fmt.Errorf("failed to dispatch worker task: %w", err)
	}

	return fmt.Sprintf("Task dispatched asynchronously to %s. I will suspend execution and wait. You will receive an INFORM message when the task completes.", t.ToolName), nil
}
