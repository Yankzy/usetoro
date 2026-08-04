package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/conversation"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/infra"
	csvmapping "github.com/Yankzy/usetoro/tap/agents/csv_mapping"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/identity"
	"github.com/Yankzy/usetoro/tap/workflows"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

type PcmWorker struct {
	db      *database.Queries
	logger  *slog.Logger
	cfg     *config.Config
	nc      *nats.Conn
	storage infra.S3Service
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		storageSvc, err := infra.NewS3Service(deps.Config)
		if err != nil {
			deps.Logger.Warn("PcmWorker: S3 storage not configured", "error", err)
		}

		return &PcmWorker{
			db:      deps.Store.Queries,
			logger:  deps.Logger.With("worker", "pcm"),
			cfg:     deps.Config,
			nc:      deps.Queue,
			storage: storageSvc,
		}, nil
	})
}

func (w *PcmWorker) Init(ctx context.Context) error {
	w.logger.Info("PcmWorker initialized")
	return nil
}

func (w *PcmWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		activityType = "workers.pcm"
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			subject = core.BuildWorkerInbox("pcm")
		}
	}

	group := workerCfg.Group
	if group == "" {
		group = "worker-inbox-pcm-group"
	}

	return []SubscriptionConfig{
		{
			Subject: subject,
			Group:   group,
			Options: []nats.SubOpt{
				nats.Durable(durableFromSubject(subject)),
				nats.DeliverAll(),
				nats.AckExplicit(),
			},
		},
	}
}

// PcmParsedDocument represents a structured document (e.g. bank statement or invoice) extracted by OCR.
type PcmParsedDocument struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"`
}

// PcmAttachment represents an email document attachment with presigned S3 URL.
type PcmAttachment struct {
	Name        string `json:"name,omitempty"`
	DocumentURL string `json:"document_url,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

// PcmOCRExtraction holds OCR parsing results returned from the Python OCR agent.
type PcmOCRExtraction struct {
	DocType       string                       `json:"doc_type"`
	FileName      string                       `json:"file_name,omitempty"`
	Data          map[string]interface{}       `json:"data"`
	ColumnMapping *csvmapping.LLMColumnMapping `json:"column_mapping,omitempty"`
	Confidence    float64                      `json:"confidence"`
}

// PcmInboundMessage represents the incoming payload delivered via NATS to PcmWorker.
type PcmInboundMessage struct {
	EntityID         string             `json:"entity_id,omitempty"`
	SessionID        string             `json:"session_id"`
	ConversationID   string             `json:"conversation_id,omitempty"`
	ExternalID       string             `json:"external_id"`
	FromHandle       string             `json:"from_handle"`
	ToHandle         string             `json:"to_handle"`
	ReplyTo          string             `json:"reply_to"`
	Subject          string             `json:"subject"`
	BodyText         string             `json:"body_text"`
	AgentAlias       string             `json:"agent_alias"`
	DocumentURL      string             `json:"document_url,omitempty"`
	Attachments      []PcmAttachment    `json:"attachments,omitempty"`
	AttachmentIndex  int                `json:"attachment_index"`
	TotalAttachments int                `json:"total_attachments"`
	OCRExtraction    PcmOCRExtraction   `json:"ocr_extraction,omitempty"`
	OCRExtractions   []PcmOCRExtraction `json:"ocr_extractions,omitempty"`
}

// Handle processes incoming PCM tasks delivered via NATS.
// It resolves user identity, handles delegation to Python OCR when extractions are pending,
// ingests bank statement transactions into database staging, and responds to the Orchestrator or downstream DAG.
func (w *PcmWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	reqEnv, inbound, err := w.parseInboundMessage(msg)
	if err != nil {
		return err
	}

	w.logger.Info("PcmWorker: processing pre-extracted OCR matching task", "from", inbound.FromHandle, "to", inbound.ToHandle)

	entityID, session, realmID, sessionIDStr, err := w.resolveEntityAndSession(ctx, &inbound)
	if err != nil {
		return err
	}
	_ = session

	w.createReconciliationTaskIfNeeded(ctx, &inbound, realmID, sessionIDStr)

	documents := w.extractOCRDocuments(&inbound)

	// If no OCR documents are extracted yet, batch all attachments and delegate to Python OCR agent
	if len(documents) == 0 {
		delegated, err := w.delegateToPythonOCR(&inbound, &reqEnv, sessionIDStr)
		if err != nil {
			return err
		}
		if delegated {
			return nil
		}
	}

	hasBankStatement := false
	for _, doc := range documents {
		if doc.Type == "bank_statement" {
			hasBankStatement = true
			break
		}
	}

	if !hasBankStatement {
		w.logger.Info("PcmWorker: documents received contain no bank statement (invoice/receipt evidence only); skipping bank reconciliation DAG execution", "doc_count", len(documents), "session_id", sessionIDStr)
		return nil
	}

	stagingSessionID, stagingSessionIDStr, fileName, err := w.ingestStagingSession(ctx, &inbound, documents, entityID, realmID, sessionIDStr)
	if err != nil {
		return err
	}

	return w.publishOutflowResult(&inbound, &reqEnv, documents, entityID, stagingSessionID, stagingSessionIDStr, realmID, fileName)
}

// parseInboundMessage unmarshals raw NATS message data into a TAP core.Envelope, PcmInboundMessage, or raw email payload.
func (w *PcmWorker) parseInboundMessage(msg *nats.Msg) (core.Envelope, PcmInboundMessage, error) {
	var reqEnv core.Envelope
	var inbound PcmInboundMessage

	if err := json.Unmarshal(msg.Data, &reqEnv); err == nil && reqEnv.Performative == core.REQUEST {
		_ = core.UnmarshalTaskPayload(reqEnv.Body, &inbound)
		return reqEnv, inbound, nil
	}

	if err := json.Unmarshal(msg.Data, &inbound); err == nil {
		return reqEnv, inbound, nil
	}

	var inboundEmail PostmarkInboundEmail
	if emailErr := json.Unmarshal(msg.Data, &inboundEmail); emailErr == nil {
		inbound.FromHandle = inboundEmail.From
		inbound.ToHandle = inboundEmail.OriginalRecipient
		if inbound.ToHandle == "" {
			inbound.ToHandle = inboundEmail.To
		}
		inbound.Subject = inboundEmail.Subject
		inbound.BodyText = inboundEmail.TextBody
		return reqEnv, inbound, nil
	}

	w.logger.Error("PcmWorker: failed to unmarshal payload", "error", string(msg.Data))
	return reqEnv, inbound, fmt.Errorf("failed to unmarshal payload")
}

// resolveEntityAndSession resolves user entity ID, agent realm, and finds or creates a conversation session in the database.
func (w *PcmWorker) resolveEntityAndSession(ctx context.Context, inbound *PcmInboundMessage) (pgtype.UUID, database.ToroCoreConversationSession, string, string, error) {
	var entityID pgtype.UUID
	if w.db != nil {
		if id, err := w.db.GetEntityIDByEmail(ctx, inbound.FromHandle); err == nil && id.Valid {
			entityID = id
		}
	}
	if !entityID.Valid && inbound.EntityID != "" {
		_ = entityID.Scan(inbound.EntityID)
	}
	if !entityID.Valid {
		w.logger.Warn("PcmWorker: ignoring email from unknown user", "from", inbound.FromHandle, "entity_id", inbound.EntityID)
		return entityID, database.ToroCoreConversationSession{}, "", "", fmt.Errorf("unknown user")
	}

	agentAlias, _ := ParseAgentEmail(inbound.ToHandle)
	realmID := "default_realm"
	if agentAlias != "" {
		realmID = agentAlias
	}

	var session database.ToroCoreConversationSession
	if inbound.SessionID != "" && w.db != nil {
		var sessionUUID pgtype.UUID
		if scanErr := sessionUUID.Scan(inbound.SessionID); scanErr == nil {
			if sess, fetchErr := w.db.GetConversationSession(ctx, sessionUUID); fetchErr == nil {
				session = sess
			}
		}
	}

	if !session.ID.Valid && w.db != nil {
		sessionManager := conversation.NewSessionManager(w.db, w.logger)
		var errSess error
		session, _, errSess = sessionManager.FindOrCreateSession(ctx, conversation.FindOrCreateParams{
			EntityID:          entityID,
			ParticipantHandle: inbound.FromHandle,
			ToroHandle:        inbound.ToHandle,
			Source:            "email",
			Subject:           inbound.Subject,
		})
		if errSess != nil {
			w.logger.Error("PcmWorker: failed to find or create conversation session", "error", errSess)
			return entityID, session, realmID, "", fmt.Errorf("session resolution failed: %w", errSess)
		}
	}

	sessionIDStr := uuidFromPG(session.ID)
	return entityID, session, realmID, sessionIDStr, nil
}

// createReconciliationTaskIfNeeded inserts a shadow ERP reconciliation task entry when processing an email directed to a rap_ agent alias.
func (w *PcmWorker) createReconciliationTaskIfNeeded(ctx context.Context, inbound *PcmInboundMessage, realmID string, sessionIDStr string) {
	agentAlias, _ := ParseAgentEmail(inbound.ToHandle)
	isRapEmail := strings.Contains(strings.ToLower(inbound.ToHandle), "rap_") ||
		strings.Contains(strings.ToLower(agentAlias), "rap_")

	if isRapEmail && w.db != nil {
		periodLabel := time.Now().Format("2006-01")
		_, err := w.db.CreateReconciliationTask(ctx, database.CreateReconciliationTaskParams{
			RealmID:       realmID,
			PeriodLabel:   periodLabel,
			Status:        "PENDING_MATCH",
			EmailThreadID: pgtype.Text{String: sessionIDStr, Valid: true},
		})
		if err != nil {
			w.logger.Warn("PcmWorker: failed to insert reconciliation task", "error", err, "session_id", sessionIDStr)
		} else {
			w.logger.Info("PcmWorker: created reconciliation task for rap_ email", "session_id", sessionIDStr, "realm_id", realmID)
		}
	}
}

// extractOCRDocuments collects extracted document structures from OCRExtractions or single OCRExtraction fields.
func (w *PcmWorker) extractOCRDocuments(inbound *PcmInboundMessage) []PcmParsedDocument {
	var documents []PcmParsedDocument

	for _, ext := range inbound.OCRExtractions {
		if ext.DocType != "" && ext.Data != nil {
			documents = append(documents, PcmParsedDocument{
				Type: ext.DocType,
				Data: ext.Data,
			})
		}
	}

	if len(documents) == 0 && inbound.OCRExtraction.DocType != "" && inbound.OCRExtraction.Data != nil {
		documents = append(documents, PcmParsedDocument{
			Type: inbound.OCRExtraction.DocType,
			Data: inbound.OCRExtraction.Data,
		})
	}

	return documents
}

// delegateToPythonOCR gathers email attachment URLs and delegates them in a single batch NATS request to the Python OCR agent.
// Returns true if a delegation request was published.
func (w *PcmWorker) delegateToPythonOCR(inbound *PcmInboundMessage, reqEnv *core.Envelope, sessionIDStr string) (bool, error) {
	var ocrAttachments []map[string]interface{}
	if len(inbound.Attachments) > 0 {
		for _, att := range inbound.Attachments {
			if att.DocumentURL != "" {
				ocrAttachments = append(ocrAttachments, map[string]interface{}{
					"name":         att.Name,
					"document_url": att.DocumentURL,
					"content_type": att.ContentType,
				})
			}
		}
	} else if inbound.DocumentURL != "" {
		ocrAttachments = append(ocrAttachments, map[string]interface{}{
			"document_url": inbound.DocumentURL,
		})
	}

	if len(ocrAttachments) > 0 {
		firstDocURL := ""
		if u, ok := ocrAttachments[0]["document_url"].(string); ok {
			firstDocURL = u
		}

		ocrTaskPayload := map[string]interface{}{
			"session_id":                sessionIDStr,
			"conversation_id":           reqEnv.ConversationID,
			"external_id":               inbound.ExternalID,
			"from_handle":               inbound.FromHandle,
			"to_handle":                 inbound.ToHandle,
			"reply_to":                  inbound.ReplyTo,
			"subject":                   inbound.Subject,
			"body_text":                 inbound.BodyText,
			"agent_alias":               inbound.AgentAlias,
			"document_url":              firstDocURL,
			"attachments":               ocrAttachments,
			"total_attachments":         len(ocrAttachments),
			"final_destination_subject": "worker.inbox.pcm",
		}
		ocrBytes, _ := json.Marshal(ocrTaskPayload)
		w.logger.Info("PcmWorker: delegating attachments batch to Python OCR agent", "attachments_count", len(ocrAttachments))
		if w.nc != nil {
			if err := w.nc.Publish("worker.inbox.python.ocr", ocrBytes); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

// ingestStagingSession creates a database cleanup session and inserts bank statement transaction rows into fignode.staging_transactions.
func (w *PcmWorker) ingestStagingSession(ctx context.Context, inbound *PcmInboundMessage, documents []PcmParsedDocument, entityID pgtype.UUID, realmID string, sessionIDStr string) (pgtype.UUID, string, string, error) {
	outflowIs := "NEGATIVE"

	totalRowCount := 0
	for _, doc := range documents {
		if doc.Type == "bank_statement" {
			if txList, ok := doc.Data["transactions"].([]interface{}); ok {
				totalRowCount += len(txList)
			}
		}
	}

	var pgUserID pgtype.UUID
	if inbound.FromHandle != "" && w.db != nil {
		if user, errUser := w.db.GetUserByEmail(ctx, inbound.FromHandle); errUser == nil && user.ID.Valid {
			pgUserID = user.ID
		}
	}

	fileName := inbound.Subject
	if fileName == "" {
		fileName = "Email_Bank_Statement.pdf"
	}

	if w.db == nil {
		return pgtype.UUID{}, "", fileName, nil
	}

	cleanupSession, errCleanup := w.db.CreateCleanupSession(ctx, database.CreateCleanupSessionParams{
		CreatedBy: pgUserID,
		FileName:  pgtype.Text{String: fileName, Valid: true},
		RowCount:  int32(totalRowCount),
		RealmID:   pgtype.Text{String: realmID, Valid: true},
		OutflowIs: outflowIs,
	})
	if errCleanup != nil || !cleanupSession.ID.Valid {
		w.logger.Error("PcmWorker: failed to create cleanup session", "error", errCleanup)
		return pgtype.UUID{}, "", fileName, fmt.Errorf("failed to create cleanup session: %w", errCleanup)
	}
	stagingSessionID := cleanupSession.ID
	stagingSessionIDStr := uuid.UUID(stagingSessionID.Bytes).String()

	if inbound.OCRExtraction.ColumnMapping != nil {
		columnMapping := inbound.OCRExtraction.ColumnMapping
		if columnMapping.PolaritySign == "none" && columnMapping.IsAmbiguous {
			w.logger.Info("PcmWorker: statement marked structurally ambiguous by OCR agent", "reason", columnMapping.AmbiguityReason)
		}
	}

	for _, doc := range documents {
		if doc.Type == "bank_statement" {
			if txList, ok := doc.Data["transactions"].([]interface{}); ok {
				for idx, txItem := range txList {
					if txMap, ok := txItem.(map[string]interface{}); ok {
						desc, _ := txMap["description"].(string)
						txType, _ := txMap["type"].(string)
						
						amtVal := ""
						if amtFloat, ok := txMap["amount"].(float64); ok {
							if amtFloat < 0 {
								amtFloat = -amtFloat
							}
							if txType == "debit" {
								amtVal = fmt.Sprintf("-%.2f", amtFloat)
							} else {
								amtVal = fmt.Sprintf("%.2f", amtFloat)
							}
						} else if amtStr, ok := txMap["amount"].(string); ok {
							amtStr = strings.TrimPrefix(amtStr, "-")
							if txType == "debit" {
								amtVal = "-" + amtStr
							} else {
								amtVal = amtStr
							}
						}
						dt, _ := txMap["date"].(string)
						rowIdx := int32(idx)

						_, errIns := w.db.InsertCleanupRow(ctx, database.InsertCleanupRowParams{
							SessionID:      stagingSessionID,
							RowIndex:       pgtype.Int4{Int32: rowIdx, Valid: true},
							SourceType:     "BankStatement",
							RawDescription: pgtype.Text{String: desc, Valid: desc != ""},
							RawAmount:      amtVal,
							RawDate:        pgtype.Text{String: dt, Valid: dt != ""},
							Status:         pgtype.Text{String: "PENDING", Valid: true},
						})
						if errIns != nil {
							w.logger.Warn("PcmWorker: failed to insert bank statement staging transaction row", "error", errIns, "session_id", sessionIDStr)
						}
					}
				}
			}
		}
	}

	return stagingSessionID, stagingSessionIDStr, fileName, nil
}

// publishOutflowResult publishes the completed execution proof to the TAP Orchestrator if executing inside a workflow step,
// or publishes a signed TAP event to JetStream / falls back to publishing an envelope to ase_bridge.
func (w *PcmWorker) publishOutflowResult(inbound *PcmInboundMessage, reqEnv *core.Envelope, documents []PcmParsedDocument, entityID pgtype.UUID, stagingSessionID pgtype.UUID, stagingSessionIDStr string, realmID string, fileName string) error {
	entityIDStr := uuid.UUID(entityID.Bytes).String()
	cid := reqEnv.ConversationID
	if cid == "" {
		cid = inbound.ConversationID
	}

	if cid != "" && w.nc != nil {
		proofData := map[string]interface{}{
			"session_id":  stagingSessionIDStr,
			"entity_id":   entityIDStr,
			"realm_id":    realmID,
			"from_handle": inbound.FromHandle,
			"to_handle":   inbound.ToHandle,
			"documents":   len(documents),
		}
		proofDataBytes, _ := json.Marshal(proofData)
		proof := core.Proof{
			Type:      core.ProofAPI,
			Timestamp: time.Now().Unix(),
			Data:      json.RawMessage(proofDataBytes),
		}
		replyEnv, envErr := core.NewEnvelope(
			uuid.New().String(),
			"worker.pcm",
			workflows.OrchestratorInbox,
			cid,
			core.INFORM,
			proof,
		)
		if envErr == nil {
			replyBytes, _ := json.Marshal(replyEnv)
			_ = w.nc.Publish(workflows.OrchestratorInbox, replyBytes)
			w.logger.Info("PcmWorker: sent completion proof to TAP Orchestrator", "cid", cid)
			return nil
		}
	}

	payload := map[string]interface{}{
		"upload_id":  stagingSessionIDStr,
		"session_id": stagingSessionIDStr,
		"filename":   fileName,
		"entity_id":  entityIDStr,
		"domain":     "accounting",
		"task_type":  "pcm_bookkeeping",
		"realm_id":   realmID,
		"outflow_is": "NEGATIVE",
		"documents":  documents,
	}
	payloadBytes, _ := json.Marshal(payload)

	taskDef := core.TaskDefinition{
		ID:         stagingSessionIDStr,
		Domain:     "accounting.pcm_bookkeeping",
		Complexity: 0,
		Payload:    payloadBytes,
	}

	kp, _ := identity.KeyPairFromSeed("gateway")
	gateDID := identity.CreateDID(kp.Public)

	informEnv, errEnv := core.NewEnvelope(
		uuid.New().String(),
		gateDID,
		"",
		uuid.New().String(),
		core.REQUEST,
		taskDef,
	)
	if errEnv == nil && w.nc != nil {
		informEnv.Signature = kp.Sign(informEnv.Body)
		informBytes, _ := json.Marshal(informEnv)

		topic := core.BuildEventSubject("accounting", core.ComplexityEntry, "pcm_bookkeeping")
		if pubErr := w.nc.Publish(topic, informBytes); pubErr == nil {
			w.logger.Info("PcmWorker: published signed TAP event to JetStream", "topic", topic, "session_id", stagingSessionIDStr)
			return nil
		}
	}

	ocrPayload := map[string]interface{}{
		"entity_id":  fmt.Sprintf("%x-%x-%x-%x-%x", entityID.Bytes[0:4], entityID.Bytes[4:6], entityID.Bytes[6:8], entityID.Bytes[8:10], entityID.Bytes[10:16]),
		"realm_id":   realmID,
		"session_id": stagingSessionIDStr,
		"documents":  documents,
	}

	env := core.Envelope{
		ID:           uuid.New().String(),
		Timestamp:    time.Now(),
		SenderDID:    "did:toro:worker:pcm",
		Performative: core.REQUEST,
	}

	taskConfig := map[string]interface{}{
		"dag_name":    "pcm_bank_reconciliation",
		"domain_tool": "bookkeeping",
	}

	bodyData := map[string]interface{}{
		"config": taskConfig,
		"input":  ocrPayload,
	}
	env.Body, _ = json.Marshal(bodyData)
	envBytes, _ := json.Marshal(env)

	if w.nc != nil {
		if err := w.nc.Publish(core.BuildWorkerInbox("ase_bridge"), envBytes); err != nil {
			w.logger.Error("PcmWorker: failed to publish fallback to ase_bridge", "error", err)
			return err
		}
	}

	w.logger.Info("PcmWorker: successfully published fallback payload to DAG", "realm_id", realmID)
	return nil
}
