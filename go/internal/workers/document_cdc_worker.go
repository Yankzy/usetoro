package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/cdc"
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/erp/ase/knowledge_system"
	"github.com/Yankzy/usetoro/internal/infra"
)

// DocumentCDCWorker subscribes to NATS JetStream CDC events on ledger.toro_core.documents.insert
// and automatically dispatches OCR task payloads whenever a document is saved with ocr_status = 'PENDING'.
type DocumentCDCWorker struct {
	db      *database.Queries
	pool    *pgxpool.Pool
	logger  *slog.Logger
	cfg     *config.Config
	nc      *nats.Conn
	storage infra.S3Service
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		storageSvc, err := infra.NewS3Service(deps.Config)
		if err != nil {
			deps.Logger.Warn("DocumentCDCWorker: S3 storage not configured", "error", err)
		}
		return &DocumentCDCWorker{
			db:      deps.Store.Queries,
			pool:    deps.Store.Pool,
			logger:  deps.Logger.With("worker", "document_cdc"),
			cfg:     deps.Config,
			nc:      deps.Queue,
			storage: storageSvc,
		}, nil
	})
}

func (w *DocumentCDCWorker) Init(ctx context.Context) error {
	w.logger.Info("DocumentCDCWorker initialized")
	return nil
}

func (w *DocumentCDCWorker) Subscriptions() []SubscriptionConfig {
	subject := "ledger.toro_core.documents.>"
	if w.cfg != nil {
		_, workerCfg := w.cfg.Workers.GetForWorker(w)
		if workerCfg.Subject != "" && workerCfg.Subject != "ledger.toro_core.documents.insert" {
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

func (w *DocumentCDCWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	var event cdc.Event
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		w.logger.Error("DocumentCDCWorker: failed to unmarshal CDC Event", "error", err)
		msg.Term()
		return nil
	}

	ocrStatus, _ := event.Data["ocr_status"].(string)
	if ocrStatus != "PENDING" {
		msg.Ack()
		return nil
	}

	docIDStr, _ := event.Data["id"].(string)
	docID, errParse := uuid.Parse(docIDStr)
	if errParse != nil {
		w.logger.Error("DocumentCDCWorker: failed to parse document UUID from CDC event", "id", docIDStr, "error", errParse)
		msg.Ack()
		return nil
	}

	fileName, _ := event.Data["file_name"].(string)
	s3URL, _ := event.Data["s3_url"].(string)

	w.logger.Info("DocumentCDCWorker: detected PENDING document via CDC replication", "doc_id", docID, "file_name", fileName)

	var docStore *knowledge_system.DocumentStore
	var kie *knowledge_system.KnowledgeIngestionEngine
	var doc *knowledge_system.Document

	if w.pool != nil {
		docStore = knowledge_system.NewDocumentStore(w.pool, w.logger)
		graphStore := knowledge_system.NewGraphStore(w.pool, w.logger)
		vectorStore := ase.NewVectorStore(w.pool, nil, w.logger)
		kie = knowledge_system.NewKnowledgeIngestionEngine(docStore, graphStore, vectorStore, w.logger)
		doc, _ = docStore.GetDocumentByID(ctx, docID)
	}

	if doc == nil {
		doc = &knowledge_system.Document{
			ID:        docID,
			FileName:  fileName,
			S3URL:     s3URL,
			OCRStatus: "PENDING",
		}
	}

	s3Key := s3URL
	var docURL string
	if s3Key != "" {
		if w.storage == nil {
			w.logger.Error("DocumentCDCWorker: S3 storage service is nil, cannot generate presigned URL", "doc_id", docID, "s3_key", s3Key)
			msg.Nak()
			return fmt.Errorf("DocumentCDCWorker: S3 storage service is nil")
		}
		presignedURL, err := w.storage.GeneratePresignedURL(ctx, s3Key, 24*time.Hour)
		if err != nil {
			w.logger.Error("DocumentCDCWorker: failed to generate presigned URL for S3 key", "error", err, "doc_id", docID, "s3_key", s3Key)
			msg.Nak()
			return fmt.Errorf("DocumentCDCWorker: failed to generate presigned S3 URL: %w", err)
		}
		docURL = presignedURL
		w.logger.Info("DocumentCDCWorker: generated presigned URL for OCR dispatch", "doc_id", docID, "s3_key", s3Key, "presigned_url_value", docURL)
	}

	callbackTopic := "events.accounting.1.pcm_bookkeeping"
	taskPayload := map[string]any{
		"document_id":  docID.String(),
		"file_name":    fileName,
		"document_url": docURL,
		"s3_url":       s3Key,
		"s3_key":       s3Key,
	}

	if len(doc.Metadata) > 0 {
		var metaMap map[string]any
		if err := json.Unmarshal(doc.Metadata, &metaMap); err == nil {
			for k, v := range metaMap {
				taskPayload[k] = v
			}
			if cb, ok := metaMap["callback_topic"].(string); ok && cb != "" {
				callbackTopic = cb
			}
		}
	}

	// Route Python OCR output through the Knowledge Ingestion Worker.
	// The ingestion worker will update the document status, run L1/L2/L3
	// ingestion, and then forward to the original callback topic.
	taskPayload["final_destination_subject"] = "worker.inbox.go.knowledge_ingest"
	taskPayload["original_callback_topic"] = callbackTopic

	if kie != nil && w.nc != nil {
		if err := kie.DispatchOCR(ctx, w.nc, doc, taskPayload); err != nil {
			w.logger.Error("DocumentCDCWorker: failed to dispatch OCR task", "doc_id", docID, "error", err)
			msg.Nak()
			return err
		}
	} else {
		w.logger.Error("DocumentCDCWorker: missing KIE or NATS connection, cannot dispatch OCR", "doc_id", docID)
		msg.Nak()
		return fmt.Errorf("DocumentCDCWorker: missing dependencies for OCR dispatch")
	}

	msg.Ack()
	return nil
}
