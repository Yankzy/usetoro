package accountselection

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/agents"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/redux"
	"github.com/Yankzy/usetoro/tap/workflows"
	"github.com/nats-io/nats.go"
)

type SelectionTaskPayload struct {
	RawDescription   string          `json:"raw_description"`
	CleanEntityName  string          `json:"clean_vendor_or_customer_name"`
	InflowOrOutflow  string          `json:"inflow_or_outflow"`
	Amount           float64         `json:"amount"`
	CompanyIndustry  string          `json:"company_industry_description"`
	MacroClass       string          `json:"macro_class"`
	AccountType      string          `json:"account_type"`
	FilteredAccounts json.RawMessage `json:"json_array_of_filtered_accounts_with_ids_and_names"`
}

type SelectionResult struct {
	AccountID string `json:"account_id"`
	Reasoning string `json:"reasoning"`
}

type AccountSelectionAgent struct {
	*agent.BaseAgent
	RT      *agent.Runtime
	Queries *database.Queries
}

const AgentName = "account-selection-agent"


func init() {
	agents.Register(AgentName, NewAgent)
}

func NewAgent(env core.Environment) core.Runnable {
	var a AccountSelectionAgent
	a.RT = agent.NewRuntime(env.Logger, env.Bus, env.Config)
	a.Queries = env.Queries

	handler := func(msg *nats.Msg) {
		meta, metaErr := msg.Metadata()
		if metaErr == nil && meta.NumDelivered > 3 {
			a.Logger.Error("Poison pill detected, terminating message", "subject", msg.Subject)
			msg.Term()
			return
		}

		if err := a.handleCFP(msg); err != nil {
			var origEnv core.Envelope
			if envErr := json.Unmarshal(msg.Data, &origEnv); envErr == nil {
				a.ReplyFailure(msg, origEnv, err)
			} else {
				msg.Nak()
			}
			return
		}

		msg.Ack()
	}

	a.BaseAgent = agent.NewBaseAgent(env.Logger, env.Bus, env.Config, handler)
	return &a
}

func (a *AccountSelectionAgent) handleCFP(msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		return nil
	}

	if env.Performative != core.CFP {
		return nil
	}

	proposal := map[string]interface{}{
		"price": 1,
		"eta":   "5s",
	}

	replyEnv, _ := core.NewEnvelope(
		uuid.New().String(),
		a.Cfg.DID,
		env.SenderDID,
		env.ConversationID,
		core.PROPOSE,
		proposal,
	)
	replyEnv.Signature = a.KP.Sign(replyEnv.Body)
	replyBytes, _ := json.Marshal(replyEnv)

	if err := a.Bus.Publish(core.BuildAgentInbox(env.SenderDID), replyBytes); err != nil {
		return err
	}

	return a.executeTask(env)
}

func (a *AccountSelectionAgent) executeTask(cfpEnv core.Envelope) error {
	var task core.TaskDefinition
	if err := json.Unmarshal(cfpEnv.Body, &task); err != nil {
		return err
	}

	var payload SelectionTaskPayload
	if err := core.UnmarshalTaskPayload(task.Payload, &payload); err != nil {
		return err
	}

	var workflowID pgtype.UUID
	_ = workflowID.Scan(task.ID)

	schema := task.WorkflowSchema
	if strings.TrimSpace(schema) == "" {
		schema = a.Cfg.WorkflowSchema
	}

	wfCfg := agent.WorkflowConfig{
		SchemaString: schema,
		RBAC: redux.RBACPolicy{
			AllowedPrefixes: map[string][]string{
				a.Cfg.DID: {"/account_id", "/reasoning"},
			},
		},
	}

	llmCallback := func(previousFaults []redux.DomainFault, currentSeq uint64, baseState []byte) ([]json.RawMessage, error) {
		prompt := a.buildUserPrompt(payload)

		respText, err := a.RT.Exec(context.Background(), prompt, task.SystemPrompt)
		if err != nil {
			return nil, err
		}

		var result SelectionResult
		cleanJSON := a.extractJSON(respText)
		if err := json.Unmarshal([]byte(cleanJSON), &result); err != nil {
			return nil, fmt.Errorf("failed to parse selection response: %w", err)
		}

		patch1 := fmt.Sprintf(`{"op": "add", "path": "/account_id", "value": "%s"}`, result.AccountID)
		patch2 := fmt.Sprintf(`{"op": "add", "path": "/reasoning", "value": "%s"}`, strings.ReplaceAll(result.Reasoning, `"`, `\"`))

		return []json.RawMessage{[]byte(patch1), []byte(patch2)}, nil
	}

	onComplete := func(nextState []byte) error {
		proof := core.Proof{
			TaskID:    task.ID,
			Type:      core.ProofAPI,
			Data:      json.RawMessage(nextState),
			Timestamp: time.Now().Unix(),
		}
		proof.Signature = a.KP.Sign(proof.Data)

		proofEnv, _ := core.NewEnvelope(uuid.New().String(), a.Cfg.DID, "did:toro:hive", cfpEnv.ConversationID, core.INFORM, proof)
		proofEnv.Signature = a.KP.Sign(proofEnv.Body)

		finalBytes, _ := json.Marshal(proofEnv)
		return a.Bus.Publish(workflows.OrchestratorInbox, finalBytes)
	}

	return a.ExecuteLocalWorkflow(
		context.Background(),
		task.ID,
		wfCfg,
		llmCallback,
		onComplete,
	)
}

func (a *AccountSelectionAgent) buildUserPrompt(p SelectionTaskPayload) string {
	return fmt.Sprintf(`### TRANSACTION CONTEXT
- Raw Description: %s [cite: 5]
- Clean Entity Name: %s [cite: 5]
- Cash Direction: %s [cite: 5]
- Amount: $%.2f [cite: 5]
- Business Industry: %s [cite: 5]

### ACCOUNTING CONSTRAINTS
- Approved Macro Class: %s [cite: 5]
- Approved Micro AccountType: %s [cite: 5]
- Available QBO Accounts for this specific AccountType: %s [cite: 5]

Select the exact QuickBooks Online Account ID based on the context and rules. [cite: 5]`,
		p.RawDescription, p.CleanEntityName, p.InflowOrOutflow, p.Amount, p.CompanyIndustry, p.MacroClass, p.AccountType, string(p.FilteredAccounts))
}

func (a *AccountSelectionAgent) extractJSON(text string) string {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end == -1 {
		return text
	}
	return text[start : end+1]
}
