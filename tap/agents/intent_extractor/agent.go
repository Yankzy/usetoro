package intentextractor

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/tap/agents"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/workflows"
)

const AgentName = "intent-extractor-agent"



type IntentExtractorAgent struct {
	*agent.BaseAgent
	rt *agent.Runtime
}

// taskPayload is the opaque input envelope delivered by the orchestrator or trigger harness.
// It is intentionally kept generic — domain-specific fields such as intents or entity types
// are described in the system_prompt and workflow_schema YAML config, not in Go code.
type taskPayload struct {
	Text        string `json:"text"`
	PhoneNumber string `json:"phone_number"`
	Domain      string `json:"domain,omitempty"`
}

func init() {
	agents.Register(AgentName, NewAgent)
}

func NewAgent(env core.Environment) core.Runnable {
	var a IntentExtractorAgent
	a.rt = agent.NewRuntime(env.Logger, env.Bus, env.Config)

	handler := func(msg *nats.Msg) {
		a.Logger.Info("📡 [DEBUG] intent-extractor-agent received JetStream message", "topic", msg.Subject, "data_length", len(msg.Data))

		meta, metaErr := msg.Metadata()
		if metaErr == nil && meta.NumDelivered > 3 {
			a.Logger.Error("Poison pill detected, terminating message", "subject", msg.Subject)
			msg.Term()
			return
		}

		if err := a.handleCFP(msg); err != nil {
			a.Logger.Error("Transient error processing message, replying with FAILURE", "error", err)
			
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

func (a *IntentExtractorAgent) handleCFP(msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		a.Logger.Error("Failed to parse envelope", "error", err)
		return nil
	}

	if env.Performative != core.CFP && env.Performative != core.ACCEPT_PROPOSAL {
		return nil
	}

	if env.Performative == core.CFP {
		a.Logger.Info("📨 Received CFP", "sender", env.SenderDID, "cid", env.ConversationID)

		proposal := map[string]interface{}{
			"price": 1,
			"eta":   "5s",
		}

		replyEnv, rErr := core.NewEnvelope(
			uuid.New().String(),
			a.Cfg.DID,
			env.SenderDID,
			env.ConversationID,
			core.PROPOSE,
			proposal,
		)
		if rErr != nil {
			a.Logger.Error("Failed to create reply envelope", "error", rErr)
			return rErr
		}
		// Sign using BaseAgent's KeyPair
		replyEnv.Signature = a.KP.Sign(replyEnv.Body)
		replyBytes, bErr := json.Marshal(replyEnv)
		if bErr != nil {
			a.Logger.Error("Failed to marshal reply envelope", "error", bErr)
			return bErr
		}

		inbox := core.BuildAgentInbox(env.SenderDID)
		if err := a.Bus.Publish(inbox, replyBytes); err != nil {
			a.Logger.Error("Failed to publish proposal", "error", err)
			return err
		}
	}

	// Auto execute immediately; internal agents don't need to wait for ACCEPT_PROPOSAL.
	return a.executeTask(env)
}

func (a *IntentExtractorAgent) executeTask(cfpEnv core.Envelope) error {
	var task core.TaskDefinition
	if err := json.Unmarshal(cfpEnv.Body, &task); err != nil {
		a.Logger.Error("Failed to parse task definition", "error", err)
		return nil
	}

	var payload taskPayload
	if err := core.UnmarshalTaskPayload(task.Payload, &payload); err != nil {
		a.Logger.Error("Failed to parse task payload", "error", err)
		return nil
	}

	// Prefer workflow-scoped schema from the TaskDefinition; fall back to agent config, then minimal default.
	schema := task.WorkflowSchema
	if strings.TrimSpace(schema) == "" {
		schema = a.Cfg.WorkflowSchema
	}

	a.Logger.Info("🧠 Extracting intent via LLM", "workflow_id", task.ID, "phone", payload.PhoneNumber, "domain", payload.Domain, "schema_override", schema != "")

	result, err := a.extractIntentUsingLLM(agent.WithModel(context.Background(), task.Model), task.SystemPrompt, payload.Text, schema)
	if err != nil {
		a.Logger.Error("Failed to extract intent from LLM", "error", err)
		return err // Transient error → NAK + retry
	}

	// Build exact proof shape expected by orchestrator.
	proof := core.Proof{
		TaskID:    task.ID,
		Type:      core.ProofAPI,
		Data:      result,
		Timestamp: time.Now().Unix(),
	}
	proof.Signature = a.KP.Sign(proof.Data)

	proofEnv, _ := core.NewEnvelope(
		uuid.New().String(),
		a.Cfg.DID,
		"did:toro:hive", // Target is orchestrator logically
		cfpEnv.ConversationID,
		core.INFORM,
		proof,
	)
	proofEnv.Signature = a.KP.Sign(proofEnv.Body)

	finalBytes, _ := json.Marshal(proofEnv)

	targetTopic := workflows.OrchestratorInbox
	a.Logger.Info("🚀 Publishing validated proof to Orchestrator", "topic", targetTopic, "data_length", len(finalBytes))

	if pubErr := a.Bus.Publish(targetTopic, finalBytes); pubErr != nil {
		a.Logger.Error("Failed to publish proof", "error", pubErr)
		return pubErr
	}

	return nil
}

// extractIntentUsingLLM calls the LLM with the provided text and returns the raw JSON result.
//
// The output schema and domain-specific intent labels are driven entirely by:
//   - schemaHint            → Optional workflow-scoped JSON schema string (per TaskDefinition)
//   - a.Cfg.WorkflowSchema  → Agent-level default if schemaHint is empty
//   - a.Cfg.SystemPrompt    → role + intent vocabulary, both set via YAML config
//
// This makes the agent fully domain-agnostic: switching from roofing to ride-hailing (or
// any other vertical) requires only a YAML config change — no Go recompile.
func (a *IntentExtractorAgent) extractIntentUsingLLM(ctx context.Context, systemPrompt string, text string, schemaHint string) (json.RawMessage, error) {
	schema := schemaHint

	prompt := "Text to classify:\n\n" + text +
		"\n\nRespond with ONLY a JSON object that exactly matches this schema (no markdown, no explanation):\n" + schema

	// ExecWithPaging owns the system-level paging context.
	respText, err := a.rt.ExecWithPaging(ctx, prompt, systemPrompt, nil, nil)
	if err != nil {
		return nil, err
	}

	a.Logger.Debug("🤖 Raw LLM response", "response", respText)

	return extractJSON(respText)
}

// extractJSON strips any markdown fencing and returns the first complete JSON object found.
func extractJSON(respText string) (json.RawMessage, error) {
	firstIdx := strings.Index(respText, "{")
	lastIdx := strings.LastIndex(respText, "}")
	if firstIdx != -1 && lastIdx != -1 && lastIdx > firstIdx {
		respText = respText[firstIdx : lastIdx+1]
	}

	var raw json.RawMessage
	if err := json.Unmarshal([]byte(respText), &raw); err != nil {
		return nil, err
	}
	return raw, nil
}
