package builtin

import (
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Yankzy/usetoro/internal/database"
)

type HistoryTool struct {
	DB        *database.Queries
	Logger    *slog.Logger
	AgentName string
}

func (t *HistoryTool) Name() string {
	return "FetchCommunicationHistory"
}

func (t *HistoryTool) Description() string {
	return "Fetch the recent communication history (email/chat threads) between you (the virtual employee) and a specific client or vendor. ALWAYS use this to read prior conversations BEFORE sending a new email to someone."
}

func (t *HistoryTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"client_handle": {
				"type": "string",
				"description": "The client's email address or chat handle"
			},
			"limit": {
				"type": "integer",
				"description": "The maximum number of recent messages to fetch (default 5, max 10)"
			}
		},
		"required": ["client_handle"]
	}`)
}

func (t *HistoryTool) Call(ctx context.Context, input map[string]any) (string, error) {
	if t.DB == nil {
		return "", fmt.Errorf("database connection not available")
	}

	clientHandle, _ := input["client_handle"].(string)
	if clientHandle == "" {
		return "", fmt.Errorf("client_handle is required")
	}

	limit := int32(5)
	if l, ok := input["limit"].(float64); ok && l > 0 {
		limit = int32(l)
		if limit > 10 {
			limit = 10
		}
	}

	agentHandle := t.AgentName
	if agentHandle == "" {
		agentHandle = "Sarah" // Fallback to Sarah as per user request
	}

	conversations, err := t.DB.GetRecentConversations(ctx, database.GetRecentConversationsParams{
		FromHandle: clientHandle,
		Limit:      limit,
	})
	if err != nil {
		return "", fmt.Errorf("failed to fetch conversation history: %w", err)
	}

	if len(conversations) == 0 {
		return fmt.Sprintf("No prior communication found with %s.", clientHandle), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d recent messages with %s:\n\n", len(conversations), clientHandle))
	
	// Helper to determine role
	getRole := func(handle string) string {
		if strings.Contains(strings.ToLower(handle), "usetoro.io") || strings.Contains(strings.ToLower(handle), "sarah") {
			return "[AI Agent]"
		}
		_, err := t.DB.GetUserByEmail(ctx, handle)
		if err == nil {
			return "[CPA / Firm User]"
		}
		return "[Business Client / External]"
	}

	for i := len(conversations) - 1; i >= 0; i-- {
		c := conversations[i]
		
		fromRole := getRole(c.FromHandle)
		toRole := getRole(c.ToHandle)
		
		sb.WriteString(fmt.Sprintf("--- Message ID: %s ---\n", c.ID))
		sb.WriteString(fmt.Sprintf("Time: %s\n", c.CreatedAt.Time.Format("2006-01-02 15:04:05")))
		sb.WriteString(fmt.Sprintf("From: %s %s\n", fromRole, c.FromHandle))
		sb.WriteString(fmt.Sprintf("To: %s %s\n", toRole, c.ToHandle))
		if c.Subject.Valid && c.Subject.String != "" {
			sb.WriteString(fmt.Sprintf("Subject: %s\n", c.Subject.String))
		}
		sb.WriteString(fmt.Sprintf("Body:\n%s\n\n", c.StrippedText.String))
	}

	return sb.String(), nil
}

func init() {
	Register("FetchCommunicationHistory", func(env core.Environment, logger *slog.Logger) tools.Tool {
		return &HistoryTool{
			DB:        env.Queries,
			Logger:    logger,
			AgentName: env.Config.Name,
		}
	})
}
