package ocr_agent

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
// - Purpose / Business Logic: Native ToroDB specialized OCR document agent.
//   Analyzes document images, PDFs, bank statements, receipts, and invoices using presigned S3 file URLs directly via runtime.go and Redux engine validation.
// - Input Envelope Shape: TaskDefinition or raw NATS JSON payload (DocumentURL/S3URL/s3_key, AttachmentName, etc.).
// - Output Proof Shape: Enriched payload sent to final_destination_subject (defaulting to worker.inbox.go.knowledge_ingest) + TAP proof.

type OCRTaskPayload struct {
	SessionID               string `json:"session_id,omitempty"`
	DocumentID              string `json:"document_id,omitempty"`
	DocumentURL             string `json:"document_url,omitempty"`
	S3URL                   string `json:"s3_url,omitempty"`
	S3Key                   string `json:"s3_key,omitempty"`
	ImageURL                string `json:"image_url,omitempty"`
	URL                     string `json:"url,omitempty"`
	AttachmentURL           string `json:"attachment_url,omitempty"`
	SHA256                  string `json:"sha256,omitempty"`
	AttachmentName          string `json:"attachment_name,omitempty"`
	FileName                string `json:"file_name,omitempty"`
	AttachmentIndex         int    `json:"attachment_index,omitempty"`
	TotalAttachments        int    `json:"total_attachments,omitempty"`
	FinalDestinationSubject string `json:"final_destination_subject,omitempty"`
	OriginalCallbackTopic   string `json:"original_callback_topic,omitempty"`
	DocTypeHint             string `json:"doc_type_hint,omitempty"`
	Prompt                  string `json:"prompt,omitempty"`
	Instructions            string `json:"instructions,omitempty"`
	Model                   string `json:"model,omitempty"`
}

func (p OCRTaskPayload) GetFileURL() string {
	if p.DocumentURL != "" {
		return p.DocumentURL
	}
	if p.S3URL != "" && (strings.HasPrefix(p.S3URL, "http://") || strings.HasPrefix(p.S3URL, "https://")) {
		return p.S3URL
	}
	if p.ImageURL != "" {
		return p.ImageURL
	}
	if p.URL != "" {
		return p.URL
	}
	if p.AttachmentURL != "" {
		return p.AttachmentURL
	}
	if p.S3Key != "" && (strings.HasPrefix(p.S3Key, "http://") || strings.HasPrefix(p.S3Key, "https://")) {
		return p.S3Key
	}
	return ""
}

func (p OCRTaskPayload) GetFileName() string {
	if p.AttachmentName != "" {
		return p.AttachmentName
	}
	if p.FileName != "" {
		return p.FileName
	}
	return "document"
}

type OCRExtraction struct {
	DocType       string                       `json:"doc_type"`
	FileName      string                       `json:"file_name,omitempty"`
	Data          map[string]interface{}       `json:"data"`
	ColumnMapping *csvmapping.LLMColumnMapping `json:"column_mapping,omitempty"`
	Confidence    float64                      `json:"confidence"`
	RawText       string                       `json:"raw_text,omitempty"`
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
		a.Logger.Info("📡 [ocr_agent] received NATS message", "topic", msg.Subject, "data_length", len(msg.Data))

		meta, metaErr := msg.Metadata()
		if metaErr == nil && meta.NumDelivered > 3 {
			a.Logger.Error("Poison pill detected, terminating message", "subject", msg.Subject)
			msg.Term()
			return
		}

		if err := a.handleMessage(msg); err != nil {
			a.Logger.Error("Error processing OCR message", "error", err)
			var origEnv core.Envelope
			if envErr := json.Unmarshal(msg.Data, &origEnv); envErr == nil && origEnv.Performative != "" {
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

func (a *OCRAgent) handleMessage(msg *nats.Msg) error {
	var env core.Envelope
	if err := json.Unmarshal(msg.Data, &env); err == nil && env.Performative != "" {
		return a.handleEnvelope(msg, env)
	}

	// Direct raw NATS payload (e.g. from documents_store.go, pcm_worker.go, document_cdc_worker.go)
	var rawMap map[string]interface{}
	if err := json.Unmarshal(msg.Data, &rawMap); err != nil {
		a.Logger.Error("Failed to unmarshal raw NATS payload in ocr_agent", "error", err)
		return nil
	}

	return a.executeRawPayload(msg, rawMap)
}

func (a *OCRAgent) handleEnvelope(msg *nats.Msg, env core.Envelope) error {
	if env.Performative != core.CFP && env.Performative != core.ACCEPT_PROPOSAL && env.Performative != core.REQUEST {
		return nil
	}

	if env.Performative == core.CFP {
		a.Logger.Info("📨 Received CFP for OCR job", "sender", env.SenderDID, "cid", env.ConversationID)
		proposal := map[string]interface{}{"price": 1, "eta": "15s"}
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

	return a.executeTask(env)
}

func (a *OCRAgent) executeRawPayload(msg *nats.Msg, rawMap map[string]interface{}) error {
	payloadBytes, _ := json.Marshal(rawMap)
	var payload OCRTaskPayload
	_ = json.Unmarshal(payloadBytes, &payload)

	a.Logger.Info("🧠 Native Go OCR Agent processing raw task payload",
		"file_name", payload.GetFileName(),
		"file_url", payload.GetFileURL(),
		"document_id", payload.DocumentID,
	)

	taskDef := core.TaskDefinition{
		ID:      uuid.New().String(),
		Model:   payload.Model,
		Payload: payloadBytes,
	}

	extraction, err := a.runExtractionWithRedux(context.Background(), taskDef, payload)
	if err != nil {
		a.Logger.Error("Native Go OCR Agent extraction failed", "error", err)
		return err
	}

	// Build enriched response map
	responsePayload := make(map[string]interface{})
	for k, v := range rawMap {
		responsePayload[k] = v
	}
	responsePayload["status"] = "OCR_SUCCESS"
	responsePayload["ocr_extraction"] = extraction
	responsePayload["ocr_result"] = extraction

	destSubject := payload.FinalDestinationSubject
	if destSubject == "" {
		if cb, ok := rawMap["final_destination_subject"].(string); ok && cb != "" {
			destSubject = cb
		}
	}
	if destSubject == "" {
		destSubject = "worker.inbox.go.knowledge_ingest"
	}

	if a.Bus != nil {
		outBytes, _ := json.Marshal(responsePayload)
		a.Logger.Info("🚀 Publishing native OCR extraction to destination subject", "subject", destSubject)
		if pubErr := a.Bus.Publish(destSubject, outBytes); pubErr != nil {
			a.Logger.Error("Failed to publish OCR extraction result", "subject", destSubject, "error", pubErr)
			return pubErr
		}
	}

	return nil
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

	a.Logger.Info("🧠 Native Go OCR Agent executing task envelope",
		"task_id", task.ID,
		"file_name", payload.GetFileName(),
	)

	extraction, err := a.runExtractionWithRedux(context.Background(), task, payload)
	if err != nil {
		return err
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
	destSubject := payload.FinalDestinationSubject
	if destSubject == "" {
		destSubject = "worker.inbox.go.knowledge_ingest"
	}

	if a.Bus != nil {
		var rawMap map[string]interface{}
		_ = json.Unmarshal(task.Payload, &rawMap)
		if rawMap == nil {
			rawMap = make(map[string]interface{})
		}
		rawMap["status"] = "OCR_SUCCESS"
		rawMap["ocr_extraction"] = extraction
		rawMap["ocr_result"] = extraction

		forwardBytes, _ := json.Marshal(rawMap)
		a.Logger.Info("🚀 Forwarding OCR extraction to destination subject", "subject", destSubject)
		if pubErr := a.Bus.Publish(destSubject, forwardBytes); pubErr != nil {
			a.Logger.Error("Failed to publish OCR output to destination subject", "subject", destSubject, "error", pubErr)
			return pubErr
		}
	}

	return nil
}

func (a *OCRAgent) runExtractionWithRedux(ctx context.Context, task core.TaskDefinition, payload OCRTaskPayload) (*OCRExtraction, error) {
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
			patches = append(patches, fmt.Appendf(nil, `{"op": "test", "path": "/status", "value": %s}`, b))
		}

		ext, err := a.extractDocumentUsingLLM(ctx, task, payload)
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
			ctx,
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
		ext, err := a.extractDocumentUsingLLM(ctx, task, payload)
		if err != nil {
			return nil, err
		}
		extraction = ext
	}

	return extraction, nil
}

func (a *OCRAgent) extractDocumentUsingLLM(ctx context.Context, task core.TaskDefinition, payload OCRTaskPayload) (*OCRExtraction, error) {
	systemPrompt := `You are an expert OCR document parser. Analyze the document and extract structured JSON matching one of these exact types:

1. Bank Statement (doc_type: "bank_statement"):
   - Return only valid json, our application do not acept list/arrays in LLM output, so no lists anywhere in your ouput! That is absolute!
   CRITICAL FOR BANK STATEMENTS:
   - Do NOT drop debit/credit, withdrawal/deposit, or polarity sign indicators ('minus', 'brackets', 'none').
   - EXPECTED JSON BANK_STATEMENT OUTPUT:
	{
		"doc_type": "bank_statement",
		"confidence": 0.99,
		"data": {
			"bank_name": "ATTIJARIWAFA BANK",
			"account_number": "007810000012345678901234",
			"statement_date": "31/07/2026",
			"start_date": "01/07/2026",
			"end_date": "31/07/2026"
			"starting_balance": 150000,
			"ending_balance": 204385,
			"transactions": {
				"1": {
					"date": "02/07/2026",
					"description": "Loyer Juil. - Soc. Immobiliere Anfa",
					"amount": 12000,
					"type": "debit"
				},
				"2": {
					"date": "05/07/2026",
					"description": "Virement Client ABC Construction SARL",
					"amount": 25000,
					"type": "credit"
				},
				"n": {
					"date": "08/07/2026",
					"description": "Paiement CB Station Afriquia Oasis",
					"amount": 850,
					"type": "debit"
				}
			}
		},
		"column_mapping": {
			"date_col_idx": 0,
			"description_col_idx": 1,
			"amount_col_idx": 2,
			"is_split_amount": true,
			"debit_col_idx": 2,
			"credit_col_idx": 3,
			"vendor_col_idx": null,
			"customer_col_idx": null,
			"confidence_score": 0.99,
			"is_ambiguous": false,
			"ambiguity_reason": null,
			"polarity_sign": "none",
			"source_account": "ATTIJARIWAFA BANK"
		}
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

	fileURL := payload.GetFileURL()
	if fileURL == "" {
		return nil, fmt.Errorf("no file_url available for OCR extraction")
	}

	userPrompt := payload.Prompt
	if userPrompt == "" {
		userPrompt = payload.Instructions
	}
	if userPrompt == "" {
		userPrompt = fmt.Sprintf("Document Name: %s\nPlease perform visual OCR inspection on the attached document and extract structured JSON matching the system instructions.",
			payload.GetFileName(),
		)
	}

	if task.Model != "" {
		ctx = agent.WithModel(ctx, task.Model)
	}

	a.Logger.Info("Processing OCR document via ExecDocumentURL", "name", payload.GetFileName(), "url", fileURL)
	respText, err := a.rt.ExecDocumentURL(ctx, userPrompt, systemPrompt, fileURL, payload.GetFileName())
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

	if parsed.Confidence == 0 {
		parsed.Confidence = 0.95
	}

	extraction := &OCRExtraction{
		DocType:       parsed.DocType,
		FileName:      payload.GetFileName(),
		Data:          parsed.Data,
		ColumnMapping: parsed.ColumnMapping,
		Confidence:    parsed.Confidence,
		RawText:       respText,
	}
	return extraction, nil
}
