package domain_tools

import (
	"context"
	"fmt"

	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func init() {
	Register("email", &EmailTool{})
}

type EmailTool struct{}

func (t *EmailTool) BuildAgents(ctx context.Context, env core.Envelope, dagName string, deps ToolDependencies) ([]*ase.AutonomousSemanticEngineNode, error) {
	var payload struct {
		Prompt    string `json:"prompt"`
		EntityID  string `json:"entity_id"`
		SessionID string `json:"session_id"`
		DagName   string `json:"dag_name"`
	}
	if err := core.UnmarshalTaskPayload(env.Body, &payload); err != nil {
		return nil, fmt.Errorf("email tool: failed to unmarshal payload: %w", err)
	}

	if payload.SessionID == "" {
		deps.Logger.Error("email tool: missing session_id")
		return nil, nil
	}

	nodePayload := map[string]interface{}{
		"session_id":  payload.SessionID,
		"prompt":      payload.Prompt,
		"domain_tool": "email",
	}

	agent := ase.NewASENode(payload.EntityID, "", dagName, nodePayload)
	agent.NodeID = payload.SessionID // Use session_id as the NodeID so we can resume it easily

	return []*ase.AutonomousSemanticEngineNode{agent}, nil
}

func (t *EmailTool) GenerateAlertPayload(ctx context.Context, a *ase.AutonomousSemanticEngineNode, deps ToolDependencies) (map[string]interface{}, error) {
	alertPrompt := fmt.Sprintf(
		"SYSTEM ALERT: An inbound email conversation (Session ID: %s) is stuck in state %s.\nReason: %s",
		a.NodeID, a.GetState(), a.HoldReason,
	)

	return map[string]interface{}{
		"prompt":      alertPrompt,
		"body_text":   alertPrompt,
		"entity_id":   a.TenantID,
		"source":      "system",
		"from_handle": fmt.Sprintf("ase:%s:email:%s", a.NodeID, a.DagName),
		"to_handle":   "general-agent",
	}, nil
}

func (t *EmailTool) ResumeAgent(ctx context.Context, nodeID string, dagName string, deps ToolDependencies) (*ase.AutonomousSemanticEngineNode, error) {
	return deps.Store.GetCachedAgent(ctx, nodeID, nil)
}

func (t *EmailTool) GetBacktrackingInstructions(a *ase.AutonomousSemanticEngineNode, newContext string, traceBytes []byte) (string, string) {
	domainSystemPrompt := `Additionally, formulate a generalized rule for handling similar emails in the future so this mistake is never repeated. Return it in the "extracted_rule" field. Keep it concise.
Also provide a "rule_keyword" (e.g., the sender's domain or topic). If it's a general rule, use "GLOBAL".`

	description := "Unknown"
	if desc, ok := a.Payload["prompt"].(string); ok {
		description = desc
	}

	userPrompt := fmt.Sprintf("Email Content: %s\nNew Human Context: %s\nExecution Trace:\n%s", description, newContext, string(traceBytes))
	return domainSystemPrompt, userPrompt
}
