package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
)

// EmailTool allows an agent to proactively send an email or reply to one.
// It publishes an INFORM envelope containing a core.Proof to `proof.outgoing.chat`.
type EmailTool struct {
	Bus      core.EventBus
	Logger   *slog.Logger
	AgentDID string
	AgentName string
}

func (t *EmailTool) Name() string {
	return "SendEmail"
}

func (t *EmailTool) Description() string {
	name := t.AgentName
	if name == "" {
		name = "Sarah"
	}
	return fmt.Sprintf("Send an email to a specific recipient. Use this to proactively reach out to clients, vendors, or CPAs when you need information or to notify them of something. IMPORTANT: You are acting as '%s', a virtual employee. Write the email from %s's perspective, using a polite and professional tone, and sign off as %s. The subject must be a brief, specific summary of the email's purpose — never use the agent name or a generic placeholder.", name, name, name)
}

func (t *EmailTool) InputSchema() json.RawMessage {
	name := t.AgentName
	if name == "" {
		name = "Sarah"
	}
	schemaStr := fmt.Sprintf(`{
		"type": "object",
		"properties": {
			"to": {
				"type": "string",
				"description": "The recipient email address"
			},
			"subject": {
				"type": "string",
				"description": "A brief, specific subject line summarizing the email's purpose (e.g., 'Invoice #1234 Payment Confirmation', 'Q2 Tax Document Request'). Do NOT use the agent name or generic text like 'General Purpose Agent'."
			},
			"body": {
				"type": "string",
				"description": "The plain text body of the email. Write this as '%s', the virtual employee. Use a polite tone and sign off as %s."
			}
		},
		"required": ["to", "subject", "body"]
	}`, name, name)
	return json.RawMessage(schemaStr)
}

func (t *EmailTool) Call(ctx context.Context, input map[string]any) (string, error) {
	to, _ := input["to"].(string)
	if to == "" {
		return "", fmt.Errorf("to address is required")
	}

	subject, _ := input["subject"].(string)
	if subject == "" {
		return "", fmt.Errorf("subject is required")
	}

	body, _ := input["body"].(string)
	if body == "" {
		return "", fmt.Errorf("body is required")
	}

	entityIDStr, _ := ctx.Value(tools.EntityIDKey{}).(string)
	if entityIDStr == "" {
		return "", fmt.Errorf("missing entity_id in context, cannot send email without knowing firm identity")
	}

	// We use AgentName as from_handle so omni_chat_worker can look up the correct alias
	fromHandle := t.AgentName
	if fromHandle == "" {
		fromHandle = "agent"
	}

	outProof := core.Proof{
		Type:      core.ProofAPI,
		Timestamp: time.Now().Unix(),
	}

	dataMap := map[string]any{
		"body_text":   body,
		"source":      "email",
		"from_handle": fromHandle,
		"to_handle":   to,
		"subject":     subject,
		"entity_id":   entityIDStr,
	}

	if sessionID, ok := ctx.Value(tools.SessionIDKey{}).(string); ok && sessionID != "" {
		dataMap["session_id"] = sessionID
	}
	
	if messageID, ok := ctx.Value(tools.MessageIDKey{}).(string); ok && messageID != "" {
		dataMap["in_reply_to"] = messageID
	}

	dataBytes, err := json.Marshal(dataMap)
	if err != nil {
		return "", fmt.Errorf("failed to marshal proof data: %w", err)
	}
	outProof.Data = dataBytes

	proofBytes, err := json.Marshal(outProof)
	if err != nil {
		return "", fmt.Errorf("failed to marshal proof: %w", err)
	}

	outEnv := core.Envelope{
		ID:           uuid.New().String(),
		Timestamp:    time.Now(),
		SenderDID:    t.AgentDID,
		ReceiverDID:  "did:toro:worker:omni_chat",
		Performative: core.INFORM,
		Body:         proofBytes,
	}

	outBytes, err := json.Marshal(outEnv)
	if err != nil {
		return "", fmt.Errorf("failed to marshal envelope: %w", err)
	}

	if err := t.Bus.Publish("proof.outgoing.chat", outBytes); err != nil {
		return "", fmt.Errorf("failed to publish to proof.outgoing.chat: %w", err)
	}

	t.Logger.Info("Sent email via SendEmail tool", "to", to, "subject", subject)
	return fmt.Sprintf("Successfully sent email to %s with subject '%s'", to, subject), nil
}

func init() {
	Register("SendEmail", func(env core.Environment, logger *slog.Logger) tools.Tool {
		name := env.Config.Name
		if name == "General Purpose Agent" || name == "" {
			name = "Sarah"
		}
		return &EmailTool{
			Bus:       env.Bus,
			Logger:    logger,
			AgentDID:  env.Config.DID,
			AgentName: name,
		}
	})
}
