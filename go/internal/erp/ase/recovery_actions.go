package ase

import (
	"context"
	"fmt"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
)

// RecoveryExecutor interface allows the ASE to run lightweight native recovery operations 
// in Go rather than falling back to LLM calls in Python.
type RecoveryExecutor interface {
	ExecuteAction(ctx context.Context, actionID string, node *AutonomousSemanticEngineNode) (bool, error)
}

// NativeRecoveryExecutor is the concrete implementation containing dependencies.
type NativeRecoveryExecutor struct {
	queries *database.Queries
}

func NewNativeRecoveryExecutor(q *database.Queries) *NativeRecoveryExecutor {
	return &NativeRecoveryExecutor{queries: q}
}

func (e *NativeRecoveryExecutor) ExecuteAction(ctx context.Context, actionID string, node *AutonomousSemanticEngineNode) (bool, error) {
	switch actionID {
	case "search_document_store":
		return e.actionSearchDocumentStore(ctx, node)
	default:
		// Not a native action or not implemented natively, return false meaning it wasn't recovered
		return false, nil
	}
}

// actionSearchDocumentStore performs a naive search using the node's RawAmount or RawDescription.
func (e *NativeRecoveryExecutor) actionSearchDocumentStore(ctx context.Context, node *AutonomousSemanticEngineNode) (bool, error) {
	if e.queries == nil {
		return false, fmt.Errorf("queries not configured in NativeRecoveryExecutor")
	}
	if node.RealmID == "" {
		return false, fmt.Errorf("missing RealmID on node")
	}

	searchTerm := ""
	if amt, ok := node.Payload["raw_amount"].(string); ok && amt != "" {
		searchTerm = amt
	} else if desc, ok := node.Payload["raw_description"].(string); ok && desc != "" {
		// Just take first 10 chars as fuzzy search to avoid too long string
		if len(desc) > 10 {
			searchTerm = desc[:10]
		} else {
			searchTerm = desc
		}
	}

	if searchTerm == "" {
		return false, nil
	}

	results, err := e.queries.SearchAttachables(ctx, database.SearchAttachablesParams{
		RealmID: node.RealmID,
		Column2: pgtype.Text{String: searchTerm, Valid: true},
	})
	if err != nil {
		return false, err
	}

	if len(results) > 0 {
		// Found something! Update the node's context so it can proceed
		if node.logger != nil {
			node.logger.Info("recovery engine: successfully rescued node via document search", "node_id", node.NodeID, "found_count", len(results))
		}
		
		docStr := fmt.Sprintf("System found matching document in storage automatically: File '%s'. Use this context to proceed.", results[0].FileName.String)
		node.Mu.Lock()
		node.ContextUpdates = append(node.ContextUpdates, docStr)
		node.Mu.Unlock()

		return true, nil
	}

	return false, nil
}
