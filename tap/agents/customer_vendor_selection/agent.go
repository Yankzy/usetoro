package entityselection

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

const AgentName = "entity-selection-agent"

func init() {
	agents.Register(AgentName, NewAgent)
}

type EntitySelectionTaskPayload struct {
	RawDescription           string          `json:"raw_description"`
	InflowOrOutflow          string          `json:"inflow_or_outflow"`
	ExistingDatabaseEntities json.RawMessage `json:"json_list_of_existing_vendors_or_customers_with_ids"`
}

type EntitySelectionResult struct {
	EntityID        *string `json:"entity_id"`
	NewCleanName    string  `json:"new_clean_name"`
	MatchConfidence string  `json:"match_confidence"`
}

type EntitySelectionAgent struct {
	*agent.BaseAgent
	RT      *agent.Runtime
	Queries *database.Queries
}



func NewAgent(env core.Environment) core.Runnable {
	var a EntitySelectionAgent
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
			a.Logger.Error("Transient error processing entity selection", "error", err)
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

func (a *EntitySelectionAgent) handleCFP(msg *nats.Msg) error {
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

func (a *EntitySelectionAgent) executeTask(cfpEnv core.Envelope) error {
	var task core.TaskDefinition
	if err := json.Unmarshal(cfpEnv.Body, &task); err != nil {
		return err
	}

	var payload EntitySelectionTaskPayload
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
				a.Cfg.DID: {"/entity_id", "/new_clean_name", "/match_confidence"},
			},
		},
	}

	llmCallback := func(previousFaults []redux.DomainFault, currentSeq uint64, baseState []byte) ([]json.RawMessage, error) {
		prompt := a.buildUserPrompt(payload)

		respText, err := a.RT.Exec(context.Background(), prompt, task.SystemPrompt)
		if err != nil {
			return nil, err
		}

		var result EntitySelectionResult
		cleanJSON := a.extractJSON(respText)
		if err := json.Unmarshal([]byte(cleanJSON), &result); err != nil {
			return nil, fmt.Errorf("failed to parse LLM response: %w", err)
		}

		var entityIDVal interface{}
		if result.EntityID != nil {
			entityIDVal = *result.EntityID
		} else {
			entityIDVal = nil
		}

		entityIDJSON, _ := json.Marshal(entityIDVal)
		patch1 := fmt.Sprintf(`{"op": "add", "path": "/entity_id", "value": %s}`, string(entityIDJSON))
		patch2 := fmt.Sprintf(`{"op": "add", "path": "/new_clean_name", "value": "%s"}`, strings.ReplaceAll(result.NewCleanName, `"`, `\"`))
		patch3 := fmt.Sprintf(`{"op": "add", "path": "/match_confidence", "value": "%s"}`, result.MatchConfidence)

		return []json.RawMessage{[]byte(patch1), []byte(patch2), []byte(patch3)}, nil
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

func (a *EntitySelectionAgent) buildUserPrompt(p EntitySelectionTaskPayload) string {
	return fmt.Sprintf(`### TRANSACTION DATA
- Raw Description: %s
- Cash Direction: %s
- Existing Database Entities: %s

Identify the true merchant or customer based on the provided RULES.`,
		p.RawDescription, p.InflowOrOutflow, string(p.ExistingDatabaseEntities))
}

func (a *EntitySelectionAgent) extractJSON(text string) string {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end == -1 {
		return text
	}
	return text[start : end+1]
}
