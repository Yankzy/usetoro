package genericbatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/agents"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/redux"
	"github.com/Yankzy/usetoro/tap/workflows"
)

const AgentName = "generic-batch-agent"

func init() {
	agents.Register(AgentName, NewAgent)
}

type GenericBatchAgent struct {
	*agent.BaseAgent
	RT      *agent.Runtime
	Queries *database.Queries
}

func NewAgent(env core.Environment) core.Runnable {
	var a GenericBatchAgent
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
			a.Logger.Error("Error processing batch classification task", "error", err)
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

func (a *GenericBatchAgent) handleCFP(msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		return nil
	}

	if env.Performative != core.CFP {
		return nil
	}

	proposal := map[string]interface{}{
		"price": 1,
		"eta":   "10s", // Batch tasks may take a bit longer
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

func (a *GenericBatchAgent) executeTask(cfpEnv core.Envelope) error {
	var task core.TaskDefinition
	if err := json.Unmarshal(cfpEnv.Body, &task); err != nil {
		return err
	}

	// Read and unwrap the payload correctly
	var shapedPayload map[string]interface{}
	if err := core.UnmarshalTaskPayload(task.Payload, &shapedPayload); err != nil {
		return err
	}

	var workflowID pgtype.UUID
	_ = workflowID.Scan(task.ID)

	schema := task.WorkflowSchema
	if strings.TrimSpace(schema) == "" {
		schema = a.Cfg.WorkflowSchema
	}

	// 1. Redux in this system forbids arrays to prevent index-shift bugs.
	// We must recursively convert all arrays in the payload to maps with stringified indices.
	var rawPayload interface{}
	_ = json.Unmarshal(task.Payload, &rawPayload)
	mapifiedPayload := mapify(rawPayload)
	mapifiedBytes, _ := json.Marshal(mapifiedPayload)

	wfCfg := agent.WorkflowConfig{
		SchemaString: schema,
		InitialState: mapifiedBytes,
		// RBAC for batch agents needs to allow wildcard arrays or at least permit writing anything
		// Since we're dynamically patching, we allow the agent's DID to write paths
		RBAC: redux.RBACPolicy{
			AllowedPrefixes: map[string][]string{
				a.Cfg.DID: {"/"}, // Allow full access to state since paths are dynamically generated per array key
			},
		},
	}

	llmCallback := func(previousFaults []redux.DomainFault, currentSeq uint64, baseState []byte) ([]json.RawMessage, error) {
		// 1. We format a generic prompt context by just JSON marshalling the payload.
		// We strip the entity_id to ensure privacy and prevent LLM confusion.
		sanitizedPayload := make(map[string]interface{})
		for k, v := range shapedPayload {
			if k != "entity_id" {
				sanitizedPayload[k] = v
			}
		}

		promptCtx, _ := json.MarshalIndent(sanitizedPayload, "", "  ")
		userPrompt := fmt.Sprintf("### BATCH INPUT DATA\n%s\n", string(promptCtx))

		// Append the actual LLM system prompt from the Workflow Step
		systemPrompt := task.SystemPrompt
		if systemPrompt == "" {
			return nil, fmt.Errorf("generic batch agent requires a system_prompt defined in the workflow step")
		}

		respText, err := a.RT.Exec(context.Background(), userPrompt, systemPrompt)
		if err != nil {
			return nil, err
		}

		// 2. Extract and return the JSON patches directly
		patches, err := a.extractJSONPatches(respText)
		if err != nil {
			return nil, fmt.Errorf("failed to extract JSON patches: %w", err)
		}

		return patches, nil
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

func (a *GenericBatchAgent) extractJSONPatches(respText string) ([]json.RawMessage, error) {
	start := strings.Index(respText, "[")
	end := strings.LastIndex(respText, "]")
	if start == -1 || end == -1 {
		return nil, fmt.Errorf("no JSON array found in LLM response")
	}
	
	cleanJSON := respText[start : end+1]
	var patches []json.RawMessage
	if err := json.Unmarshal([]byte(cleanJSON), &patches); err != nil {
		return nil, err
	}
	return patches, nil
}

func mapify(v interface{}) interface{} {
	switch val := v.(type) {
	case []interface{}:
		m := make(map[string]interface{})
		for i, item := range val {
			m[fmt.Sprintf("%d", i)] = mapify(item)
		}
		return m
	case map[string]interface{}:
		for k, childV := range val {
			val[k] = mapify(childV)
		}
		return val
	default:
		return v
	}
}
