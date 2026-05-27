package conversation

import (
	"fmt"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
)

// ConversationMessage represents a single message in the conversation history
// with proper role separation for structured LLM input.
type ConversationMessage struct {
	Role      string    `json:"role"`    // "user" or "assistant"
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

// ContextBuilder assembles the full conversational context for the general agent.
type ContextBuilder struct {
	maxHistoryMessages int
}

func NewContextBuilder() *ContextBuilder {
	return &ContextBuilder{
		maxHistoryMessages: 50,
	}
}

// BuildContext assembles the system prompt and structured message history for the
// general agent from a session's message history plus the new inbound message.
//
// It returns both:
//   - A flat Prompt string (backward compat for non-conversation callers)
//   - A structured Messages slice with proper user/assistant roles and timestamps
func (b *ContextBuilder) BuildContext(session database.ToroCoreConversationSession, history []database.ToroCoreConversation, newMessage string) BuildResult {
	systemPrompt := textFromPG(session.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = defaultSystemPrompt()
	}

	if len(session.ContextJson) > 0 && string(session.ContextJson) != "{}" {
		systemPrompt += fmt.Sprintf("\n\n## Conversation Context\n%s", string(session.ContextJson))
	}

	// Build structured messages from history
	messages := b.buildStructuredMessages(history, newMessage)

	// Add date-awareness context to system prompt
	if len(history) > 0 {
		systemPrompt += "\n\n## Conversation Thread\nThe following conversation has taken place over time. Each message includes a timestamp so you can understand the chronological context."
	}

	// Also build the flat prompt for backward compatibility
	flatPrompt := b.buildFlatPrompt(history, newMessage)

	return BuildResult{
		SystemPrompt: systemPrompt,
		Prompt:       flatPrompt,
		Messages:     messages,
	}
}

// buildStructuredMessages converts DB history rows into a slice of ConversationMessage
// with proper roles and timestamps, then appends the new inbound message.
func (b *ContextBuilder) buildStructuredMessages(history []database.ToroCoreConversation, newMessage string) []ConversationMessage {
	var messages []ConversationMessage

	startIdx := 0
	if len(history) > b.maxHistoryMessages {
		startIdx = len(history) - b.maxHistoryMessages
	}

	var lastDate string
	for _, msg := range history[startIdx:] {
		body := textFromPG(msg.BodyText)
		if body == "" {
			continue
		}

		ts := timeFromPG(msg.CreatedAt)
		currentDate := ts.Format("January 2, 2006")

		// Add date separator when the date changes for thread awareness
		content := body
		if currentDate != lastDate && !ts.IsZero() {
			content = fmt.Sprintf("[%s]\n%s", ts.Format("Jan 2, 3:04 PM"), body)
			lastDate = currentDate
		} else if !ts.IsZero() {
			content = fmt.Sprintf("[%s]\n%s", ts.Format("3:04 PM"), body)
		}

		role := "user"
		if msg.Role == "assistant" {
			role = "assistant"
		}

		messages = append(messages, ConversationMessage{
			Role:      role,
			Content:   content,
			Timestamp: ts,
		})
	}

	// Append the new inbound message as the final user message if provided
	if newMessage != "" {
		messages = append(messages, ConversationMessage{
			Role:      "user",
			Content:   newMessage,
			Timestamp: time.Now(),
		})
	}

	return messages
}

// buildFlatPrompt creates the legacy single-string prompt for backward compatibility.
func (b *ContextBuilder) buildFlatPrompt(history []database.ToroCoreConversation, newMessage string) string {
	var builder strings.Builder

	startIdx := 0
	if len(history) > b.maxHistoryMessages {
		startIdx = len(history) - b.maxHistoryMessages
		builder.WriteString("(Earlier messages omitted for brevity)\n\n")
	}

	for _, msg := range history[startIdx:] {
		body := textFromPG(msg.BodyText)
		if body == "" {
			continue
		}
		if msg.Role == "assistant" {
			builder.WriteString("Assistant: ")
		} else {
			builder.WriteString("User: ")
		}
		builder.WriteString(body)
		builder.WriteString("\n\n")
	}

	if newMessage != "" {
		builder.WriteString("User: ")
		builder.WriteString(newMessage)
		builder.WriteString("\n\n")
	}

	return builder.String()
}

type BuildResult struct {
	SystemPrompt string
	Prompt       string
	Messages     []ConversationMessage
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

func timeFromPG(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}
