// agent.go
package ocr_agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// Agent Specification
// - Agent Display Name: "OCR_Agent"
// - Package Name: ocr_agent (directory: tap/agents/ocr_agent/)
// - internal_module key: "ocr_agent"
// - activity_type: "agents.ocr"
// - Purpose / Business Logic: Analyzes incoming MMS images, receipts, and other documents to extract structured context.
// - Input Envelope Shape: TaskDefinition with Payload containing OCRTaskPayload (Document URL/URI).
// - Output Proof Shape: Publish extracted structured data in core.Proof.Data to workflows.OrchestratorInbox.
// - Uses Redux (ExecuteGlobalWorkflow): YES
// - Allowed state paths: ["/status", "/extracted_data"]

type OCRTaskPayload struct {
	SessionID   string `json:"session_id"`
	DocumentURL string `json:"document_url"`
}

type OCRExtraction struct {
	Text       string            `json:"text"`
	Entities   map[string]string `json:"entities"`
	Confidence float64           `json:"confidence"`
}

type OCRAgent struct {
	*agent.BaseAgent
	rt      *agent.Runtime
	queries *database.Queries
}

const AgentName = "ocr_agent"

func init() {
	agents.Register(AgentName, NewAgent)
}

func NewAgent(env core.Environment) core.Runnable {
	var a OCRAgent
	a.rt = agent.NewRuntime(env.Logger, env.Bus, env.Config)
	a.queries = env.Queries

	handler := func(msg *nats.Msg) {
		a.Logger.Info("📡 [DEBUG] ocr_agent received JetStream message", "topic", msg.Subject, "data_length", len(msg.Data))

		meta, metaErr := msg.Metadata()
		if metaErr == nil && meta.NumDelivered > 3 {
			a.Logger.Error("Poison pill detected, terminating message", "subject", msg.Subject)
			msg.Term()
			return
		}

		if err := a.handleCFP(msg); err != nil {
			a.Logger.Error("Transient error processing message, nacking", "error", err)
			msg.Nak()
			return
		}

		msg.Ack()
	}

	a.BaseAgent = agent.NewBaseAgent(env.Logger, env.Bus, env.Config, handler)
	return &a
}

func (a *OCRAgent) handleCFP(msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		a.Logger.Error("Failed to parse envelope", "error", err)
		return nil
	}

	if env.Performative != core.CFP && env.Performative != core.ACCEPT_PROPOSAL {
		// Ignore unsupported performatives
		return nil
	}

	if env.Performative == core.CFP {
		a.Logger.Info("📨 Received CFP", "sender", env.SenderDID, "cid", env.ConversationID)

		proposal := map[string]interface{}{
			"price": 1,
			"eta":   "15s",
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
			a.Logger.Error("Failed to publish proposal", "error", err)
			return err
		}
	}

	// Auto execute immediately from CFP, or on ACCEPT_PROPOSAL for forward compatibility
	return a.executeTask(env)
}

func (a *OCRAgent) executeTask(env core.Envelope) error {
	var task core.TaskDefinition
	if err := json.Unmarshal(env.Body, &task); err != nil {
		return fmt.Errorf("failed to parse task definition: %w", err)
	}

	var payload OCRTaskPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("failed to parse task payload: %w", err)
	}

	a.Logger.Info("🧠 Processing OCR via Redux global wrapper",
		"workflow_id", task.ID,
		"session_id", payload.SessionID,
	)

	var workflowID pgtype.UUID
	if err := workflowID.Scan(task.ID); err != nil {
		return fmt.Errorf("invalid workflow UUID: %w", err)
	}

	schema := task.WorkflowSchema
	if strings.TrimSpace(schema) == "" {
		schema = a.Cfg.WorkflowSchema
	}
	// If still empty, allow empty schema (previous default) — Redux wrapper tolerates it.

	wfCfg := agent.WorkflowConfig{
		SchemaString: schema,
		RBAC: redux.RBACPolicy{
			AllowedPrefixes: map[string][]string{
				a.Cfg.DID: {"/status", "/extracted_data"},
			},
		},
	}

	llmCallback := func(previousFaults []redux.DomainFault, currentSeq uint64, baseState []byte) ([]json.RawMessage, error) {
		if len(previousFaults) > 0 {
			a.Logger.Warn("⚠️ Redux faults from previous attempt, retrying LLM", "faults", len(previousFaults))
		}

		var stateObj map[string]interface{}
		_ = json.Unmarshal(baseState, &stateObj)
		var patches []json.RawMessage

		if statusVal, exists := stateObj["status"]; exists {
			b, _ := json.Marshal(statusVal)
			patches = append(patches, []byte(fmt.Sprintf(`{"op": "test", "path": "/status", "value": %s}`, string(b))))
		}

		extraction, err := a.extractDocumentUsingLLM(context.Background(), payload)
		if err != nil {
			return nil, err
		}

		extJSON, _ := json.Marshal(extraction)

		patch1 := `{"op": "add", "path": "/status", "value": "OCR_COMPLETED"}`
		patch2 := fmt.Sprintf(`{"op": "add", "path": "/extracted_data", "value": %s}`, string(extJSON))

		patches = append(patches, []byte(patch1), []byte(patch2))
		return patches, nil
	}

	onComplete := func(nextState []byte) error {
		a.Logger.Info("✅ Redux-validated state received, publishing proof")

		var validatedState map[string]json.RawMessage
		if err := json.Unmarshal(nextState, &validatedState); err != nil {
			return fmt.Errorf("failed to parse validated state: %w", err)
		}

		proof := core.Proof{
			TaskID:    task.ID,
			Type:      core.ProofAPI,
			Data:      validatedState["extracted_data"],
			Timestamp: time.Now().Unix(),
		}
		proof.Signature = a.KP.Sign(proof.Data)

		proofEnv, _ := core.NewEnvelope(uuid.New().String(), a.Cfg.DID, "did:toro:hive", env.ConversationID, core.INFORM, proof)
		proofEnv.Signature = a.KP.Sign(proofEnv.Body)

		finalBytes, _ := json.Marshal(proofEnv)
		targetTopic := workflows.OrchestratorInbox
		a.Logger.Info("🚀 Publishing validated proof to Orchestrator", "topic", targetTopic)

		if pubErr := a.Bus.Publish(targetTopic, finalBytes); pubErr != nil {
			a.Logger.Error("Failed to publish proof", "error", pubErr)
			return pubErr
		}
		return nil
	}

	return a.ExecuteGlobalWorkflow(
		context.Background(),
		a.queries,
		workflowID,
		wfCfg,
		llmCallback,
		onComplete,
	)
}

func (a *OCRAgent) extractDocumentUsingLLM(ctx context.Context, payload OCRTaskPayload) (*OCRExtraction, error) {
	pages := []agent.PageContext{
		{
			Type:    "document",
			Summary: "The primary document requiring OCR extraction.",
			UUID:    payload.DocumentURL,
		},
	}

	fetcher := func(fetchCtx context.Context, uuidStr string) (string, error) {
		a.Logger.Info("Fetching document content via Page Tool", "url", uuidStr)
		if strings.HasPrefix(uuidStr, "http://") || strings.HasPrefix(uuidStr, "https://") {
			req, err := http.NewRequestWithContext(fetchCtx, "GET", uuidStr, nil)
			if err != nil {
				return "", fmt.Errorf("failed to create request: %w", err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return "", fmt.Errorf("failed to fetch document: %w", err)
			}
			defer resp.Body.Close()
			
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return "", fmt.Errorf("failed to read document body: %w", err)
			}
			// Note: If the payload is a binary image, we would pass 'body' to an external OCR API 
			// like AWS Textract or OpenAI Vision here. For text payloads, we return it directly.
			return string(body), nil
		}
		
		// Fallback for internal identifiers
		return fmt.Sprintf("MOCK_TEXT_FOR_LOCAL_UUID: %s", uuidStr), nil
	}

	prompt := "Extract the structured contents from the available document.\n\nUse the PAGE_IN tool to read the raw contents of the document before answering.\n\nReturn ONLY a JSON object with 'text' (full raw text), 'entities' (key-value pairs of found fields), and 'confidence' (float 0-1)."

	fullPrompt := fmt.Sprintf("%s\n\n%s", a.Cfg.SystemPrompt, prompt)

	respText, err := a.rt.ExecWithPaging(ctx, fullPrompt, pages, fetcher)
	if err != nil {
		return nil, err
	}

	firstIdx := strings.Index(respText, "{")
	lastIdx := strings.LastIndex(respText, "}")
	if firstIdx != -1 && lastIdx != -1 && lastIdx > firstIdx {
		respText = respText[firstIdx : lastIdx+1]
	}

	var extraction OCRExtraction
	if err := json.Unmarshal([]byte(respText), &extraction); err != nil {
		return nil, fmt.Errorf("failed to parse LLM response: %w", err)
	}

	return &extraction, nil
}
