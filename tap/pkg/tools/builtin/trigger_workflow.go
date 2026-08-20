package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/google/uuid"
)

// TriggerWorkflowTool allows the Dynamic Agent to initiate an orchestrator workflow pipeline
// by publishing a trigger event to the workflow's trigger topic.
type TriggerWorkflowTool struct {
	bus    core.EventBus
	logger *slog.Logger
}

func NewTriggerWorkflowTool(bus core.EventBus, logger *slog.Logger) tools.Tool {
	return &TriggerWorkflowTool{
		bus:    bus,
		logger: logger,
	}
}

func (t *TriggerWorkflowTool) Name() string {
	return "trigger_workflow"
}

func (t *TriggerWorkflowTool) Description() string {
	return "Trigger and run a workflow orchestration pipeline (e.g. 'events.accounting.1.test_dag' or 'events.accounting.1.pcm_bookkeeping'). " +
		"Use this tool to start a workflow after inspecting available workflows with get_user_workflows."
}

func (t *TriggerWorkflowTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"trigger_topic": {
				"type": "string",
				"description": "The exact NATS trigger topic of the workflow (e.g. 'events.accounting.1.test_dag', 'events.accounting.1.pcm_bookkeeping')."
			},
			"workflow_name": {
				"type": "string",
				"description": "Optional human-readable name of the workflow (e.g. 'DAG Test Workflow', 'PCM Bookkeeping Workflow')."
			},
			"document_ids": {
				"type": "array",
				"items": { "type": "string" },
				"description": "List of document UUIDs from attachments to pass into the workflow."
			},
			"session_id": {
				"type": "string",
				"description": "Conversation/Workflow session ID."
			},
			"entity_id": {
				"type": "string",
				"description": "Entity/Tenant ID."
			},
			"subject": {
				"type": "string",
				"description": "Subject line of the triggering request or email."
			},
			"body_text": {
				"type": "string",
				"description": "Body text or instructions of the triggering request."
			},
			"params": {
				"type": "object",
				"description": "Optional extra execution parameters for the workflow."
			}
		},
		"required": ["trigger_topic"]
	}`)
}

func (t *TriggerWorkflowTool) Call(ctx context.Context, input map[string]any) (string, error) {
	topic, _ := input["trigger_topic"].(string)
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return "", fmt.Errorf("trigger_topic is required")
	}

	if t.bus == nil {
		return "", fmt.Errorf("event bus not available to trigger workflow")
	}

	// Extract session ID and entity ID from context if not provided
	sessionID, _ := input["session_id"].(string)
	if sessionID == "" {
		if sessFromCtx, ok := ctx.Value(tools.SessionIDKey{}).(string); ok {
			sessionID = sessFromCtx
		}
	}
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	entityID, _ := input["entity_id"].(string)
	if entityID == "" {
		if entFromCtx, ok := ctx.Value(tools.EntityIDKey{}).(string); ok {
			entityID = entFromCtx
		}
	}

	// Extract document_ids
	var docIDs []string
	if rawDocs, ok := input["document_ids"].([]any); ok {
		for _, d := range rawDocs {
			if s, ok := d.(string); ok && s != "" {
				docIDs = append(docIDs, s)
			}
		}
	}

	payload := make(map[string]any)

	// Merge inbound context payload fields (from_handle, to_handle, reply_to, agent_alias, external_id, attachments, etc.)
	if inboundMap, ok := ctx.Value(tools.InboundTaskPayloadKey{}).(map[string]any); ok {
		for k, v := range inboundMap {
			if v != nil {
				payload[k] = v
			}
		}
	}

	payload["session_id"] = sessionID
	payload["entity_id"] = entityID
	if subj, ok := input["subject"].(string); ok && subj != "" {
		payload["subject"] = subj
	}
	if body, ok := input["body_text"].(string); ok && body != "" {
		payload["body_text"] = body
	}
	if len(docIDs) > 0 {
		payload["document_ids"] = docIDs
	}
	payload["triggered_at"] = time.Now().Format(time.RFC3339)

	if extraParams, ok := input["params"].(map[string]any); ok {
		for k, v := range extraParams {
			payload[k] = v
		}
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal trigger payload: %w", err)
	}
	t.logger.Info("Triggering workflow", "TRIGGER_PAYLOAD", payload)

	if err := t.bus.Publish(topic, payloadBytes); err != nil {
		if t.logger != nil {
			t.logger.Error("failed to publish workflow trigger event", "topic", topic, "error", err)
		}
		return "", fmt.Errorf("failed to publish trigger event to %s: %w", topic, err)
	}

	if t.logger != nil {
		t.logger.Info("successfully triggered workflow", "topic", topic, "session_id", sessionID, "document_count", len(docIDs))
	}

	return fmt.Sprintf("Workflow triggered successfully on topic '%s' with session_id '%s' and %d document(s).", topic, sessionID, len(docIDs)), nil
}

func init() {
	Register("trigger_workflow", func(env core.Environment, logger *slog.Logger) tools.Tool {
		return NewTriggerWorkflowTool(env.Bus, logger)
	})
}
