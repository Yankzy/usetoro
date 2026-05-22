package genericbatch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/tap/agents"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

const AgentName = "generic-batch-agent"

func init() {
	agents.Register(AgentName, NewAgent)
}

// GenericBatchAgent processes batches of transactions through a single LLM
// call. It receives a CFP, proposes, calls the LLM with the provided rows
// and system prompt, and replies with the extracted patches to the caller.
type GenericBatchAgent struct {
	*agent.BaseAgent
	RT        *agent.Runtime
	semaphore chan struct{} // bounds concurrent LLM calls
}

func NewAgent(env core.Environment) core.Runnable {
	var a GenericBatchAgent
	a.RT = agent.NewRuntime(env.Logger, env.Bus, env.Config)
	a.semaphore = make(chan struct{}, 10)

	handler := func(msg *nats.Msg) {
		a.semaphore <- struct{}{} // acquire
		go func() {
			defer func() { <-a.semaphore }() // release
			defer func() {
				if r := recover(); r != nil {
					a.Logger.Error("agent panic recovered", "panic", r, "subject", msg.Subject)
					msg.Nak()
				}
			}()

			meta, metaErr := msg.Metadata()
			if metaErr == nil && meta.NumDelivered > 3 {
				a.Logger.Error("Poison pill detected, terminating message", "subject", msg.Subject)
				msg.Term()
				return
			}

			if err := a.handleCFP(msg); err != nil {
				a.Logger.Error("Error processing batch task", "error", err)
				var origEnv core.Envelope
				if envErr := json.Unmarshal(msg.Data, &origEnv); envErr == nil {
					a.ReplyFailure(msg, origEnv, err)
				} else {
					msg.Nak()
				}
				return
			}
			msg.Ack()
		}()
	}

	a.BaseAgent = agent.NewBaseAgent(env.Logger, env.Bus, env.Config, handler)
	return &a
}

func (a *GenericBatchAgent) handleCFP(msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		a.Logger.Error("Failed to parse envelope", "error", err)
		return nil
	}

	if env.Performative != core.CFP {
		return nil
	}

	proposal := map[string]interface{}{
		"price": 1,
		"eta":   "10s",
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

	// Unwrap the payload to get rows and context.
	var shapedPayload map[string]interface{}
	if err := core.UnmarshalTaskPayload(task.Payload, &shapedPayload); err != nil {
		return err
	}

	rows, ok := shapedPayload["rows"]
	if !ok {
		return fmt.Errorf("generic batch agent: missing 'rows' in payload")
	}

	promptCtx, _ := json.MarshalIndent(rows, "", "  ")
	userPrompt := fmt.Sprintf("### BATCH INPUT DATA\n%s\n", string(promptCtx))

	systemPrompt := task.SystemPrompt
	if systemPrompt == "" {
		return fmt.Errorf("generic batch agent requires a system_prompt defined in the workflow step")
	}

	ctx := agent.WithModel(context.Background(), task.Model)
	respText, err := a.RT.Exec(ctx, userPrompt, systemPrompt)
	if err != nil {
		return err
	}

	patches, err := extractJSONPatches(respText)
	if err != nil {
		return fmt.Errorf("failed to extract JSON patches: %w", err)
	}

	// Reply directly to the CFP sender (the worker that dispatched this task),
	// not the hardcoded OrchestratorInbox.
	proof := core.Proof{
		TaskID:    task.ID,
		Type:      core.ProofAPI,
		Data:      mustMarshalJSON(patches),
		Timestamp: time.Now().Unix(),
	}
	proof.Signature = a.KP.Sign(proof.Data)

	proofEnv, _ := core.NewEnvelope(
		uuid.New().String(),
		a.Cfg.DID,
		cfpEnv.SenderDID,
		cfpEnv.ConversationID,
		core.INFORM,
		proof,
	)
	proofEnv.Signature = a.KP.Sign(proofEnv.Body)

	finalBytes, _ := json.Marshal(proofEnv)
	return a.Bus.Publish(core.BuildAgentInbox(cfpEnv.SenderDID), finalBytes)
}

func mustMarshalJSON(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return json.RawMessage(b)
}

// extractJSONPatches finds and extracts a JSON array or object from LLM response text.
func extractJSONPatches(respText string) ([]json.RawMessage, error) {
	startArr := strings.Index(respText, "[")
	endArr := strings.LastIndex(respText, "]")

	if startArr != -1 && endArr != -1 && startArr < endArr {
		cleanJSON := respText[startArr : endArr+1]
		var patches []json.RawMessage
		if err := json.Unmarshal([]byte(cleanJSON), &patches); err == nil {
			return patches, nil
		}
	}

	startObj := strings.Index(respText, "{")
	endObj := strings.LastIndex(respText, "}")

	if startObj != -1 && endObj != -1 && startObj < endObj {
		cleanJSON := respText[startObj : endObj+1]
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(cleanJSON), &m); err == nil {
			return []json.RawMessage{json.RawMessage(cleanJSON)}, nil
		}
	}

	return nil, fmt.Errorf("no valid JSON array or object found in LLM response: %s", respText)
}
