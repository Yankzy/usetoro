package domain_tools

import (
	"context"
	"fmt"

	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func init() {
	Register("onboarding", &OnboardingTool{})
}

type OnboardingTool struct{}

func (t *OnboardingTool) BuildAgents(ctx context.Context, env core.Envelope, dagName string, deps ToolDependencies) ([]*ase.AutonomousSemanticEngineNode, error) {
	// TODO: Implement mapping for onboarding events
	return nil, fmt.Errorf("onboarding tool BuildAgents not yet implemented")
}

func (t *OnboardingTool) GenerateAlertPayload(ctx context.Context, a *ase.AutonomousSemanticEngineNode, deps ToolDependencies) (map[string]interface{}, error) {
	// TODO: Implement alert prompt for onboarding issues
	alertPrompt := fmt.Sprintf(
		"SYSTEM ALERT: An onboarding flow (Node ID: %s) is stuck in state %s.\nReason: %s",
		a.NodeID, a.GetState(), a.HoldReason,
	)

	return map[string]interface{}{
		"prompt":      alertPrompt,
		"body_text":   alertPrompt,
		"entity_id":   a.TenantID,
		"source":      "system",
		"from_handle": fmt.Sprintf("ase:%s:onboarding:%s", a.NodeID, a.DagName),
		"to_handle":   "general-agent",
	}, nil
}

func (t *OnboardingTool) ResumeAgent(ctx context.Context, nodeID string, dagName string, deps ToolDependencies) (*ase.AutonomousSemanticEngineNode, error) {
	return nil, fmt.Errorf("onboarding tool ResumeAgent not yet implemented")
}

func (t *OnboardingTool) GetBacktrackingInstructions(a *ase.AutonomousSemanticEngineNode, newContext string, traceBytes []byte) (string, string) {
	domainSystemPrompt := `Additionally, formulate a generalized rule for onboarding this type of entity so this mistake is never repeated. Return it in the "extracted_rule" field. Keep it concise.
Also provide a "rule_keyword" (e.g., the onboarding step or entity type). If it's a general rule, use "GLOBAL".`

	userPrompt := fmt.Sprintf("New Human Context: %s\nExecution Trace:\n%s", newContext, string(traceBytes))
	return domainSystemPrompt, userPrompt
}

func (t *OnboardingTool) GetClassifier(deps ToolDependencies) ase.Classifier {
	return nil // Not implemented yet
}
