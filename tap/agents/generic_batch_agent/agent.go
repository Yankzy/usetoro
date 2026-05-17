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

	"github.com/Yankzy/usetoro/tap/agents"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/redux"
	"github.com/Yankzy/usetoro/tap/workflows"
)

const AgentName = "generic-batch-agent"

// Register the agent with the global agent registry during package initialization.
func init() {
	agents.Register(AgentName, NewAgent)
}

// GenericBatchAgent is a versatile agent capable of processing batches of data.
// It uses a local Redux store for state management and an LLM for data processing.
type GenericBatchAgent struct {
	*agent.BaseAgent
	RT *agent.Runtime
}

// NewAgent initializes and returns a new instance of GenericBatchAgent.
func NewAgent(env core.Environment) core.Runnable {
	var a GenericBatchAgent
	a.RT = agent.NewRuntime(env.Logger, env.Bus, env.Config)

	// Define the main message handler for the agent.
	handler := func(msg *nats.Msg) {
		// Detect and terminate potential poison pill messages.
		meta, metaErr := msg.Metadata()
		if metaErr == nil && meta.NumDelivered > 3 {
			a.Logger.Error("Poison pill detected, terminating message", "subject", msg.Subject)
			msg.Term()
			return
		}

		// Handle Call For Proposals (CFP) from the orchestrator.
		if err := a.handleCFP(msg); err != nil {
			a.Logger.Error("Error processing batch classification task", "error", err)
			var origEnv core.Envelope
			if envErr := json.Unmarshal(msg.Data, &origEnv); envErr == nil {
				// Reply with a FAILURE envelope if the process fails.
				a.ReplyFailure(msg, origEnv, err)
			} else {
				msg.Nak()
			}
			return
		}
		msg.Ack()
	}

	// Initialize the base agent with the defined handler.
	a.BaseAgent = agent.NewBaseAgent(env.Logger, env.Bus, env.Config, handler)
	return &a
}

// handleCFP processes FIPA Call For Proposal (CFP) messages.
// It automatically replies with a PROPOSE message if it recognizes the CFP.
func (a *GenericBatchAgent) handleCFP(msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		a.Logger.Error("Failed to parse envelope", "error", err)
		return nil // Ignore malformed envelopes
	}

	if env.Performative != core.CFP {
		return nil // Ignore non-CFP messages
	}

	// Draft a proposal for the work.
	proposal := map[string]interface{}{
		"price": 1,
		"eta":   "10s", // Batch tasks may take a bit longer
	}

	// Send a PROPOSE envelope back to the requester.
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

	// Start executing the task immediately after proposing.
	return a.executeTask(env)
}

// executeTask handles the actual processing of the batch task.
func (a *GenericBatchAgent) executeTask(cfpEnv core.Envelope) error {
	var task core.TaskDefinition
	if err := json.Unmarshal(cfpEnv.Body, &task); err != nil {
		return err
	}

	// 1. Read and unwrap the payload correctly. The payload is the data to be processed.
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

	// 2. State Preparation: Redux in this system forbids arrays to prevent index-shift bugs.
	// We must recursively convert all arrays in the payload to maps with stringified indices.
	var rawPayload interface{}
	_ = json.Unmarshal(task.Payload, &rawPayload)
	mapifiedPayload := mapify(rawPayload)
	mapifiedBytes, _ := json.Marshal(mapifiedPayload)

	// 3. Configure the Redux workflow.
	wfCfg := agent.WorkflowConfig{
		SchemaString: schema,
		InitialState: mapifiedBytes,
		// RBAC for batch agents needs to allow full state access since paths are dynamic.
		RBAC: redux.RBACPolicy{
			AllowedPrefixes: map[string][]string{
				a.Cfg.DID: {"/"},
			},
		},
	}

	// 4. Define the LLM callback. This is where the actual logic resides.
	llmCallback := func(previousFaults []redux.DomainFault, currentSeq uint64, baseState []byte) ([]json.RawMessage, error) {
		// Prepare a sanitized version of the payload for the LLM prompt.
		sanitizedPayload := make(map[string]interface{})
		for k, v := range shapedPayload {
			if k != "entity_id" {
				sanitizedPayload[k] = v
			}
		}

		promptCtx, _ := json.MarshalIndent(sanitizedPayload, "", "  ")
		userPrompt := fmt.Sprintf("### BATCH INPUT DATA\n%s\n", string(promptCtx))

		// Append the actual LLM system prompt from the Workflow Step.
		systemPrompt := task.SystemPrompt
		if systemPrompt == "" {
			return nil, fmt.Errorf("generic batch agent requires a system_prompt defined in the workflow step")
		}

		// Execute the LLM request.
		respText, err := a.RT.Exec(context.Background(), userPrompt, systemPrompt)
		if err != nil {
			return nil, err
		}

		// Extract and return the JSON patches directly from the LLM response.
		patches, err := a.extractJSONPatches(respText)
		if err != nil {
			return nil, fmt.Errorf("failed to extract JSON patches: %w", err)
		}

		return patches, nil
	}

	// 5. Define the onComplete handler. This sends the proof back to the orchestrator.
	onComplete := func(nextState []byte) error {
		proof := core.Proof{
			TaskID:    task.ID,
			Type:      core.ProofAPI,
			Data:      json.RawMessage(nextState),
			Timestamp: time.Now().Unix(),
		}
		proof.Signature = a.KP.Sign(proof.Data)

		// INFORM the orchestrator of the completed work.
		proofEnv, _ := core.NewEnvelope(uuid.New().String(), a.Cfg.DID, "did:toro:hive", cfpEnv.ConversationID, core.INFORM, proof)
		proofEnv.Signature = a.KP.Sign(proofEnv.Body)

		finalBytes, _ := json.Marshal(proofEnv)
		return a.Bus.Publish(workflows.OrchestratorInbox, finalBytes)
	}

	// 6. Execute the workflow locally and statelessly.
	return a.ExecuteLocalWorkflow(
		context.Background(),
		task.ID,
		wfCfg,
		llmCallback,
		onComplete,
	)
}

// extractJSONPatches identifies and extracts a JSON array from a text string.
// This is used to parse RFC6902 patches returned by the LLM.
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

// mapify recursively converts all JSON arrays into maps with stringified indices.
// This is a requirement for the Redux store to prevent index-shift vulnerabilities
// when applying LLM-generated patches to dynamic data structures.
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
