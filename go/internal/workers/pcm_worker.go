package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/conversation"
	"github.com/Yankzy/usetoro/internal/database"
	csvmapping "github.com/Yankzy/usetoro/tap/agents/csv_mapping"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/Yankzy/usetoro/tap/pkg/identity"
	"github.com/Yankzy/usetoro/tap/workflows"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
)

type PcmWorker struct {
	db     *database.Queries
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &PcmWorker{
			db:     deps.Store.Queries,
			logger: deps.Logger.With("worker", "pcm"),
			cfg:    deps.Config,
			nc:     deps.Queue,
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

// PcmReadyDocument is the document contract emitted by document_readiness.
//
// PCM does not query ToroDB for OCR results anymore. document_readiness owns
// readiness verification and retrieval of RawOCRJSON.
//
// Expected shape:
//
//	{
//	  "document_id": "...",
//	  "ocr_status": "EMBEDDINGS_SUCCESS",
//	  "raw_ocr_json": { ... }
//	}
type PcmReadyDocument struct {
	DocumentID string          `json:"document_id"`
	OCRStatus  string          `json:"ocr_status"`
	RawOCRJSON json.RawMessage `json:"raw_ocr_json"`
}

// PcmParsedDocument is PCM's normalized representation of an OCR document.
//
// RawOCRJSON is converted into this representation before staging ingestion.
// Document metadata is retained so later PCM processing can reason about the
// source document without another database lookup.
type PcmParsedDocument struct {
	DocumentID    string                       `json:"document_id,omitempty"`
	OCRStatus     string                       `json:"ocr_status,omitempty"`
	Type          string                       `json:"type"`
	FileName      string                       `json:"file_name,omitempty"`
	Data          map[string]interface{}       `json:"data"`
	ColumnMapping *csvmapping.LLMColumnMapping `json:"column_mapping,omitempty"`
	Confidence    float64                      `json:"confidence,omitempty"`
}

// PcmAttachment represents source attachment metadata.
//
// document_url remains for backwards compatibility with older direct PCM
// invocation paths. Workflow execution normally uses DocumentID/S3Key.
type PcmAttachment struct {
	DocumentID  string `json:"document_id,omitempty"`
	Name        string `json:"name,omitempty"`
	DocumentURL string `json:"document_url,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	S3Key       string `json:"s3_key,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
}

// PcmOCRExtraction is the canonical OCR extraction structure.
//
// These fields correspond directly to the structure stored inside
// documents[].raw_ocr_json.
type PcmOCRExtraction struct {
	DocType       string                       `json:"doc_type"`
	FileName      string                       `json:"file_name,omitempty"`
	Data          map[string]interface{}       `json:"data"`
	ColumnMapping *csvmapping.LLMColumnMapping `json:"column_mapping,omitempty"`
	Confidence    float64                      `json:"confidence"`
}

// PcmInboundMessage is the workflow contract consumed by PcmWorker.
//
// The primary OCR input is Documents. OCRExtraction/OCRExtractions remain only
// as migration compatibility for callers that have not yet adopted
// document_readiness.
type PcmInboundMessage struct {
	EntityID       string `json:"entity_id,omitempty"`
	SessionID      string `json:"session_id"`
	ConversationID string `json:"conversation_id,omitempty"`
	ExternalID     string `json:"external_id"`
	FromHandle     string `json:"from_handle"`
	ToHandle       string `json:"to_handle"`
	ReplyTo        string `json:"reply_to"`
	Subject        string `json:"subject"`
	BodyText       string `json:"body_text"`
	AgentAlias     string `json:"agent_alias"`

	DocumentIDs      []string           `json:"document_ids,omitempty"`
	Attachments      []PcmAttachment    `json:"attachments,omitempty"`
	DocumentVerified bool               `json:"document_verified,omitempty"`
	Documents        []PcmReadyDocument `json:"documents,omitempty"`

	// Legacy inputs.
	DocumentURL      string             `json:"document_url,omitempty"`
	AttachmentIndex  int                `json:"attachment_index,omitempty"`
	TotalAttachments int                `json:"total_attachments,omitempty"`
	OCRExtraction    PcmOCRExtraction   `json:"ocr_extraction,omitempty"`
	OCRExtractions   []PcmOCRExtraction `json:"ocr_extractions,omitempty"`
}

// Handle processes an incoming PCM workflow task.
//
// The normal workflow is:
//
//	document_readiness
//	    |
//	    | documents[].raw_ocr_json
//	    v
//	pcm_worker
//	    |
//	    | normalized transactions
//	    v
//	staging_transactions
//
// PcmWorker deliberately does not perform document readiness checks or query
// the document store. Those responsibilities belong to document_readiness.
func (w *PcmWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	reqEnv, inbound, err := w.parseInboundMessage(msg)
	if err != nil {
		return err
	}

	w.logger.Info(
		"PcmWorker: processing pre-extracted OCR task",
		"from", inbound.FromHandle,
		"to", inbound.ToHandle,
		"document_count", len(inbound.Documents),
		"document_verified", inbound.DocumentVerified,
	)

	entityID, session, realmID, conversationSessionID, err := w.resolveEntityAndSession(
		ctx,
		&inbound,
	)
	if err != nil {
		return err
	}

	_ = session

	documents, err := w.extractOCRDocuments(&inbound)
	if err != nil {
		w.logger.Error(
			"PcmWorker: failed to decode OCR documents",
			"error", err,
			"session_id", conversationSessionID,
		)

		return fmt.Errorf("pcm: decode OCR documents: %w", err)
	}

	hasBankStatement := false

	for _, doc := range documents {
		if isBankStatementType(doc.Type) {
			hasBankStatement = true
			break
		}
	}

	if !hasBankStatement {
		w.logger.Info(
			"PcmWorker: documents contain no bank statement; skipping bank reconciliation ingestion",
			"doc_count", len(documents),
			"session_id", conversationSessionID,
		)

		if reqEnv.Performative == core.REQUEST {
			if err := w.sendSkipProofToOrchestrator(
				&inbound,
				&reqEnv,
				conversationSessionID,
				realmID,
			); err != nil {
				return err
			}
		}

		return nil
	}

	stagingSessionID,
		stagingSessionIDStr,
		fileName,
		err := w.ingestStagingSession(
		ctx,
		&inbound,
		documents,
		entityID,
		realmID,
		conversationSessionID,
	)
	if err != nil {
		return err
	}

	return w.publishOutflowResult(
		&inbound,
		&reqEnv,
		documents,
		entityID,
		stagingSessionID,
		stagingSessionIDStr,
		conversationSessionID,
		realmID,
		fileName,
	)
}

// parseInboundMessage supports:
//
//  1. Orchestrator REQUEST envelopes.
//  2. Direct PCM payloads.
//  3. Legacy Postmark payloads.
//
// Unknown JSON objects are not silently accepted as empty PcmInboundMessage
// values.
func (w *PcmWorker) parseInboundMessage(
	msg *nats.Msg,
) (core.Envelope, PcmInboundMessage, error) {
	var reqEnv core.Envelope
	var inbound PcmInboundMessage

	if err := json.Unmarshal(msg.Data, &reqEnv); err == nil &&
		reqEnv.Performative == core.REQUEST {

		if err := core.UnmarshalTaskPayload(reqEnv.Body, &inbound); err != nil {
			w.logger.Error(
				"PcmWorker: failed to unmarshal orchestrator task payload",
				"error", err,
				"cid", reqEnv.ConversationID,
			)

			return reqEnv, inbound, fmt.Errorf(
				"unmarshal orchestrator PCM payload: %w",
				err,
			)
		}

		return reqEnv, inbound, nil
	}

	if err := json.Unmarshal(msg.Data, &inbound); err == nil &&
		isRecognizedPCMInbound(inbound) {

		return reqEnv, inbound, nil
	}

	var inboundEmail PostmarkInboundEmail

	if err := json.Unmarshal(msg.Data, &inboundEmail); err == nil &&
		inboundEmail.From != "" {

		inbound.FromHandle = inboundEmail.From
		inbound.ToHandle = inboundEmail.OriginalRecipient

		if inbound.ToHandle == "" {
			inbound.ToHandle = inboundEmail.To
		}

		inbound.Subject = inboundEmail.Subject
		inbound.BodyText = inboundEmail.TextBody

		return reqEnv, inbound, nil
	}

	w.logger.Error(
		"PcmWorker: failed to recognize inbound payload",
		"payload_size", len(msg.Data),
	)

	return reqEnv, inbound, fmt.Errorf(
		"pcm: unsupported or malformed inbound payload",
	)
}

func isRecognizedPCMInbound(inbound PcmInboundMessage) bool {
	return inbound.SessionID != "" ||
		inbound.EntityID != "" ||
		inbound.FromHandle != "" ||
		inbound.ToHandle != "" ||
		len(inbound.DocumentIDs) > 0 ||
		len(inbound.Documents) > 0 ||
		len(inbound.OCRExtractions) > 0 ||
		inbound.OCRExtraction.DocType != ""
}

// resolveEntityAndSession resolves entity, accounting realm and conversation
// session.
//
// EntityID from the workflow payload is used as a fallback when the sender
// email cannot be resolved directly.
func (w *PcmWorker) resolveEntityAndSession(
	ctx context.Context,
	inbound *PcmInboundMessage,
) (
	pgtype.UUID,
	database.ToroCoreConversationSession,
	string,
	string,
	error,
) {
	var entityID pgtype.UUID

	if w.db != nil && inbound.FromHandle != "" {
		if id, err := w.db.GetEntityIDByEmail(
			ctx,
			inbound.FromHandle,
		); err == nil && id.Valid {
			entityID = id
		}
	}

	if !entityID.Valid && inbound.EntityID != "" {
		if err := entityID.Scan(inbound.EntityID); err != nil {
			w.logger.Warn(
				"PcmWorker: invalid entity_id in workflow payload",
				"entity_id", inbound.EntityID,
				"error", err,
			)
		}
	}

	if !entityID.Valid {
		w.logger.Warn(
			"PcmWorker: unable to resolve entity",
			"from", inbound.FromHandle,
			"entity_id", inbound.EntityID,
		)

		return entityID,
			database.ToroCoreConversationSession{},
			"",
			"",
			fmt.Errorf("pcm: unknown entity")
	}

	agentAlias := inbound.AgentAlias

	if agentAlias == "" {
		agentAlias, _ = ParseAgentEmail(inbound.ToHandle)
	}

	realmID := w.resolveRealmIDFromAlias(ctx, agentAlias)

	var session database.ToroCoreConversationSession

	if inbound.SessionID != "" && w.db != nil {
		var sessionUUID pgtype.UUID

		if err := sessionUUID.Scan(inbound.SessionID); err == nil {
			if existing, fetchErr := w.db.GetConversationSession(
				ctx,
				sessionUUID,
			); fetchErr == nil {
				session = existing
			}
		}
	}

	if !session.ID.Valid && w.db != nil {
		sessionManager := conversation.NewSessionManager(
			w.db,
			w.logger,
		)

		resolved,
			_,
			err := sessionManager.FindOrCreateSession(
			ctx,
			conversation.FindOrCreateParams{
				EntityID:          entityID,
				ParticipantHandle: inbound.FromHandle,
				ToroHandle:        inbound.ToHandle,
				Source:            "email",
				Subject:           inbound.Subject,
			},
		)
		if err != nil {
			w.logger.Error(
				"PcmWorker: failed to find or create conversation session",
				"error", err,
			)

			return entityID,
				session,
				realmID,
				"",
				fmt.Errorf(
					"pcm: session resolution failed: %w",
					err,
				)
		}

		session = resolved
	}

	sessionIDStr := uuidFromPG(session.ID)

	return entityID, session, realmID, sessionIDStr, nil
}

// resolveRealmIDFromAlias resolves realm_id using client_dossiers.
//
// It first tries the complete alias and then the legacy "rap_" stripped dossier
// code.
func (w *PcmWorker) resolveRealmIDFromAlias(
	ctx context.Context,
	agentAlias string,
) string {
	if agentAlias == "" {
		w.logger.Warn(
			"PcmWorker: agent alias unavailable while resolving realm",
		)

		return ""
	}

	if w.db != nil {
		if dossier, err := w.db.GetClientDossierByRealmOrCode(
			ctx,
			agentAlias,
		); err == nil {

			w.logger.Info(
				"PcmWorker: resolved realm_id from client_dossiers",
				"alias", agentAlias,
				"realm_id", dossier.RealmID,
			)

			return dossier.RealmID
		}

		dossierCode := strings.TrimPrefix(agentAlias, "rap_")

		if dossierCode != agentAlias {
			if dossier, err := w.db.GetClientDossierByRealmOrCode(
				ctx,
				dossierCode,
			); err == nil {

				w.logger.Info(
					"PcmWorker: resolved realm_id from dossier_code",
					"dossier_code", dossierCode,
					"realm_id", dossier.RealmID,
				)

				return dossier.RealmID
			}
		}

		w.logger.Warn(
			"PcmWorker: no client_dossier found for alias; using alias as realm_id",
			"alias", agentAlias,
		)
	}

	return agentAlias
}

// extractOCRDocuments converts document_readiness output into PCM documents.
//
// documents[].raw_ocr_json is authoritative whenever Documents is non-empty.
// Legacy OCRExtraction fields are considered only when Documents is absent.
//
// Critically, this function performs no database queries.
func (w *PcmWorker) extractOCRDocuments(
	inbound *PcmInboundMessage,
) ([]PcmParsedDocument, error) {
	if len(inbound.Documents) > 0 {
		documents := make(
			[]PcmParsedDocument,
			0,
			len(inbound.Documents),
		)

		for index, readyDocument := range inbound.Documents {
			document, err := parseReadyDocument(readyDocument)
			if err != nil {
				return nil, fmt.Errorf(
					"documents[%d]: %w",
					index,
					err,
				)
			}

			documents = append(documents, document)
		}

		return documents, nil
	}

	// Migration compatibility for callers that still provide the historical
	// OCRExtractions structure directly.
	documents := make(
		[]PcmParsedDocument,
		0,
		len(inbound.OCRExtractions)+1,
	)

	for index, extraction := range inbound.OCRExtractions {
		if extraction.DocType == "" {
			w.logger.Warn(
				"PcmWorker: ignoring legacy OCR extraction with no document type",
				"index", index,
			)

			continue
		}

		if extraction.Data == nil {
			w.logger.Warn(
				"PcmWorker: ignoring legacy OCR extraction with no data",
				"index", index,
				"doc_type", extraction.DocType,
			)

			continue
		}

		documents = append(
			documents,
			PcmParsedDocument{
				Type:          extraction.DocType,
				FileName:      extraction.FileName,
				Data:          extraction.Data,
				ColumnMapping: extraction.ColumnMapping,
				Confidence:    extraction.Confidence,
			},
		)
	}

	if len(documents) == 0 &&
		inbound.OCRExtraction.DocType != "" &&
		inbound.OCRExtraction.Data != nil {

		extraction := inbound.OCRExtraction

		documents = append(
			documents,
			PcmParsedDocument{
				Type:          extraction.DocType,
				FileName:      extraction.FileName,
				Data:          extraction.Data,
				ColumnMapping: extraction.ColumnMapping,
				Confidence:    extraction.Confidence,
			},
		)
	}

	return documents, nil
}

// parseReadyDocument parses one document_readiness result.
//
// RawOCRJSON is expected to contain the OCR agent's extraction document:
//
//	{
//	  "doc_type": "bank_statement",
//	  "data": { ... },
//	  "confidence": 0.99,
//	  "column_mapping": { ... }
//	}
func parseReadyDocument(
	readyDocument PcmReadyDocument,
) (PcmParsedDocument, error) {
	if readyDocument.DocumentID == "" {
		return PcmParsedDocument{}, fmt.Errorf(
			"document_id is required",
		)
	}

	if len(readyDocument.RawOCRJSON) == 0 {
		return PcmParsedDocument{}, fmt.Errorf(
			"document %q has empty raw_ocr_json",
			readyDocument.DocumentID,
		)
	}

	if !json.Valid(readyDocument.RawOCRJSON) {
		return PcmParsedDocument{}, fmt.Errorf(
			"document %q has invalid raw_ocr_json",
			readyDocument.DocumentID,
		)
	}

	var extraction PcmOCRExtraction

	if err := json.Unmarshal(
		readyDocument.RawOCRJSON,
		&extraction,
	); err != nil {
		return PcmParsedDocument{}, fmt.Errorf(
			"decode raw_ocr_json for document %q: %w",
			readyDocument.DocumentID,
			err,
		)
	}

	// Defensive compatibility for older OCR producers that used "type" or
	// "document_type" instead of "doc_type".
	if extraction.DocType == "" || extraction.Data == nil {
		var raw map[string]interface{}

		if err := json.Unmarshal(
			readyDocument.RawOCRJSON,
			&raw,
		); err != nil {
			return PcmParsedDocument{}, fmt.Errorf(
				"decode generic OCR payload for document %q: %w",
				readyDocument.DocumentID,
				err,
			)
		}

		if extraction.DocType == "" {
			if value, ok := raw["document_type"].(string); ok {
				extraction.DocType = value
			}

			if extraction.DocType == "" {
				if value, ok := raw["type"].(string); ok {
					extraction.DocType = value
				}
			}
		}

		if extraction.Data == nil {
			if value, ok := raw["data"].(map[string]interface{}); ok {
				extraction.Data = value
			} else {
				// Historical OCR payloads occasionally emitted the extracted
				// fields directly at the root.
				extraction.Data = raw
			}
		}
	}

	if strings.TrimSpace(extraction.DocType) == "" {
		return PcmParsedDocument{}, fmt.Errorf(
			"document %q raw_ocr_json is missing doc_type",
			readyDocument.DocumentID,
		)
	}

	if extraction.Data == nil {
		return PcmParsedDocument{}, fmt.Errorf(
			"document %q raw_ocr_json is missing data",
			readyDocument.DocumentID,
		)
	}

	return PcmParsedDocument{
		DocumentID:    readyDocument.DocumentID,
		OCRStatus:     readyDocument.OCRStatus,
		Type:          extraction.DocType,
		FileName:      extraction.FileName,
		Data:          extraction.Data,
		ColumnMapping: extraction.ColumnMapping,
		Confidence:    extraction.Confidence,
	}, nil
}

func isBankStatementType(documentType string) bool {
	normalized := strings.ToLower(
		strings.TrimSpace(documentType),
	)

	switch normalized {
	case "bank_statement", "bankstatement":
		return true
	default:
		return false
	}
}

// extractTransactionMaps normalizes the OCR transaction representation.
//
// Supported:
//
//	"transactions": [
//	  {...},
//	  {...}
//	]
//
// and:
//
//	"transactions": {
//	  "1": {...},
//	  "2": {...}
//	}
//
// The keyed-object form is sorted numerically to preserve statement order.
func extractTransactionMaps(
	raw interface{},
) []map[string]interface{} {
	switch transactions := raw.(type) {
	case []interface{}:
		result := make(
			[]map[string]interface{},
			0,
			len(transactions),
		)

		for _, item := range transactions {
			if transaction, ok := item.(map[string]interface{}); ok {
				result = append(result, transaction)
			}
		}

		return result

	case map[string]interface{}:
		keys := make([]string, 0, len(transactions))

		for key := range transactions {
			keys = append(keys, key)
		}

		sort.SliceStable(keys, func(i, j int) bool {
			leftNumber, leftErr := strconv.Atoi(keys[i])
			rightNumber, rightErr := strconv.Atoi(keys[j])

			switch {
			case leftErr == nil && rightErr == nil:
				return leftNumber < rightNumber
			case leftErr == nil:
				return true
			case rightErr == nil:
				return false
			default:
				return keys[i] < keys[j]
			}
		})

		result := make(
			[]map[string]interface{},
			0,
			len(keys),
		)

		for _, key := range keys {
			transaction, ok := transactions[key].(map[string]interface{})
			if !ok {
				continue
			}

			result = append(result, transaction)
		}

		return result

	default:
		return nil
	}
}

// ingestStagingSession creates the PCM cleanup/staging session and inserts all
// bank statement transactions.
func (w *PcmWorker) ingestStagingSession(
	ctx context.Context,
	inbound *PcmInboundMessage,
	documents []PcmParsedDocument,
	entityID pgtype.UUID,
	realmID string,
	conversationSessionID string,
) (
	pgtype.UUID,
	string,
	string,
	error,
) {
	const outflowIs = "NEGATIVE"

	totalRowCount := 0

	for _, document := range documents {
		if !isBankStatementType(document.Type) {
			continue
		}

		totalRowCount += len(
			extractTransactionMaps(
				document.Data["transactions"],
			),
		)
	}

	if totalRowCount == 0 {
		return pgtype.UUID{},
			"",
			"",
			fmt.Errorf(
				"pcm: bank statement contains no usable transactions",
			)
	}

	var pgUserID pgtype.UUID

	if inbound.FromHandle != "" && w.db != nil {
		if user, err := w.db.GetUserByEmail(
			ctx,
			inbound.FromHandle,
		); err == nil && user.ID.Valid {
			pgUserID = user.ID
		}
	}

	fileName := resolvePCMFileName(
		inbound,
		documents,
	)

	if w.db == nil {
		return pgtype.UUID{},
			"",
			fileName,
			fmt.Errorf("pcm: database unavailable")
	}

	cleanupSession, err := w.db.CreateCleanupSession(
		ctx,
		database.CreateCleanupSessionParams{
			CreatedBy: pgUserID,
			FileName: pgtype.Text{
				String: fileName,
				Valid:  fileName != "",
			},
			RowCount: int32(totalRowCount),
			RealmID: pgtype.Text{
				String: realmID,
				Valid:  realmID != "",
			},
			OutflowIs: outflowIs,
		},
	)
	if err != nil {
		w.logger.Error(
			"PcmWorker: failed to create cleanup session",
			"error", err,
			"conversation_session_id", conversationSessionID,
		)

		return pgtype.UUID{},
			"",
			fileName,
			fmt.Errorf(
				"pcm: create cleanup session: %w",
				err,
			)
	}

	if !cleanupSession.ID.Valid {
		return pgtype.UUID{},
			"",
			fileName,
			fmt.Errorf(
				"pcm: cleanup session returned invalid ID",
			)
	}

	stagingSessionID := cleanupSession.ID
	stagingSessionIDStr := uuid.UUID(
		stagingSessionID.Bytes,
	).String()

	rowIndex := int32(0)

	for _, document := range documents {
		if !isBankStatementType(document.Type) {
			continue
		}

		if document.ColumnMapping != nil &&
			document.ColumnMapping.PolaritySign == "none" &&
			document.ColumnMapping.IsAmbiguous {

			w.logger.Info(
				"PcmWorker: statement marked structurally ambiguous by OCR",
				"document_id", document.DocumentID,
				"reason", document.ColumnMapping.AmbiguityReason,
			)
		}

		transactions := extractTransactionMaps(
			document.Data["transactions"],
		)

		for _, transaction := range transactions {
			description, _ := transaction["description"].(string)
			transactionType, _ := transaction["type"].(string)
			transactionDate, _ := transaction["date"].(string)

			amount := formatPCMAmount(
				transaction["amount"],
				transactionType,
			)

			_, err := w.db.InsertCleanupRow(
				ctx,
				database.InsertCleanupRowParams{
					SessionID: stagingSessionID,
					RowIndex: pgtype.Int4{
						Int32: rowIndex,
						Valid: true,
					},
					SourceType: "BankStatement",
					RawDescription: pgtype.Text{
						String: description,
						Valid:  description != "",
					},
					RawAmount: amount,
					RawDate: pgtype.Text{
						String: transactionDate,
						Valid:  transactionDate != "",
					},
					Status: pgtype.Text{
						String: "PENDING",
						Valid:  true,
					},
				},
			)
			if err != nil {
				w.logger.Error(
					"PcmWorker: failed to insert staging transaction",
					"error", err,
					"staging_session_id", stagingSessionIDStr,
					"document_id", document.DocumentID,
					"row_index", rowIndex,
				)

				return pgtype.UUID{},
					"",
					fileName,
					fmt.Errorf(
						"pcm: insert staging transaction row %d: %w",
						rowIndex,
						err,
					)
			}

			rowIndex++
		}
	}

	w.logger.Info(
		"PcmWorker: staged bank statement transactions",
		"staging_session_id", stagingSessionIDStr,
		"conversation_session_id", conversationSessionID,
		"transaction_count", rowIndex,
		"document_count", len(documents),
	)

	return stagingSessionID,
		stagingSessionIDStr,
		fileName,
		nil
}

func resolvePCMFileName(
	inbound *PcmInboundMessage,
	documents []PcmParsedDocument,
) string {
	for _, document := range documents {
		if document.FileName != "" {
			return document.FileName
		}

		if document.DocumentID == "" {
			continue
		}

		for _, attachment := range inbound.Attachments {
			if attachment.DocumentID == document.DocumentID &&
				attachment.Name != "" {
				return attachment.Name
			}
		}
	}

	for _, attachment := range inbound.Attachments {
		if attachment.Name != "" {
			return attachment.Name
		}
	}

	if inbound.Subject != "" {
		return inbound.Subject
	}

	return "Email_Bank_Statement.pdf"
}

func formatPCMAmount(
	rawAmount interface{},
	transactionType string,
) string {
	var amount string

	switch value := rawAmount.(type) {
	case float64:
		if value < 0 {
			value = -value
		}

		amount = strconv.FormatFloat(
			value,
			'f',
			2,
			64,
		)

	case float32:
		floatValue := float64(value)

		if floatValue < 0 {
			floatValue = -floatValue
		}

		amount = strconv.FormatFloat(
			floatValue,
			'f',
			2,
			64,
		)

	case int:
		if value < 0 {
			value = -value
		}

		amount = strconv.Itoa(value)

	case int32:
		if value < 0 {
			value = -value
		}

		amount = strconv.FormatInt(
			int64(value),
			10,
		)

	case int64:
		if value < 0 {
			value = -value
		}

		amount = strconv.FormatInt(
			value,
			10,
		)

	case json.Number:
		valueString := strings.TrimSpace(value.String())
		valueString = strings.TrimPrefix(valueString, "-")
		valueString = strings.TrimPrefix(valueString, "+")

		amount = valueString

	case string:
		value = strings.TrimSpace(value)
		value = strings.TrimPrefix(value, "-")
		value = strings.TrimPrefix(value, "+")

		amount = value
	}

	if amount == "" {
		return ""
	}

	if strings.EqualFold(
		strings.TrimSpace(transactionType),
		"debit",
	) {
		return "-" + amount
	}

	return amount
}

// publishOutflowResult reports PCM completion.
//
// During orchestrated execution it returns both the staging result and the raw
// readiness documents. Keeping Documents in the proof means subsequent steps
// can request documents via workflow_schema without having to reach backwards
// into document_readiness or ToroDB.
func (w *PcmWorker) publishOutflowResult(
	inbound *PcmInboundMessage,
	reqEnv *core.Envelope,
	documents []PcmParsedDocument,
	entityID pgtype.UUID,
	stagingSessionID pgtype.UUID,
	stagingSessionIDStr string,
	conversationSessionID string,
	realmID string,
	fileName string,
) error {
	entityIDStr := uuid.UUID(
		entityID.Bytes,
	).String()

	cid := reqEnv.ConversationID

	if cid == "" {
		cid = inbound.ConversationID
	}

	if cid != "" && w.nc != nil {
		proofData := map[string]interface{}{
			// Keep session_id as the staging session for backwards
			// compatibility with the downstream PCM/ASE pipeline.
			"session_id": stagingSessionIDStr,

			"staging_session_id":      stagingSessionIDStr,
			"conversation_session_id": conversationSessionID,

			"entity_id": entityIDStr,
			"realm_id":  realmID,

			"from_handle": inbound.FromHandle,
			"to_handle":   inbound.ToHandle,

			"document_verified": inbound.DocumentVerified,
			"document_count":    len(documents),

			// Preserve the document_readiness contract for later workflow
			// stages, including raw_ocr_json.
			"documents": inbound.Documents,
		}

		proofDataBytes, err := json.Marshal(proofData)
		if err != nil {
			return fmt.Errorf(
				"pcm: marshal completion proof data: %w",
				err,
			)
		}

		proof := core.Proof{
			Type:      core.ProofAPI,
			Timestamp: time.Now().Unix(),
			Data:      json.RawMessage(proofDataBytes),
		}

		replyEnv, err := core.NewEnvelope(
			uuid.New().String(),
			"worker.pcm",
			workflows.OrchestratorInbox,
			cid,
			core.INFORM,
			proof,
		)
		if err != nil {
			return fmt.Errorf(
				"pcm: build completion envelope: %w",
				err,
			)
		}

		replyBytes, err := json.Marshal(replyEnv)
		if err != nil {
			return fmt.Errorf(
				"pcm: marshal completion envelope: %w",
				err,
			)
		}

		if err := w.nc.Publish(
			workflows.OrchestratorInbox,
			replyBytes,
		); err != nil {
			return fmt.Errorf(
				"pcm: publish completion proof: %w",
				err,
			)
		}

		w.logger.Info(
			"PcmWorker: sent completion proof to TAP Orchestrator",
			"cid", cid,
			"staging_session_id", stagingSessionIDStr,
			"document_count", len(documents),
		)

		return nil
	}

	// Non-orchestrated compatibility path.
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

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf(
			"pcm: marshal outflow payload: %w",
			err,
		)
	}

	taskDef := core.TaskDefinition{
		ID:         stagingSessionIDStr,
		Domain:     "accounting.pcm_bookkeeping",
		Complexity: 0,
		Payload:    payloadBytes,
	}

	kp, err := identity.KeyPairFromSeed("gateway")
	if err != nil {
		return fmt.Errorf(
			"pcm: create gateway identity: %w",
			err,
		)
	}

	gateDID := identity.CreateDID(kp.Public)

	informEnv, err := core.NewEnvelope(
		uuid.New().String(),
		gateDID,
		"",
		uuid.New().String(),
		core.REQUEST,
		taskDef,
	)
	if err == nil && w.nc != nil {
		informEnv.Signature = kp.Sign(informEnv.Body)

		informBytes, marshalErr := json.Marshal(informEnv)
		if marshalErr != nil {
			return fmt.Errorf(
				"pcm: marshal signed TAP event: %w",
				marshalErr,
			)
		}

		topic := core.BuildEventSubject(
			"accounting",
			core.ComplexityEntry,
			"pcm_bookkeeping",
		)

		if publishErr := w.nc.Publish(
			topic,
			informBytes,
		); publishErr == nil {

			w.logger.Info(
				"PcmWorker: published signed TAP event to JetStream",
				"topic", topic,
				"session_id", stagingSessionIDStr,
			)

			return nil
		}
	}

	// Final compatibility fallback to ase_bridge.
	ocrPayload := map[string]interface{}{
		"entity_id":  entityIDStr,
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

	bodyBytes, err := json.Marshal(bodyData)
	if err != nil {
		return fmt.Errorf(
			"pcm: marshal ASE bridge payload: %w",
			err,
		)
	}

	env.Body = bodyBytes

	envBytes, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf(
			"pcm: marshal ASE bridge envelope: %w",
			err,
		)
	}

	if w.nc == nil {
		return fmt.Errorf(
			"pcm: NATS unavailable while publishing ASE fallback",
		)
	}

	if err := w.nc.Publish(
		core.BuildWorkerInbox("ase_bridge"),
		envBytes,
	); err != nil {
		w.logger.Error(
			"PcmWorker: failed to publish fallback to ase_bridge",
			"error", err,
		)

		return fmt.Errorf(
			"pcm: publish ASE fallback: %w",
			err,
		)
	}

	w.logger.Info(
		"PcmWorker: successfully published fallback payload to DAG",
		"realm_id", realmID,
	)

	return nil
}

func (w *PcmWorker) sendSkipProofToOrchestrator(
	inbound *PcmInboundMessage,
	reqEnv *core.Envelope,
	sessionIDStr string,
	realmID string,
) error {
	cid := reqEnv.ConversationID

	if cid == "" {
		cid = inbound.ConversationID
	}

	if cid == "" || w.nc == nil {
		return nil
	}

	proofData := map[string]interface{}{
		"session_id":        sessionIDStr,
		"realm_id":          realmID,
		"status":            "SKIPPED_NO_BANK_STATEMENT",
		"document_verified": inbound.DocumentVerified,
		"document_count":    len(inbound.Documents),
		"documents":         inbound.Documents,
	}

	proofDataBytes, err := json.Marshal(proofData)
	if err != nil {
		return fmt.Errorf(
			"pcm: marshal skip proof: %w",
			err,
		)
	}

	proof := core.Proof{
		Type:      core.ProofAPI,
		Timestamp: time.Now().Unix(),
		Data:      json.RawMessage(proofDataBytes),
	}

	replyEnv, err := core.NewEnvelope(
		uuid.New().String(),
		"worker.pcm",
		workflows.OrchestratorInbox,
		cid,
		core.INFORM,
		proof,
	)
	if err != nil {
		return fmt.Errorf(
			"pcm: build skip proof envelope: %w",
			err,
		)
	}

	replyBytes, err := json.Marshal(replyEnv)
	if err != nil {
		return fmt.Errorf(
			"pcm: marshal skip proof envelope: %w",
			err,
		)
	}

	if err := w.nc.Publish(
		workflows.OrchestratorInbox,
		replyBytes,
	); err != nil {
		return fmt.Errorf(
			"pcm: publish skip proof: %w",
			err,
		)
	}

	w.logger.Info(
		"PcmWorker: sent skip proof to TAP Orchestrator",
		"cid", cid,
	)

	return nil
}

// Ensure PcmWorker satisfies the Worker interface at compile time.
var _ Worker = (*PcmWorker)(nil)
