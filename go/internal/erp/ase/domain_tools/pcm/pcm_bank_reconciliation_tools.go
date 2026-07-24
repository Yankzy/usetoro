package pcm

import (
	"context"

	"github.com/google/uuid"

	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/domain_tools"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func init() {
	domain_tools.Register("pcm_bank_reconciliation", &PcmBankReconciliationTool{})
}

type PcmBankReconciliationTool struct{}

type PcmPayload struct {
	EntityID  string `json:"entity_id"`
	RealmID   string `json:"realm_id"`
	SessionID string `json:"session_id"`
	Documents []struct {
		Type string                 `json:"type"`
		Data map[string]interface{} `json:"data"`
	} `json:"documents"`
}

func (t *PcmBankReconciliationTool) BuildAgents(ctx context.Context, env core.Envelope, dagName string, deps domain_tools.ToolDependencies) ([]*ase.AutonomousSemanticEngineNode, error) {
	var payload PcmPayload
	if err := core.UnmarshalTaskPayload(env.Body, &payload); err != nil {
		deps.Logger.Error("pcm_bank_reconciliation: failed to unmarshal payload", "error", err)
		return nil, nil
	}

	var agents []*ase.AutonomousSemanticEngineNode
	for _, doc := range payload.Documents {
		agent := ase.NewASENode(payload.EntityID, payload.RealmID, dagName, doc.Data)
		// We set a new NodeID, ideally this comes from the DB insert, but we'll generate one for now
		agent.NodeID = uuid.New().String()
		agent.SetLogger(deps.Logger)
		agent.Persister = deps.Store
		agents = append(agents, agent)
	}

	return agents, nil
}

func (t *PcmBankReconciliationTool) GenerateAlertPayload(ctx context.Context, a *ase.AutonomousSemanticEngineNode, deps domain_tools.ToolDependencies) (map[string]interface{}, error) {
	return map[string]interface{}{
		"prompt": "We need a photo of the official Facture to claim your TVA deduction. Please send a photo here.",
	}, nil
}

func (t *PcmBankReconciliationTool) ResumeAgent(ctx context.Context, nodeID string, dagName string, deps domain_tools.ToolDependencies) (*ase.AutonomousSemanticEngineNode, error) {
	return deps.Store.GetCachedAgent(ctx, nodeID, nil)
}

func (t *PcmBankReconciliationTool) GetBacktrackingInstructions(a *ase.AutonomousSemanticEngineNode, newContext string, traceBytes []byte) (string, string) {
	return "Update bank reconciliation rules based on user input.", newContext
}

func (t *PcmBankReconciliationTool) GetClassifier(deps domain_tools.ToolDependencies) ase.Classifier {
	return NewPcmBankReconciliationClassifier(deps.Runtime, deps.NC, deps.Logger)
}

func (t *PcmBankReconciliationTool) GetStatePersister(deps domain_tools.ToolDependencies) ase.StatePersister {
	return NewPcmBankReconciliationStore(deps)
}
