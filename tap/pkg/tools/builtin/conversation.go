package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
)

// ConversationStateTool lets an agent update the conversation session
// status (e.g. mark as resolved, awaiting reply, or escalated).
type ConversationStateTool struct {
	Bus      core.EventBus
	Logger   *slog.Logger
	AgentDID string
}

func (t *ConversationStateTool) Name() string        { return "ConversationState" }
func (t *ConversationStateTool) Description() string { return "Update the current conversation session status. Use this to mark a conversation as resolved when the user's request is complete, or mark it as awaiting_reply when you've asked a question and need to wait for a response." }
func (t *ConversationStateTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"status": {"type": "string", "enum": ["resolved", "awaiting_reply", "escalated", "active"], "description": "The new status for the conversation session."},
			"reason": {"type": "string", "description": "Optional human-readable reason for the status change."}
		},
		"required": ["status"]
	}`)
}

func (t *ConversationStateTool) Call(ctx context.Context, input map[string]any) (string, error) {
	status, _ := input["status"].(string)
	if status == "" {
		return "", fmt.Errorf("status is required")
	}

	reason, _ := input["reason"].(string)

	payload := map[string]interface{}{
		"status": status,
		"reason": reason,
	}
	payloadBytes, _ := json.Marshal(payload)

	subject, err := core.BuildWorkerInboxFromActivity("workers.conversation_scheduler")
	if err != nil {
		return "", fmt.Errorf("derive scheduler inbox: %w", err)
	}

	envlp := core.Envelope{
		ID:           uuid.New().String(),
		SenderDID:    t.AgentDID,
		ReceiverDID:  "did:toro:worker:conversation_scheduler",
		Performative: core.REQUEST,
		Body:         payloadBytes,
	}
	envlpBytes, _ := json.Marshal(envlp)

	if err := t.Bus.Publish(subject, envlpBytes); err != nil {
		return "", fmt.Errorf("publish conversation update: %w", err)
	}

	t.Logger.Info("conversation state updated", "status", status, "reason", reason)
	return fmt.Sprintf("Conversation status updated to '%s'.", status), nil
}

// ScheduleReminderTool lets an agent schedule a delayed follow-up
// message. The scheduler worker will publish the message back to the ingress at the specified time.
type ScheduleReminderTool struct {
	Bus      core.EventBus
	Logger   *slog.Logger
	AgentDID string
}

func (t *ScheduleReminderTool) Name() string        { return "ScheduleReminder" }
func (t *ScheduleReminderTool) Description() string { return "Schedule a follow-up reminder to be sent after a delay. Use this when waiting for a client response and you want to follow up automatically. The reminder will re-trigger the conversation with context." }
func (t *ScheduleReminderTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"delay": {"type": "string", "description": "Delay duration (e.g., '72h', '3d', '1w'). Max 30 days."},
			"message": {"type": "string", "description": "The reminder message to send when the timer fires."}
		},
		"required": ["delay", "message"]
	}`)
}

func (t *ScheduleReminderTool) Call(ctx context.Context, input map[string]any) (string, error) {
	delay, _ := input["delay"].(string)
	message, _ := input["message"].(string)
	if delay == "" || message == "" {
		return "", fmt.Errorf("delay and message are required")
	}

	payload := map[string]interface{}{
		"action":  "schedule_reminder",
		"delay":   delay,
		"message": message,
	}
	payloadBytes, _ := json.Marshal(payload)

	subject, err := core.BuildWorkerInboxFromActivity("workers.conversation_scheduler")
	if err != nil {
		return "", fmt.Errorf("derive scheduler inbox: %w", err)
	}

	envlp := core.Envelope{
		ID:           uuid.New().String(),
		SenderDID:    t.AgentDID,
		ReceiverDID:  "did:toro:worker:conversation_scheduler",
		Performative: core.REQUEST,
		Body:         payloadBytes,
	}
	envlpBytes, _ := json.Marshal(envlp)

	if err := t.Bus.Publish(subject, envlpBytes); err != nil {
		return "", fmt.Errorf("publish schedule reminder: %w", err)
	}

	t.Logger.Info("reminder scheduled", "delay", delay)
	return fmt.Sprintf("Reminder scheduled in %s.", delay), nil
}
