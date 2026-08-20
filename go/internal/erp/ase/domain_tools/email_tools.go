package domain_tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

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

	var sUUID pgtype.UUID
	_ = sUUID.Scan(payload.SessionID)

	holds, err := deps.DB.GetHeldTransactionsBySession(ctx, sUUID)
	if err == nil && len(holds) > 0 {
		deps.Logger.Info("email tool: found holds for session, attempting LLM mapping", "session_id", payload.SessionID, "holds_count", len(holds))
		
		prompt := fmt.Sprintf("Client Reply:\n%s\n\nPending Transactions:\n", payload.Prompt)
		for _, h := range holds {
			prompt += fmt.Sprintf("- Transaction ID: %s | Desc: %s | Amount: %s | Reasoning: %s\n",
				uuid.UUID(h.TransactionID.Bytes).String(),
				h.RawDescription.String,
				h.RawAmount,
				string(h.AseExecutionTrace),
			)
		}

		sysPrompt := "You are a mapping agent. Map the client's reply to the corresponding transactions. Output JSON with a 'mappings' array containing 'transaction_id' and 'context'."

		type LLMMappingResult struct {
			Mappings []struct {
				TransactionID string `json:"transaction_id"`
				Context       string `json:"context"`
			} `json:"mappings"`
		}

		var result LLMMappingResult
		respStr, err := deps.Runtime.Exec(ctx, prompt, sysPrompt)
		if err == nil {
			cleanResp := strings.TrimSpace(respStr)
			if strings.HasPrefix(cleanResp, "```json") {
				cleanResp = strings.TrimPrefix(cleanResp, "```json")
				cleanResp = strings.TrimSuffix(cleanResp, "```")
			} else if strings.HasPrefix(cleanResp, "```") {
				cleanResp = strings.TrimPrefix(cleanResp, "```")
				cleanResp = strings.TrimSuffix(cleanResp, "```")
			}
			cleanResp = strings.TrimSpace(cleanResp)
			
			if unmarshalErr := json.Unmarshal([]byte(cleanResp), &result); unmarshalErr == nil {
				for _, mapping := range result.Mappings {
					deps.Logger.Info("email tool: mapped reply to transaction", "transaction_id", mapping.TransactionID)
				
				resumeEvt := map[string]interface{}{
					"node_id":         mapping.TransactionID,
					"start_node_id":   "", 
					"resolved_reason": mapping.Context,
					"dag_name":        "ase_gaap_us",
					"domain_tool":     "bookkeeping",
				}
				
				b, _ := json.Marshal(resumeEvt)
				_ = deps.NC.Publish("ase.events.resume", b)
			}
			
			// If all holds were addressed, we might abort the email DAG. 
				// to stop the email workflow from duplicating work.
				return nil, nil
			} else {
				deps.Logger.Error("email tool: JSON unmarshal failed", "error", unmarshalErr, "resp", respStr)
			}
		} else {
			deps.Logger.Error("email tool: LLM mapping failed", "error", err)
		}
	}

	nodePayload := map[string]interface{}{
		"session_id":  payload.SessionID,
		"prompt":      payload.Prompt,
		"domain_tool": "email",
	}

	agent := ase.NewASENode(payload.EntityID, dagName, nodePayload)
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

func (t *EmailTool) GetClassifier(deps ToolDependencies) ase.Classifier {
	return nil // Not implemented yet
}

func (t *EmailTool) GetStatePersister(deps ToolDependencies) ase.StatePersister {
	return nil
}
