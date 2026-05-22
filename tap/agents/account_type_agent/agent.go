package accounttypeagent

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
	MacroClass                 string          `json:"macro_class"`
	NormalizedDescription      string          `json:"normalized_description"`
	InflowOrOutflow            string          `json:"inflow_or_outflow"`
	Amount                     float64         `json:"amount"`
	CompanyIndustryDescription string          `json:"company_industry_description"`
	SubsetAccountTypes         json.RawMessage `json:"json_subset_of_account_types"`
}

type SelectionResult struct {
	AccountType string `json:"account_type"`
	Reasoning   string `json:"reasoning"`
}

type AccountTypeSelectionAgent struct {
	*agent.BaseAgent
	RT      *agent.Runtime
	Queries *database.Queries
}

const AgentName = "account-type-selection-agent"


func init() {
	agents.Register(AgentName, NewAgent)
}

func NewAgent(env core.Environment) core.Runnable {
	var a AccountTypeSelectionAgent
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
			a.Logger.Error("Transient error processing account type selection", "error", err)
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

func (a *AccountTypeSelectionAgent) handleCFP(msg *nats.Msg) error {
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

func (a *AccountTypeSelectionAgent) executeTask(cfpEnv core.Envelope) error {
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
				a.Cfg.DID: {"/account_type", "/reasoning"},
			},
		},
	}

	llmCallback := func(previousFaults []redux.DomainFault, currentSeq uint64, baseState []byte) ([]json.RawMessage, error) {
		prompt := a.buildUserPrompt(payload)

		ctx := agent.WithModel(context.Background(), task.Model)
		respText, err := a.RT.Exec(ctx, prompt, task.SystemPrompt)
		if err != nil {
			return nil, err
		}

		var result SelectionResult
		cleanJSON := a.extractJSON(respText)
		if err := json.Unmarshal([]byte(cleanJSON), &result); err != nil {
			return nil, fmt.Errorf("failed to parse LLM response: %w", err)
		}

		patch1 := fmt.Sprintf(`{"op": "add", "path": "/account_type", "value": "%s"}`, result.AccountType)
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

func (a *AccountTypeSelectionAgent) buildUserPrompt(p SelectionTaskPayload) string {
	return fmt.Sprintf(`### TRANSACTION DATA
- Description: %s
- Cash Direction: %s
- Absolute Amount: $%.2f
- Business Industry/Description: %s
- Available AccountTypes for %s: %s

Select the most accurate QuickBooks Online 'AccountType' based on the SELECTION RULES.`,
		p.NormalizedDescription, p.InflowOrOutflow, p.Amount, p.CompanyIndustryDescription, p.MacroClass, string(p.SubsetAccountTypes))
}

func (a *AccountTypeSelectionAgent) extractJSON(text string) string {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end == -1 {
		return text
	}
	return text[start : end+1]
}
