package inflowclassification

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

const AgentName = "inflow-classification-agent"

func init() {
	agents.Register(AgentName, NewAgent)
}

type ClassificationTaskPayload struct {
	NormalizedDescription  string   `json:"normalized_description"`
	Amount                 float64  `json:"amount"`
	KnownLiabilityAccounts []string `json:"json_list_of_liability_accounts"`
	KnownBankAccounts      []string `json:"json_list_of_bank_accounts"`
}


type OutflowClassificationAgent struct {
	*agent.BaseAgent
	RT      *agent.Runtime
	Queries *database.Queries
}

func NewAgent(env core.Environment) core.Runnable {
	var a OutflowClassificationAgent
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
			a.Logger.Error("Error processing classification task", "error", err)
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

func (a *OutflowClassificationAgent) handleCFP(msg *nats.Msg) error {
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

func (a *OutflowClassificationAgent) executeTask(cfpEnv core.Envelope) error {
	var task core.TaskDefinition
	if err := json.Unmarshal(cfpEnv.Body, &task); err != nil {
		return err
	}

	var payload ClassificationTaskPayload
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
				a.Cfg.DID: {"/macro_class", "/reasoning"},
			},
		},
	}

	llmCallback := func(previousFaults []redux.DomainFault, currentSeq uint64, baseState []byte) ([]json.RawMessage, error) {
		prompt := a.buildUserPrompt(payload)

		sysPrompt := task.SystemPrompt
		if schema != "" {
			sysPrompt += "\n\n" + redux.Prompt(schema)
		}

		ctx := agent.WithModel(context.Background(), task.Model)
		respText, err := a.RT.Exec(ctx, prompt, sysPrompt)
		if err != nil {
			return nil, err
		}

		return redux.ParsePatches(respText)
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

func (a *OutflowClassificationAgent) buildUserPrompt(p ClassificationTaskPayload) string {
	liabilities, _ := json.Marshal(p.KnownLiabilityAccounts)
	banks, _ := json.Marshal(p.KnownBankAccounts)

	return fmt.Sprintf(`### TRANSACTION DATA
- Description: %s
- Cash Direction: OUTFLOW
- Amount: $%.2f
- User's Known Liability/Debt Accounts: %s
- User's Known Bank Accounts: %s

Assign the macro_class based on the provided CLASSIFICATION RULES.`,
		p.NormalizedDescription, p.Amount, string(liabilities), string(banks))
}

