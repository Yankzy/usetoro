package conversation

import (
	"fmt"
	"strings"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
)

// ContextBuilder assembles the full conversational context for the general agent.
type ContextBuilder struct {
	maxHistoryMessages int // cap on messages to include to avoid context overflow
}

func NewContextBuilder() *ContextBuilder {
	return &ContextBuilder{
		maxHistoryMessages: 50,
	}
}

// BuildContext assembles the prompt and system prompt for the general agent
// from a conversation session, its message history, and the new inbound message.
func (b *ContextBuilder) BuildContext(session database.ToroCoreConversationSession, history []database.ToroCoreConversation, newMessage string) BuildResult {
	// Build the system prompt — use session override if set, otherwise default
	systemPrompt := textFromPG(session.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = defaultSystemPrompt()
	}

	// Merge session context into the system prompt
	if len(session.ContextJson) > 0 {
		ctxStr := string(session.ContextJson)
		systemPrompt += fmt.Sprintf("\n\n## Conversation Context\n%s", ctxStr)
	}

	// Build the user prompt from history + new message
	var promptBuilder strings.Builder

	// Include prior messages (truncate if needed)
	startIdx := 0
	if len(history) > b.maxHistoryMessages {
		startIdx = len(history) - b.maxHistoryMessages
		promptBuilder.WriteString("(Earlier messages omitted for brevity)\n\n")
	}

	for _, msg := range history[startIdx:] {
		role := messageRole(msg.FromHandle, session.ToroHandle)
		body := textFromPG(msg.BodyText)
		if body == "" {
			continue
		}
		fmt.Fprintf(&promptBuilder, "%s: %s\n\n", role, body)
	}

	// Append the new inbound message
	fmt.Fprintf(&promptBuilder, "%s: %s\n\n", messageRole(session.ParticipantHandle, ""), newMessage)

	return BuildResult{
		SystemPrompt: systemPrompt,
		Prompt:       promptBuilder.String(),
	}
}

// BuildResult holds the assembled prompt components.
type BuildResult struct {
	SystemPrompt string
	Prompt       string
}

// messageRole returns the role label for a message author.
// Messages FROM the toro handle are labeled "ASSISTANT" (the agent's prior responses),
// messages FROM anyone else are labeled "USER".
func messageRole(fromHandle, toroHandle string) string {
	if toroHandle != "" && strings.EqualFold(fromHandle, toroHandle) {
		return "ASSISTANT"
	}
	return "USER"
}

func defaultSystemPrompt() string {
	return `You are a conversational AI assistant. You are communicating with a person on behalf of an accounting firm.
Be professional, friendly, and concise. Help collect documents, answer questions, and provide clarifications.

When the conversation requires a response or follow-up, draft a clear message. If the query is resolved, indicate that the conversation is complete.`
}

func textFromPG(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}
