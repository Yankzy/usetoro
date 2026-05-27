package omnichat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/agents"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/redux"
)

const AgentName = "omni-chat-agent"

// Register the agent with the global agent registry during package initialization.
func init() {
	agents.Register(AgentName, NewAgent)
}

// OmniChatAgent is a generic chat agent that handles multi-channel communication.
// It fetches conversation history from the database to provide context to the LLM.
type OmniChatAgent struct {
	*agent.BaseAgent
	RT      *agent.Runtime
	Queries *database.Queries
}

// NewAgent initializes and returns a new instance of OmniChatAgent.
func NewAgent(env core.Environment) core.Runnable {
	var a OmniChatAgent
	a.RT = agent.NewRuntime(env.Logger, env.Bus, env.Config)
	a.Queries = env.Queries

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
			a.Logger.Error("Error processing chat task", "error", err)
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
func (a *OmniChatAgent) handleCFP(msg *nats.Msg) error {
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
		"price": 0,    // Chat is usually free in this context
		"eta":   "2s", // Chat should be fast
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

// executeTask handles the actual processing of the chat message using the Redux engine.
func (a *OmniChatAgent) executeTask(cfpEnv core.Envelope) error {
	var task core.TaskDefinition
	if err := json.Unmarshal(cfpEnv.Body, &task); err != nil {
		return err
	}

	// 1. Read and unwrap the payload correctly.
	var payload map[string]interface{}
	if err := core.UnmarshalTaskPayload(task.Payload, &payload); err != nil {
		return err
	}

	fromHandle := core.RowString(payload, "from_handle")
	toHandle := core.RowString(payload, "to_handle")
	userMessage := core.RowString(payload, "body_text")

	if fromHandle == "" || userMessage == "" {
		return fmt.Errorf("missing from_handle or body_text in chat payload")
	}

	// 2. Fetch Conversation Context from Database.
	history, err := a.Queries.GetRecentConversations(context.Background(), database.GetRecentConversationsParams{
		FromHandle: fromHandle,
		ToHandle:   toHandle,
		Limit:      int32(10),
	})
	if err != nil {
		a.Logger.Warn("Failed to fetch conversation history, proceeding without context", "error", err)
	}

	// 3. Prepare Redux Initial State.
	// We mapify the history to prevent array-shift vulnerabilities in the Redux store.
	historyList := make([]map[string]string, 0)
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		role := "user"
		if msg.FromHandle == toHandle {
			role = "assistant"
		}
		historyList = append(historyList, map[string]string{
			"role":    role,
			"content": msg.BodyText.String,
		})
	}

	initialState := map[string]interface{}{
		"history": mapify(historyList),
		"reply":   "",
	}
	stateBytes, _ := json.Marshal(initialState)

	// 4. Configure the Redux workflow.
	wfCfg := agent.WorkflowConfig{
		SchemaString: `{"type":"object","properties":{"reply":{"type":"string"}},"required":["reply"]}`,
		InitialState: stateBytes,
		RBAC: redux.RBACPolicy{
			AllowedPrefixes: map[string][]string{
				a.Cfg.DID: {"/reply"},
			},
		},
	}

	// 5. Define the LLM callback.
	llmCallback := func(previousFaults []redux.DomainFault, currentSeq uint64, baseState []byte) ([]json.RawMessage, error) {
		// Prepare history text for the prompt
		var historyText strings.Builder
		for i, msg := range historyList {
			historyText.WriteString(fmt.Sprintf("%s: %s\n", msg["role"], msg["content"]))
			if i > 10 {
				break
			}
		}

		systemPrompt := task.SystemPrompt
		if systemPrompt == "" {
			systemPrompt = `
You are an assistant for a business. Your job is to respond to user messages and provide help.
Be extremely concise and helpful. Do not repeat yourself. 
### OUTPUT FORMAT
RFC 6902 Compliance: You MUST reply with a JSON containing a SINGLE RFC 6902 patch that replaces the entire "/reply" field.

Example: 
{
	"op": "add",
	"path": "/reply",
	"value": "Your response here"
}
`
		}

		userPrompt := fmt.Sprintf("### CONVERSATION HISTORY\n%s\n\n### CURRENT MESSAGE\nUser: %s\n", historyText.String(), userMessage)

		// 5.1 Prepare Paging Context.
		// Instead of dumping everything into the prompt, we use the Paging Runtime.
		pages := make([]agent.PageContext, 0)
		for _, msg := range history {
			pages = append(pages, agent.PageContext{
				Type:    "message",
				Summary: fmt.Sprintf("From %s at %s", msg.FromHandle, msg.CreatedAt.Time.Format(time.Kitchen)),
				UUID:    uuid.UUID(msg.ID.Bytes).String(),
			})
		}

		// DocumentFetcher resolves the UUID back to the full message content.
		fetcher := func(ctx context.Context, uuidStr string) (string, error) {
			for _, msg := range history {
				if uuid.UUID(msg.ID.Bytes).String() == uuidStr {
					role := "user"
					if msg.FromHandle == toHandle {
						role = "assistant"
					}
					return fmt.Sprintf("ROLE: %s\nCONTENT: %s", role, msg.BodyText.String), nil
				}
			}
			return "", fmt.Errorf("message not found")
		}

		// Feed back any previous faults for self-correction
		if len(previousFaults) > 0 {
			faultsJson, _ := json.Marshal(previousFaults)
			userPrompt += fmt.Sprintf("\n### PREVIOUS ERRORS (Self-Correct These)\n%s\n", string(faultsJson))
		}

		ctx := agent.WithModel(context.Background(), task.Model)
		respText, err := a.RT.ExecWithPaging(ctx, userPrompt, systemPrompt, pages, fetcher)
		if err != nil {
			return nil, err
		}

		// Extract and return the JSON patches.
		patches, err := a.extractJSONPatches(respText)
		if err != nil {
			return nil, fmt.Errorf("failed to extract JSON patches: %w", err)
		}

		return patches, nil
	}

	// 6. Define the onComplete handler.
	onComplete := func(nextState []byte) error {
		var finalState struct {
			Reply string `json:"reply"`
		}
		if err := json.Unmarshal(nextState, &finalState); err != nil {
			return fmt.Errorf("failed to parse final state: %w", err)
		}

		response := map[string]interface{}{
			"body_text":   finalState.Reply,
			"from_handle": toHandle,
			"to_handle":   fromHandle,
			"source":      core.RowString(payload, "source"),
			"subject":     core.RowString(payload, "subject"),
			"in_reply_to": core.RowString(payload, "in_reply_to"),
		}

		responseBytes, _ := json.Marshal(response)

		proof := core.Proof{
			TaskID:    task.ID,
			Type:      core.ProofAPI,
			Data:      json.RawMessage(responseBytes),
			Timestamp: time.Now().Unix(),
		}
		proof.Signature = a.KP.Sign(proof.Data)

		proofEnv, _ := core.NewEnvelope(uuid.New().String(), a.Cfg.DID, "did:toro:hive", cfpEnv.ConversationID, core.INFORM, proof)
		proofEnv.Signature = a.KP.Sign(proofEnv.Body)

		finalBytes, _ := json.Marshal(proofEnv)

		outputSubject := a.Cfg.OutputSubject
		if outputSubject == "" {
			outputSubject = "proof.outgoing.chat"
		}
		return a.Bus.Publish(outputSubject, finalBytes)
	}

	// 7. Execute the workflow via the Redux engine.
	return a.ExecuteLocalWorkflow(
		context.Background(),
		task.ID,
		wfCfg,
		llmCallback,
		onComplete,
	)
}

// extractJSONPatches identifies and extracts a JSON array or object from a text string.
func (a *OmniChatAgent) extractJSONPatches(respText string) ([]json.RawMessage, error) {
	// Try finding an array first
	startArr := strings.Index(respText, "[")
	endArr := strings.LastIndex(respText, "]")

	if startArr != -1 && endArr != -1 && startArr < endArr {
		cleanJSON := respText[startArr : endArr+1]
		var patches []json.RawMessage
		if err := json.Unmarshal([]byte(cleanJSON), &patches); err == nil {
			return patches, nil
		}
	}

	// Fallback: Try finding a single object
	startObj := strings.Index(respText, "{")
	endObj := strings.LastIndex(respText, "}")

	if startObj != -1 && endObj != -1 && startObj < endObj {
		cleanJSON := respText[startObj : endObj+1]
		// Validate it's at least valid JSON
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(cleanJSON), &m); err == nil {
			return []json.RawMessage{json.RawMessage(cleanJSON)}, nil
		}
	}

	return nil, fmt.Errorf("no valid JSON array or object found in LLM response: %s", respText)
}

// mapify recursively converts all JSON arrays into maps with stringified indices.
func mapify(v interface{}) interface{} {
	switch val := v.(type) {
	case []interface{}:
		m := make(map[string]interface{})
		for i, item := range val {
			m[fmt.Sprintf("%d", i)] = mapify(item)
		}
		return m
	case []map[string]string: // Specific case for our history list
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
