package conversation

import (
	"context"
)

// IngressRequest represents an inbound message from an ingress channel
type IngressRequest struct {
	Prompt         string `json:"prompt"`
	BodyText       string `json:"body_text"`
	EntityID       string `json:"entity_id"`
	SystemPrompt   string `json:"system_prompt,omitempty"`
	AgentAlias     string `json:"agent_alias,omitempty"`
	AgentName      string `json:"agent_name,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	InReplyTo      string `json:"in_reply_to,omitempty"`
	MessageID      string `json:"message_id,omitempty"`
	FromHandle     string `json:"from_handle,omitempty"`
	ToHandle       string `json:"to_handle,omitempty"`
	Source         string `json:"source,omitempty"`
	Subject        string `json:"subject,omitempty"`
	HasAttachments bool   `json:"has_attachments,omitempty"`
}

// IngressInterceptor allows hooking into the ingress pipeline to modify
// the prompt or bypass AI entirely for domain-specific logic.
type IngressInterceptor interface {
	Intercept(ctx context.Context, req *IngressRequest) (bypassed bool, extraSystemPrompt string, err error)
}
