package domain_tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/google/uuid"
)

func init() {
	Register("email_marketing", &EmailMarketingTool{})
}

type EmailMarketingTool struct{}

func (t *EmailMarketingTool) BuildAgents(ctx context.Context, env core.Envelope, dagName string, deps ToolDependencies) ([]*ase.AutonomousSemanticEngineNode, error) {
	var task core.TaskDefinition
	if err := json.Unmarshal(env.Body, &task); err != nil {
		return nil, fmt.Errorf("marketing_email_tool: failed to unmarshal envelope body: %w", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return nil, fmt.Errorf("marketing_email_tool: failed to unmarshal payload: %w", err)
	}

	entityID := ""
	if eid, ok := payload["entity_id"].(string); ok {
		entityID = eid
	}

	nodeID := uuid.New().String()
	agent := ase.NewASENode(entityID, "", dagName, payload)
	agent.NodeID = nodeID

	return []*ase.AutonomousSemanticEngineNode{agent}, nil
}

func (t *EmailMarketingTool) GenerateAlertPayload(ctx context.Context, a *ase.AutonomousSemanticEngineNode, deps ToolDependencies) (map[string]interface{}, error) {
	alertPrompt := fmt.Sprintf(
		"SYSTEM ALERT: A marketing email sequence (ID: %s) is stuck in state %s.\nReason: %s",
		a.NodeID, a.GetState(), a.HoldReason,
	)

	return map[string]interface{}{
		"prompt":      alertPrompt,
		"body_text":   alertPrompt,
		"entity_id":   a.TenantID,
		"source":      "system",
		"from_handle": fmt.Sprintf("ase:%s:email_marketing:%s", a.NodeID, a.DagName),
		"to_handle":   "general-agent",
	}, nil
}

func (t *EmailMarketingTool) ResumeAgent(ctx context.Context, nodeID string, dagName string, deps ToolDependencies) (*ase.AutonomousSemanticEngineNode, error) {
	return deps.Store.GetCachedAgent(ctx, nodeID, nil)
}

func (t *EmailMarketingTool) GetBacktrackingInstructions(a *ase.AutonomousSemanticEngineNode, newContext string, traceBytes []byte) (string, string) {
	domainSystemPrompt := `Extract a generalized rule for handling similar marketing leads in the future so this mistake is never repeated. Return it in the "extracted_rule" field.`
	userPrompt := fmt.Sprintf("New Human Context: %s\nExecution Trace:\n%s", newContext, string(traceBytes))
	return domainSystemPrompt, userPrompt
}

func (t *EmailMarketingTool) GetClassifier(deps ToolDependencies) ase.Classifier {
	// Re-using the BookkeepingClassifier as a generic FIPA/LLM dispatcher
	// for the marketing tool, assuming the marketing DAG handles the specialized prompts.
	return NewBookkeepingClassifier(deps.Runtime, deps.NC, "worker.inbox.marketing", deps.Logger)
}

func (t *EmailMarketingTool) GetStatePersister(deps ToolDependencies) ase.StatePersister {
	// Initialize and return the custom MarketingStore
	// To pass Config we need to fetch it since it's not in deps
	// But actually, we don't have access to Config here unless we add it to ToolDependencies or get it via config.GetGlobal()
	cfg := config.GetGlobal() // Assuming this is available, if not we'll need to patch domain_tool.go
	return NewMarketingStore(deps.Redis, cfg)
}
