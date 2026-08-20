package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	know "github.com/Yankzy/usetoro/internal/erp/ase/knowledge_system"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// DocumentCreatePayload represents the NATS event contract for requesting document registration in ToroDB.
type DocumentCreatePayload struct {
	EntityID      string            `json:"entity_id,omitempty"`
	SenderEmail   string            `json:"sender_email,omitempty"`
	SessionID     string            `json:"session_id,omitempty"`
	DocumentType  know.DocumentType `json:"document_type"`
	FileName      string            `json:"file_name"`
	MimeType      string            `json:"mime_type"`
	S3Key         string            `json:"s3_key"`
	SHA256        string            `json:"sha256,omitempty"`
	SourceChannel string            `json:"source_channel,omitempty"`
	Metadata      map[string]any    `json:"metadata,omitempty"`
}

// DocumentWorker is a tenant-aware, user-aware worker responsible for registering documents
// into ToroDB (toro_core.documents) asynchronously over NATS.
//
// This worker decouples ingress adapters (such as PostmarkInboundEmailWorker) from direct ToroDB
// database persistence, enabling ToroDB to be deployed independently on separate host/container infrastructure.
type DocumentWorker struct {
	db     *database.Queries
	pool   *pgxpool.Pool
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		return &DocumentWorker{
			db:     deps.Store.Queries,
			pool:   deps.Store.Pool,
			logger: deps.Logger.With("worker", "document"),
			cfg:    deps.Config,
			nc:     deps.Queue,
		}, nil
	})
}

func (w *DocumentWorker) Init(ctx context.Context) error {
	w.logger.Info("DocumentWorker initialized")
	return nil
}

func (w *DocumentWorker) Subscriptions() []SubscriptionConfig {
	subject := "worker.inbox.document.create"
	if w.cfg != nil {
		_, workerCfg := w.cfg.Workers.GetForWorker(w)
		if workerCfg.Subject != "" {
			subject = workerCfg.Subject
		} else if workerCfg.ActivityType != "" {
			if derived, err := core.BuildWorkerInboxFromActivity(workerCfg.ActivityType); err == nil {
				subject = derived
			}
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

func (w *DocumentWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	logger := w.getLogger()

	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		logger.Error("DocumentWorker: poison pill exceeded retries", "subject", msg.Subject)
		msg.Term()
		return nil
	}

	var payload DocumentCreatePayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		logger.Error("DocumentWorker: failed to unmarshal DocumentCreatePayload", "error", err)
		msg.Term()
		return nil
	}

	sessionID := payload.SessionID

	if payload.S3Key == "" || payload.FileName == "" {
		logger.Error("DocumentWorker: missing required s3_key or file_name in payload", "session_id", sessionID)
		msg.Term()
		return nil
	}

	if w.pool == nil {
		logger.Error("DocumentWorker: database pool is nil, cannot register document", "session_id", sessionID, "file_name", payload.FileName)
		return fmt.Errorf("DocumentWorker: database pool is nil")
	}

	docStore := know.NewDocumentStore(w.pool, logger)
	doc, result, err := docStore.CreateOrGetDocument(
		ctx,
		sessionID,
		payload.DocumentType,
		payload.FileName,
		payload.MimeType,
		payload.S3Key,
		payload.SHA256,
		payload.SourceChannel,
		payload.SenderEmail,
		payload.Metadata,
	)

	if err != nil {
		logger.Error("DocumentWorker: failed to create or get document in ToroDB", "session_id", sessionID, "file_name", payload.FileName, "error", err)
		return fmt.Errorf("DocumentWorker: CreateOrGetDocument failed: %w", err)
	}

	switch result {
	case know.CreateResultExisting:
		logger.Info("DocumentWorker: duplicate sha256 detected, fast-forwarding existing document", "sha256", payload.SHA256, "session_id", sessionID, "doc_id", doc.ID)

		callbackTopic, _ := payload.Metadata["callback_topic"].(string)
		if callbackTopic != "" && w.nc != nil {
			switch doc.OCRStatus {
			case string(know.DocStatusEmbeddingsSuccess):
				// Both OCR and embeddings are complete. Forward stored OCR extraction directly.
				w.forwardDuplicateOCR(logger, doc, payload, callbackTopic)

			case string(know.DocStatusOCRSuccess):
				// OCR succeeded previously, but embeddings were not complete.
				// Run knowledge ingestion directly without re-running Go OCR Agent.
				graphStore := know.NewGraphStore(w.pool, logger)
				vectorStore := ase.NewVectorStore(w.pool, nil, logger)
				kie := know.NewKnowledgeIngestionEngine(docStore, graphStore, vectorStore, logger)

				if ingestErr := kie.IngestProcessedDocument(ctx, doc); ingestErr != nil {
					logger.Warn("DocumentWorker: duplicate document embedding ingestion failed", "doc_id", doc.ID, "error", ingestErr)
				} else {
					_, _ = docStore.UpdateDocumentStatus(ctx, doc.ID, string(know.DocStatusEmbeddingsSuccess))
					logger.Info("DocumentWorker: duplicate document embedding ingestion complete", "doc_id", doc.ID)
				}
				w.forwardDuplicateOCR(logger, doc, payload, callbackTopic)

			default:
				// Document has not completed OCR (PENDING, PROCESSING, or FAILED).
				// Reset status to PENDING so CDC re-triggers Go OCR Agent.
				_, errUpdate := docStore.UpdateDocumentStatus(ctx, doc.ID, string(know.DocStatusPending))
				if errUpdate != nil {
					logger.Error("DocumentWorker: failed to reset duplicate document to PENDING", "error", errUpdate)
				} else {
					logger.Info("DocumentWorker: duplicate document reset status to PENDING for CDC Go OCR Agent re-process", "doc_id", doc.ID)
				}
			}
		}

	case know.CreateResultCreated:
		logger.Info("DocumentWorker: successfully registered document in ToroDB with PENDING status (CDC handoff complete)",
			"doc_id", doc.ID,
			"session_id", doc.SessionID,
			"ocr_status", doc.OCRStatus,
			"file_name", doc.FileName,
		)
	}

	return nil

}

func (w *DocumentWorker) forwardDuplicateOCR(logger *slog.Logger, existingDoc *know.Document, payload DocumentCreatePayload, callbackTopic string) {
	var ocrExtraction map[string]any
	_ = json.Unmarshal(existingDoc.RawOCRJSON, &ocrExtraction)
	if ocrExtraction == nil {
		ocrExtraction = make(map[string]any)
	}
	if existingDoc.ExtractedText != "" {
		ocrExtraction["raw_text"] = existingDoc.ExtractedText
	}

	if strings.Contains(callbackTopic, "pcm_bookkeeping") && !isBankStatementDoc(existingDoc.DocumentType, ocrExtraction) {
		logger.Info("DocumentWorker: skipping duplicate document callback trigger because document is not a bank statement", "doc_id", existingDoc.ID, "doc_type", existingDoc.DocumentType, "topic", callbackTopic)
		return
	}

	forwardPayload := map[string]any{
		"status":                  "OCR_SUCCESS",
		"document_id":             existingDoc.ID.String(),
		"original_callback_topic": callbackTopic,
		"ocr_extraction":          ocrExtraction,
		"ocr_extractions":         []any{ocrExtraction},
		"document_url":            existingDoc.S3URL,
		"total_attachments":       1,
		"attachments": []map[string]any{
			{
				"name":         existingDoc.FileName,
				"document_url": existingDoc.S3URL,
				"content_type": existingDoc.MimeType,
			},
		},
	}
	for k, v := range payload.Metadata {
		forwardPayload[k] = v
	}

	forwardBytes, _ := json.Marshal(forwardPayload)
	if pubErr := w.nc.Publish(callbackTopic, forwardBytes); pubErr != nil {
		logger.Error("DocumentWorker: failed to forward duplicate document to callback", "error", pubErr)
	} else {
		logger.Info("DocumentWorker: successfully forwarded duplicate document", "doc_id", existingDoc.ID, "topic", callbackTopic)
	}
}

func isBankStatementDoc(docType know.DocumentType, ocrExtraction map[string]any) bool {
	if docType == know.DocTypeBankStatement {
		return true
	}
	dtStr := strings.ToUpper(string(docType))
	if dtStr == "BANK_STATEMENT" || dtStr == "STATEMENT" {
		return true
	}
	if ocrExtraction != nil {
		if dt, ok := ocrExtraction["doc_type"].(string); ok {
			dtUpper := strings.ToUpper(dt)
			if dtUpper == "BANK_STATEMENT" || dtUpper == "STATEMENT" {
				return true
			}
		}
		if dt, ok := ocrExtraction["document_type"].(string); ok {
			dtUpper := strings.ToUpper(dt)
			if dtUpper == "BANK_STATEMENT" || dtUpper == "STATEMENT" {
				return true
			}
		}
	}
	return false
}

func (w *DocumentWorker) getLogger() *slog.Logger {
	if w.logger != nil {
		return w.logger
	}
	return slog.Default().With("worker", "document")
}

// Ensure DocumentWorker satisfies the Worker interface at compile time.
var _ Worker = (*DocumentWorker)(nil)
