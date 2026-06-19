package builtin

import (
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"log/slog"
	"context"
	"encoding/json"
	"fmt"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/jackc/pgx/v5/pgtype"
)

// ClientLookupTool allows an agent to search for a client's RealmID using the Firm's EntityID and a fuzzy search term.
type ClientLookupTool struct {
	queries *database.Queries
}

func NewClientLookupTool(queries *database.Queries) tools.Tool {
	return &ClientLookupTool{
		queries: queries,
	}
}

func (t *ClientLookupTool) Name() string {
	return "LookupClient"
}

func (t *ClientLookupTool) Description() string {
	return "Search for a specific client (RealmID) by name across the Firm's connected clients. Use this when the CPA mentions a client name in natural language and you need to determine the exact RealmID to proceed with the task."
}

func (t *ClientLookupTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "The name of the client to search for (e.g. 'Cho restaurant')"
			}
		},
		"required": ["query"]
	}`)
}

func (t *ClientLookupTool) Call(ctx context.Context, input map[string]any) (string, error) {
	query, ok := input["query"].(string)
	if !ok || query == "" {
		return "", fmt.Errorf("query is required")
	}

	// EntityID must be injected into the context by the agent runtime
	entityIDStr, ok := ctx.Value(tools.EntityIDKey{}).(string)
	if !ok || entityIDStr == "" {
		return "", fmt.Errorf("EntityID not found in context; cannot perform client lookup without knowing the CPA Firm context")
	}

	var entityUUID pgtype.UUID
	err := entityUUID.Scan(entityIDStr)
	if err != nil {
		return "", fmt.Errorf("invalid EntityID format: %w", err)
	}

	if t.queries == nil {
		return "", fmt.Errorf("database queries not initialized in tool")
	}

	results, err := t.queries.SearchClientsByEntityID(ctx, database.SearchClientsByEntityIDParams{
		EntityID: entityUUID,
		Column2:  pgtype.Text{String: query, Valid: true},
	})
	if err != nil {
		return "", fmt.Errorf("database search failed: %w", err)
	}

	if len(results) == 0 {
		return fmt.Sprintf("No clients found matching '%s'. Please ask the USER to clarify or provide the exact name.", query), nil
	}

	// Format results for the LLM
	response := "Found the following matching clients:\n"
	for _, r := range results {
		emailStr := "Unknown"
		if info, err := t.queries.GetCompanyInfo(ctx, r.RealmID); err == nil && info.Email.Valid {
			emailStr = info.Email.String
		}
		response += fmt.Sprintf("- Name: %s (RealmID: %s, Email: %s)\n", r.CompanyName, r.RealmID, emailStr)
	}
	response += "\nIf there is exactly one match, you can proceed with the task using that RealmID. If there are multiple, you MUST reply to the CPA asking them to clarify which one they meant."

	return response, nil
}

func init() {
	Register("LookupClient", func(env core.Environment, logger *slog.Logger) tools.Tool {
		return NewClientLookupTool(env.Queries)
	})
}
