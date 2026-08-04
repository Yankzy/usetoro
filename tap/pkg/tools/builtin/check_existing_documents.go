package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/jackc/pgx/v5/pgtype"
)

type CheckExistingDocumentsTool struct {
	queries *database.Queries
}

func NewCheckExistingDocumentsTool(queries *database.Queries) tools.Tool {
	return &CheckExistingDocumentsTool{
		queries: queries,
	}
}

func (t *CheckExistingDocumentsTool) Name() string {
	return "CheckExistingDocuments"
}

func (t *CheckExistingDocumentsTool) Description() string {
	return "Search the firm's document storage (attachables/receipts) for existing documents that might match the transaction. Use this BEFORE queueing a client request to see if the client already sent the receipt."
}

func (t *CheckExistingDocumentsTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"search_query": {
				"type": "string",
				"description": "Keywords to search for (e.g. vendor name, amount, or receipt). Will fuzzy match against document names and notes."
			}
		},
		"required": ["search_query"]
	}`)
}

func (t *CheckExistingDocumentsTool) Call(ctx context.Context, input map[string]any) (string, error) {
	searchQuery, ok := input["search_query"].(string)
	if !ok || searchQuery == "" {
		return "", fmt.Errorf("search_query is required")
	}

	realmIDStr, ok := ctx.Value(tools.RealmIDKey{}).(string)
	if !ok || realmIDStr == "" {
		return "", fmt.Errorf("RealmID not found in context")
	}

	if t.queries == nil {
		return "", fmt.Errorf("database queries not initialized in tool")
	}

	// SearchAttachables expects the search query to be used in ILIKE. 
	// The DB query handles the wildcards.
	// Convert interface to string
	searchTerm := fmt.Sprintf("%v", searchQuery)

	results, err := t.queries.SearchAttachables(ctx, database.SearchAttachablesParams{
		RealmID: realmIDStr,
		Column2: pgtype.Text{String: searchTerm, Valid: true},
	})
	if err != nil {
		return "", fmt.Errorf("failed to search attachables: %w", err)
	}

	if len(results) == 0 {
		return fmt.Sprintf("No existing documents found matching '%s'. You may proceed to request it from the client.", searchTerm), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d matching documents in storage:\n", len(results)))
	for _, doc := range results {
		name := "Unknown"
		if doc.FileName.Valid {
			name = doc.FileName.String
		}
		sb.WriteString(fmt.Sprintf("- File: %s (Created: %v)\n", name, doc.ErpCreatedTime.Time))
	}
	sb.WriteString("\nIf one of these looks like the required document, you can classify the transaction or use it as evidence. If none match exactly what you need, you should still request the document from the client.")

	return sb.String(), nil
}

func init() {
	Register("CheckExistingDocuments", func(env core.Environment, logger *slog.Logger) tools.Tool {
		return NewCheckExistingDocumentsTool(env.Queries)
	})
}
