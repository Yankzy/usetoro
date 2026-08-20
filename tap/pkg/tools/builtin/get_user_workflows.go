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
)

// GetUserWorkflowsTool allows the Dynamic Agent to query available workflows
// from toro_core.workflows to determine which workflow orchestrator topic to trigger.
type GetUserWorkflowsTool struct {
	queries *database.Queries
}

func NewGetUserWorkflowsTool(queries *database.Queries) tools.Tool {
	return &GetUserWorkflowsTool{
		queries: queries,
	}
}

func (t *GetUserWorkflowsTool) Name() string {
	return "get_user_workflows"
}

func (t *GetUserWorkflowsTool) Description() string {
	return "Retrieve available workflows registered in toro_core.workflows for the user or organization. " +
		"Use this tool to inspect active workflows, trigger topics, and workflow blueprints to decide which workflow to initiate."
}

func (t *GetUserWorkflowsTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"filter": {
				"type": "string",
				"description": "Optional keyword or workflow name filter (e.g. 'DAG test', 'bookkeeping', 'petty_cash')."
			},
			"entity_id": {
				"type": "string",
				"description": "Optional Entity/Tenant UUID to scope workflows to this specific entity."
			},
			"user_id": {
				"type": "string",
				"description": "Optional User UUID to resolve entity workflows."
			}
		}
	}`)
}

func (t *GetUserWorkflowsTool) Call(ctx context.Context, input map[string]any) (string, error) {
	var filter string
	if f, ok := input["filter"].(string); ok {
		filter = strings.ToLower(strings.TrimSpace(f))
	}

	if t.queries == nil {
		return "", fmt.Errorf("database queries not initialized in get_user_workflows tool")
	}

	// Resolve entity_id from input, context, or inbound task payload
	entityIDStr, _ := input["entity_id"].(string)
	if entityIDStr == "" {
		if uid, ok := input["user_id"].(string); ok && uid != "" {
			entityIDStr = uid
		}
	}
	if entityIDStr == "" {
		if eid, ok := ctx.Value(tools.EntityIDKey{}).(string); ok && eid != "" {
			entityIDStr = eid
		}
	}
	if entityIDStr == "" {
		if inboundMap, ok := ctx.Value(tools.InboundTaskPayloadKey{}).(map[string]any); ok {
			if eid, ok := inboundMap["entity_id"].(string); ok && eid != "" {
				entityIDStr = eid
			}
		}
	}

	var sb strings.Builder

	// 1. Fetch available workflow blueprints
	blueprints, err := t.queries.GetWorkflowBlueprints(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to query workflow blueprints: %w", err)
	}

	var matchedBlueprints []database.ToroCoreWorkflowBlueprint
	for _, bp := range blueprints {
		if filter != "" {
			nameMatch := strings.Contains(strings.ToLower(bp.Name), filter)
			topicMatch := strings.Contains(strings.ToLower(bp.TriggerTopic), filter)
			defMatch := strings.Contains(strings.ToLower(string(bp.Definition)), filter)
			if !nameMatch && !topicMatch && !defMatch {
				continue
			}
		}
		matchedBlueprints = append(matchedBlueprints, bp)
	}

	if len(matchedBlueprints) == 0 {
		if filter != "" {
			return fmt.Sprintf("No workflow blueprints found matching filter '%s'.", filter), nil
		}
		return "No registered workflow blueprints found in toro_core.workflow_blueprints.", nil
	}

	if entityIDStr != "" {
		sb.WriteString(fmt.Sprintf("Found %d registered workflow blueprint(s) for Entity `%s`:\n", len(matchedBlueprints), entityIDStr))
	} else {
		sb.WriteString(fmt.Sprintf("Found %d registered workflow blueprint(s):\n", len(matchedBlueprints)))
	}
	for _, bp := range matchedBlueprints {
		sb.WriteString(fmt.Sprintf("- **%s**\n", bp.Name))
		sb.WriteString(fmt.Sprintf("  - Trigger Topic: `%s`\n", bp.TriggerTopic))
		if len(bp.Definition) > 0 {
			var defMap map[string]any
			if errUn := json.Unmarshal(bp.Definition, &defMap); errUn == nil {
				if desc, ok := defMap["description"].(string); ok && desc != "" {
					sb.WriteString(fmt.Sprintf("  - Description: %s\n", desc))
				}
				if steps, ok := defMap["steps"].([]any); ok && len(steps) > 0 {
					var stepSummaries []string
					for _, st := range steps {
						if stMap, ok := st.(map[string]any); ok {
							if id, ok := stMap["id"].(string); ok {
								stepSummaries = append(stepSummaries, id)
							}
						}
					}
					if len(stepSummaries) > 0 {
						sb.WriteString(fmt.Sprintf("  - Steps: [%s]\n", strings.Join(stepSummaries, " -> ")))
					}
				}
			}
		}
	}

	sb.WriteString("\nUse trigger_workflow with the Trigger Topic and document IDs to initiate the desired workflow.")

	return sb.String(), nil
}

func init() {
	Register("get_user_workflows", func(env core.Environment, logger *slog.Logger) tools.Tool {
		return NewGetUserWorkflowsTool(env.Queries)
	})
}
