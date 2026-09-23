package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/knowledge_system"
)

// OCRResultWorker subscribes to OCR callback results and completes the
// Knowledge System ingestion loop: UpdateOCRStatus → IngestProcessedDocument → forward.
//
// This worker closes the gap identified in audit F-01: previously, OCR
// results were published directly to the downstream accounting pipeline,
// bypassing L1/L2/L3 knowledge ingestion entirely.
type OCRResultWorker struct {
	db     *database.Queries
	pool   *pgxpool.Pool
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &OCRResultWorker{
			db:     deps.Store.Queries,
			pool:   deps.Store.Pool,
			logger: deps.Logger.With("worker", "ocr_result"),
			cfg:    deps.Config,
			nc:     deps.Queue,
		}, nil
	})
}

func (w *OCRResultWorker) Init(ctx context.Context) error {
	w.logger.Info("OCRResultWorker initialized")
	return nil
}

func (w *OCRResultWorker) Subscriptions() []SubscriptionConfig {
	subject := "worker.inbox.go.knowledge_ingest"
	if w.cfg != nil {
		_, workerCfg := w.cfg.Workers.GetForWorker(w)
		if workerCfg.Subject != "" {
			subject = workerCfg.Subject
		}
	}
	group := groupFromSubject(subject)

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

func (w *OCRResultWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	logger := w.getLogger()

	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		logger.Error("OCRResultWorker: poison pill exceeded retries", "subject", msg.Subject)
		msg.Term()
		return nil
	}

	// Parse the OCR result payload
	var payload map[string]any
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		logger.Error("OCRResultWorker: failed to unmarshal payload", "error", err)
		return nil
	}

	status, _ := payload["status"].(string)
	docIDStr, _ := payload["document_id"].(string)
	var docID uuid.UUID
	if docIDStr != "" {
		docID, _ = uuid.Parse(docIDStr)
	}

	if status != "OCR_SUCCESS" {
		logger.Warn("OCRResultWorker: received non-success OCR result, updating DB status to FAILED and forwarding",
			"status", status, "doc_id", docIDStr)
		if docID != uuid.Nil && w.pool != nil {
			docStore := knowledge_system.NewDocumentStore(w.pool, logger)
			_, _ = docStore.UpdateOCRStatus(ctx, docID, "FAILED", map[string]any{"error": "OCR non-success status", "status": status}, "")
		}
		w.forwardToCallback(payload)
		return nil
	}

	if docIDStr == "" || docID == uuid.Nil {
		logger.Warn("OCRResultWorker: missing or invalid document_id in OCR result, forwarding as-is")
		w.forwardToCallback(payload)
		return nil
	}

	if w.pool == nil {
		logger.Error("OCRResultWorker: database pool is nil, cannot complete ingestion")
		w.forwardToCallback(payload)
		return nil
	}

	docStore := knowledge_system.NewDocumentStore(w.pool, logger)
	graphStore := knowledge_system.NewGraphStore(w.pool, logger)
	vectorStore := ase.NewVectorStore(w.pool, nil, logger)
	kie := knowledge_system.NewKnowledgeIngestionEngine(docStore, graphStore, vectorStore, logger)

	// 1. Fetch the document from toro_core.documents
	doc, fetchErr := docStore.GetDocumentByID(ctx, docID)
	if fetchErr != nil || doc == nil {
		logger.Error("OCRResultWorker: document not found in toro_core.documents",
			"doc_id", docID, "error", fetchErr)
		w.forwardToCallback(payload)
		return nil
	}

	// 2. Extract OCR data from the ocr agent
	ocrExtraction, _ := payload["ocr_extraction"].(map[string]any)
	if ocrExtraction == nil {
		ocrExtraction, _ = payload["ocr_result"].(map[string]any)
	}
	if ocrExtraction == nil {
		logger.Warn("OCRResultWorker: no ocr_extraction in payload, updating DB status to FAILED and skipping ingestion")
		_, _ = docStore.UpdateOCRStatus(ctx, docID, "FAILED", map[string]any{"error": "Missing ocr_extraction in payload"}, "")
		w.forwardToCallback(payload)
		return nil
	}

	// Build raw OCR map for storage
	rawOCR := map[string]any{
		"doc_type":   ocrExtraction["doc_type"],
		"confidence": ocrExtraction["confidence"],
		"data":       ocrExtraction["data"],
	}
	if cm, ok := ocrExtraction["column_mapping"]; ok {
		rawOCR["column_mapping"] = cm
	}

	extractedText := ""
	if rt, ok := ocrExtraction["raw_text"].(string); ok {
		extractedText = rt
	}

	// 3. Update toro_core.documents to OCR_SUCCESS
	ocrDoc, updateErr := docStore.UpdateOCRStatus(ctx, docID, string(knowledge_system.DocStatusOCRSuccess), rawOCR, extractedText)
	if updateErr != nil {
		logger.Error("OCRResultWorker: failed to update document OCR status",
			"doc_id", docID, "error", updateErr)
		w.forwardToCallback(payload)
		return nil
	}

	// 4. Ingest into Knowledge System L1 Facts, L2 Graph Edges, L3 Vector Memory
	if ingestErr := kie.IngestProcessedDocument(ctx, ocrDoc); ingestErr != nil {
		logger.Warn("OCRResultWorker: knowledge ingestion failed (non-fatal, still forwarding)",
			"doc_id", docID, "error", ingestErr)
	} else {
		logger.Info("OCRResultWorker: successfully ingested document into L1/L2/L3",
			"doc_id", docID, "type", ocrDoc.DocumentType)
		if _, statusErr := docStore.UpdateDocumentStatus(ctx, docID, string(knowledge_system.DocStatusEmbeddingsSuccess)); statusErr != nil {
			logger.Warn("OCRResultWorker: failed to update document status to EMBEDDINGS_SUCCESS",
				"doc_id", docID, "error", statusErr)
		}
	}

	// 5. Forward to the original callback topic for downstream processing
	w.forwardToCallback(payload)

	return nil
}

func (w *OCRResultWorker) getLogger() *slog.Logger {
	if w.logger != nil {
		return w.logger
	}
	return slog.Default().With("worker", "ocr_result")
}

// forwardToCallback publishes only to an explicit producer callback. Document
// type and workflow selection belong to the Dynamic Agent/blueprint layer.
func (w *OCRResultWorker) forwardToCallback(payload map[string]any) {
	logger := w.getLogger()
	callbackTopic, _ := payload["original_callback_topic"].(string)
	if callbackTopic == "" || callbackTopic == "worker.inbox.go.knowledge_ingest" {
		logger.Debug("OCRResultWorker: knowledge ingestion complete, no external callback to forward to")
		return
	}

	if w.nc == nil {
		logger.Error("OCRResultWorker: NATS connection is nil, cannot forward to callback")
		return
	}

	// For the Django accounting intake boundary, emit ONLY after OCR_SUCCESS
	// and emit a small, lightweight event instead of the massive financial extraction payload.
	if callbackTopic == "worker.inbox.python.accounting_intake" {
		statusStr, _ := payload["status"].(string)
		if statusStr != "OCR_SUCCESS" {
			logger.Warn("OCRResultWorker: suppressing accounting intake dispatch for non-success OCR result",
				"status", statusStr, "doc_id", payload["document_id"])
			return
		}

		docIDStr, _ := payload["document_id"].(string)
		entityIDStr, _ := payload["entity_id"].(string)
		sessionIDStr, _ := payload["session_id"].(string)
		sourceMsgIDStr, _ := payload["external_id"].(string)
		if sourceMsgIDStr == "" {
			sourceMsgIDStr, _ = payload["source_message_id"].(string)
		}
		fileName, _ := payload["file_name"].(string)

		accountingEvent := map[string]any{
			"document_id":       docIDStr,
			"entity_id":         entityIDStr,
			"session_id":        sessionIDStr,
			"source_message_id": sourceMsgIDStr,
			"file_name":         fileName,
			"status":            "OCR_SUCCESS",
		}

		outBytes, err := json.Marshal(accountingEvent)
		if err != nil {
			logger.Error("OCRResultWorker: failed to marshal accounting intake payload", "error", err)
			return
		}

		if pubErr := w.nc.Publish(callbackTopic, outBytes); pubErr != nil {
			logger.Error("OCRResultWorker: failed to forward to accounting intake topic",
				"topic", callbackTopic, "error", pubErr)
		} else {
			logger.Info("OCRResultWorker: forwarded small accounting intake event",
				"topic", callbackTopic, "doc_id", docIDStr)
		}
		return
	}

	// Build the outgoing payload that the downstream worker expects.
	outBytes, err := json.Marshal(payload)
	if err != nil {
		logger.Error("OCRResultWorker: failed to marshal forward payload", "error", err)
		return
	}

	if pubErr := w.nc.Publish(callbackTopic, outBytes); pubErr != nil {
		logger.Error("OCRResultWorker: failed to forward to callback topic",
			"topic", callbackTopic, "error", pubErr)
	} else {
		logger.Info("OCRResultWorker: forwarded OCR result to callback",
			"topic", callbackTopic)
	}
}

func isBankStatementPayload(payload map[string]any) bool {
	checkType := func(val any) bool {
		if s, ok := val.(string); ok {
			upper := strings.ToUpper(s)
			if upper == "BANK_STATEMENT" || upper == "STATEMENT" {
				return true
			}
		}
		return false
	}

	if checkType(payload["document_type"]) || checkType(payload["doc_type"]) {
		return true
	}

	if ocrExt, ok := payload["ocr_extraction"].(map[string]any); ok {
		if checkType(ocrExt["doc_type"]) || checkType(ocrExt["document_type"]) {
			return true
		}
	}

	if ocrExts, ok := payload["ocr_extractions"].([]any); ok {
		for _, item := range ocrExts {
			if extMap, ok := item.(map[string]any); ok {
				if checkType(extMap["doc_type"]) || checkType(extMap["document_type"]) {
					return true
				}
			}
		}
	}

	return false
}

// Ensure OCRResultWorker satisfies the Worker interface at compile time.
var _ Worker = (*OCRResultWorker)(nil)
