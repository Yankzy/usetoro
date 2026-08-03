// agent.go
package ocr_agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/agents"
	csvmapping "github.com/Yankzy/usetoro/tap/agents/csv_mapping"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/redux"
	"github.com/nats-io/nats.go"
)

// Agent Specification
// - Agent Display Name: "OCR_Agent"
// - Package Name: ocr_agent (directory: tap/agents/ocr_agent/)
// - internal_module key: "ocr_agent"
// - activity_type: "agents.ocr"
// - Purpose / Business Logic: Standalone general OCR document agent. Analyzes incoming document images, PDFs, bank statements, receipts, and invoices using OpenAI visual upload.
// - Input Envelope Shape: TaskDefinition with Payload containing OCRTaskPayload (DocumentURL, S3Key, AttachmentName, SHA256).
// - Output Proof Shape: Standard TAP core.Proof containing OCRExtraction sent to caller.

type OCRTaskPayload struct {
	SessionID               string `json:"session_id,omitempty"`
	DocumentURL             string `json:"document_url,omitempty"`
	S3Key                   string `json:"s3_key,omitempty"`
	SHA256                  string `json:"sha256,omitempty"`
	AttachmentName          string `json:"attachment_name,omitempty"`
	AttachmentIndex         int    `json:"attachment_index,omitempty"`
	TotalAttachments        int    `json:"total_attachments,omitempty"`
	FinalDestinationSubject string `json:"final_destination_subject,omitempty"`
	DocTypeHint             string `json:"doc_type_hint,omitempty"`
}

type OCRExtraction struct {
	DocType       string                       `json:"doc_type"`
	SHA256        string                       `json:"sha256,omitempty"`
	S3Key         string                       `json:"s3_key,omitempty"`
	FileName      string                       `json:"file_name,omitempty"`
	Data          map[string]interface{}       `json:"data"`
	ColumnMapping *csvmapping.LLMColumnMapping `json:"column_mapping,omitempty"`
	Confidence    float64                      `json:"confidence"`
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

func (a *OCRAgent) handleCFP(msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		a.Logger.Error("Failed to parse envelope", "error", err)
		return nil
	}

	if env.Performative != core.CFP && env.Performative != core.ACCEPT_PROPOSAL && env.Performative != core.REQUEST {
		// Ignore unsupported performatives
		return nil
	}

	if env.Performative == core.CFP {
		a.Logger.Info("📨 Received CFP for OCR job", "sender", env.SenderDID, "cid", env.ConversationID)

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

	// Auto execute task on CFP, ACCEPT_PROPOSAL, or REQUEST
	return a.executeTask(env)
}

func (a *OCRAgent) fetchDocumentBytes(ctx context.Context, payload OCRTaskPayload) ([]byte, error) {
	docURL := payload.DocumentURL
	if docURL == "" {
		docURL = payload.S3Key
	}

	if strings.HasPrefix(docURL, "http://") || strings.HasPrefix(docURL, "https://") {
		req, err := http.NewRequestWithContext(ctx, "GET", docURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch document: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read document body: %w", err)
		}
		return body, nil
	}

	if docURL != "" {
		if fileBytes, err := os.ReadFile(docURL); err == nil {
			return fileBytes, nil
		}
	}
	if payload.AttachmentName != "" {
		if fileBytes, err := os.ReadFile(payload.AttachmentName); err == nil {
			return fileBytes, nil
		}
	}

	return []byte(fmt.Sprintf("DOCUMENT_CONTENT_FOR: %s (%s)", payload.AttachmentName, docURL)), nil
}

func (a *OCRAgent) executeTask(env core.Envelope) error {
	var task core.TaskDefinition
	if err := json.Unmarshal(env.Body, &task); err != nil {
		return fmt.Errorf("failed to parse task definition: %w", err)
	}

	var payload OCRTaskPayload
	if err := core.UnmarshalTaskPayload(task.Payload, &payload); err != nil {
		return fmt.Errorf("failed to parse task payload: %w", err)
	}

	a.Logger.Info("🧠 General OCR Agent executing task",
		"task_id", task.ID,
		"attachment_name", payload.AttachmentName,
	)

	fileBytes, fetchErr := a.fetchDocumentBytes(context.Background(), payload)
	if fetchErr != nil {
		return fmt.Errorf("failed to fetch document bytes: %w", fetchErr)
	}

	var extraction *OCRExtraction

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

		ext, err := a.extractDocumentUsingLLM(context.Background(), task, payload, fileBytes)
		if err != nil {
			return nil, err
		}
		extraction = ext

		extJSON, _ := json.Marshal(ext)

		patch1 := `{"op": "add", "path": "/status", "value": "OCR_COMPLETED"}`
		patch2 := fmt.Sprintf(`{"op": "add", "path": "/extracted_data", "value": %s}`, string(extJSON))

		patches = append(patches, []byte(patch1), []byte(patch2))
		return patches, nil
	}

	onComplete := func(nextState []byte) error {
		a.Logger.Info("✅ Redux-validated state received for OCR task")
		return nil
	}

	if a.queries != nil && workflowID.Valid {
		if wfErr := a.ExecuteGlobalWorkflow(
			context.Background(),
			a.queries,
			workflowID,
			wfCfg,
			llmCallback,
			onComplete,
		); wfErr != nil {
			a.Logger.Warn("ExecuteGlobalWorkflow non-fatal error", "error", wfErr)
		}
	}

	if extraction == nil {
		ext, err := a.extractDocumentUsingLLM(context.Background(), task, payload, fileBytes)
		if err != nil {
			return err
		}
		extraction = ext
	}

	// Publish TAP Proof / Inform envelope back to sender
	extJSON, _ := json.Marshal(extraction)
	proof := core.Proof{
		TaskID:    task.ID,
		Type:      core.ProofAPI,
		Data:      extJSON,
		Timestamp: time.Now().Unix(),
	}
	proof.Signature = a.KP.Sign(proof.Data)

	replyTarget := core.BuildAgentInbox(env.SenderDID)

	if replyTarget != "" && a.Bus != nil {
		informEnv, _ := core.NewEnvelope(
			uuid.New().String(),
			a.Cfg.DID,
			env.SenderDID,
			env.ConversationID,
			core.INFORM,
			proof,
		)
		informEnv.Signature = a.KP.Sign(informEnv.Body)
		informBytes, _ := json.Marshal(informEnv)

		a.Logger.Info("🚀 Sending structured OCR extraction proof", "target", replyTarget)
		if pubErr := a.Bus.Publish(replyTarget, informBytes); pubErr != nil {
			a.Logger.Error("Failed to publish OCR proof", "target", replyTarget, "error", pubErr)
		}
	}

	// Forward caller context + OCR extraction to final destination subject if requested
	if payload.FinalDestinationSubject != "" && a.Bus != nil {
		var rawMap map[string]interface{}
		_ = json.Unmarshal(task.Payload, &rawMap)
		if rawMap == nil {
			rawMap = make(map[string]interface{})
		}
		rawMap["ocr_extraction"] = extraction

		forwardBytes, _ := json.Marshal(rawMap)
		a.Logger.Info("🚀 Forwarding OCR extraction to destination subject", "subject", payload.FinalDestinationSubject)
		if pubErr := a.Bus.Publish(payload.FinalDestinationSubject, forwardBytes); pubErr != nil {
			a.Logger.Error("Failed to publish OCR output to destination subject", "subject", payload.FinalDestinationSubject, "error", pubErr)
			return pubErr
		}
	}

	return nil
}

func (a *OCRAgent) extractDocumentUsingLLM(ctx context.Context, task core.TaskDefinition, payload OCRTaskPayload, fileBytes []byte) (*OCRExtraction, error) {
	systemPrompt := `You are an expert OCR document parser. Analyze the document and extract structured JSON matching one of these exact types:

1. Bank Statement (doc_type: "bank_statement"):
   Extract 'bank_name', 'account_number', 'statement_date', 'period', 'starting_balance', 'ending_balance', and 'transactions' list containing items with 'date', 'description', 'amount', 'type' ('debit'/'credit').
   CRITICAL FOR BANK STATEMENTS:
   - Do NOT drop debit/credit, withdrawal/deposit, or polarity sign indicators ('minus', 'brackets', 'none').
   - Also return a 'column_mapping' object conforming to:
     {
       "date_col_idx": 0,
       "description_col_idx": 1,
       "amount_col_idx": 2,
       "is_split_amount": true/false,
       "debit_col_idx": null,
       "credit_col_idx": null,
       "vendor_col_idx": null,
       "customer_col_idx": null,
       "confidence_score": 0.95,
       "is_ambiguous": true/false,
       "ambiguity_reason": null,
       "polarity_sign": "minus" | "brackets" | "none",
       "source_account": "bank name"
     }

2. Invoice (doc_type: "invoice"):
   Extract 'vendor_name', 'invoice_number', 'date', 'total_mad', 'ht_mad', 'tva_mad', and 'line_items'.

3. Receipt (doc_type: "receipt"):
   Extract 'vendor_name', 'date', 'total_mad', and 'payment_method'.

4. Other (doc_type: "other"):
   Extract general key-value metadata.

Return ONLY a valid JSON object matching this structure:
{
  "doc_type": "bank_statement" | "invoice" | "receipt" | "other",
  "confidence": 0.95,
  "data": { ... extracted fields ... },
  "column_mapping": { ... optional column mapping for bank statements ... }
}`

	userPrompt := fmt.Sprintf("Document Name: %s\nPlease perform visual OCR inspection on the attached document and extract structured JSON matching the system instructions.",
		payload.AttachmentName,
	)

	ctx = agent.WithModel(ctx, task.Model)
	a.Logger.Info("Processing OCR document via ExecDocument", "name", payload.AttachmentName, "size_bytes", len(fileBytes))

	respText, err := a.rt.ExecDocument(ctx, userPrompt, systemPrompt, fileBytes, payload.AttachmentName)
	if err != nil {
		return nil, fmt.Errorf("LLM document OCR extraction failed: %w", err)
	}

	firstIdx := strings.Index(respText, "{")
	lastIdx := strings.LastIndex(respText, "}")
	if firstIdx != -1 && lastIdx != -1 && lastIdx > firstIdx {
		respText = respText[firstIdx : lastIdx+1]
	}

	var parsed struct {
		DocType       string                       `json:"doc_type"`
		Confidence    float64                      `json:"confidence"`
		Data          map[string]interface{}       `json:"data"`
		ColumnMapping *csvmapping.LLMColumnMapping `json:"column_mapping"`
	}

	if err := json.Unmarshal([]byte(respText), &parsed); err != nil {
		a.Logger.Warn("failed to parse structured JSON from LLM OCR, using fallback", "raw", respText, "error", err)
		parsed.DocType = "other"
		parsed.Data = map[string]interface{}{"raw_response": respText}
	}

	return &OCRExtraction{
		DocType:       parsed.DocType,
		SHA256:        payload.SHA256,
		S3Key:         payload.S3Key,
		FileName:      payload.AttachmentName,
		Data:          parsed.Data,
		ColumnMapping: parsed.ColumnMapping,
		Confidence:    parsed.Confidence,
	}, nil
}
