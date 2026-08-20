package knowledge_system

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/infra"
	csvmapping "github.com/Yankzy/usetoro/tap/agents/csv_mapping"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

// Deprecated: DocumentOCRWorker is a legacy native Go LLM worker.
// Production OCR ingestion is handled by DocumentCDCWorker
// -> OCR agent (ocr_agent) -> OCRResultWorker (ocr_result_worker.go).
// OCRExtraction holds structured visual perception output matching python-worker/app/openai/ocr.py.
type OCRExtraction struct {
	DocType       string                       `json:"doc_type"`
	FileName      string                       `json:"file_name,omitempty"`
	S3URL         string                       `json:"s3_url,omitempty"`
	Data          map[string]interface{}       `json:"data"`
	ColumnMapping *csvmapping.LLMColumnMapping `json:"column_mapping,omitempty"`
	Confidence    float64                      `json:"confidence"`
	RawText       string                       `json:"raw_text,omitempty"`
}

// DocumentOCRWorker is the native ToroDB background engine responsible for
// visual document perception and OCR state transitions.
type DocumentOCRWorker struct {
	docStore *DocumentStore
	kie      *KnowledgeIngestionEngine
	pool     *pgxpool.Pool
	nc       *nats.Conn
	s3       infra.S3Service
	logger   *slog.Logger
	rt       *agent.Runtime
	mu       sync.Mutex
}

// NewDocumentOCRWorker instantiates a native ToroDB DocumentOCRWorker.
func NewDocumentOCRWorker(
	docStore *DocumentStore,
	kie *KnowledgeIngestionEngine,
	pool *pgxpool.Pool,
	nc *nats.Conn,
	s3 infra.S3Service,
	logger *slog.Logger,
) *DocumentOCRWorker {
	if logger == nil {
		logger = slog.Default()
	}

	rtCfg := core.AgentConfig{
		DID:          "did:toro:torodb-ocr-worker",
		Model:        "gpt-4o",
		SystemPrompt: ocrSystemPrompt,
	}
	rt := agent.NewRuntime(logger, nil, rtCfg)

	return &DocumentOCRWorker{
		docStore: docStore,
		kie:      kie,
		pool:     pool,
		nc:       nc,
		s3:       s3,
		logger:   logger.With("component", "torodb.document_ocr_worker"),
		rt:       rt,
	}
}

const ocrSystemPrompt = `You are an expert OCR document parser. Analyze the document and extract structured JSON matching one of these exact types:

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

// ProcessPendingDocument performs native visual LLM inspection, updates toro_core.documents
// to 'PROCESSED', and triggers L1/L2/L3 fact ingestion.
func (w *DocumentOCRWorker) ProcessPendingDocument(ctx context.Context, doc *Document) (*OCRExtraction, error) {
	if doc == nil || doc.OCRStatus != "PENDING" {
		return nil, fmt.Errorf("document ocr worker: document is nil or not in PENDING state")
	}

	w.logger.Info("native torodb ocr: starting visual inspection", "doc_id", doc.ID, "file_name", doc.FileName)

	// Resolve presigned S3 URL
	docURL := doc.S3URL
	if (docURL == "" || !strings.HasPrefix(docURL, "http")) && len(doc.Metadata) > 0 {
		var meta map[string]any
		if err := json.Unmarshal(doc.Metadata, &meta); err == nil {
			if s3Key, ok := meta["s3_key"].(string); ok && s3Key != "" && w.s3 != nil {
				if presigned, err := w.s3.GeneratePresignedURL(ctx, s3Key, 24*time.Hour); err == nil {
					docURL = presigned
				}
			}
		}
	}

	userPrompt := fmt.Sprintf("Document Name: %s\nPlease perform visual OCR inspection on the attached document and extract structured JSON matching the system instructions.", doc.FileName)

	respText, err := w.rt.ExecDocumentURL(ctx, userPrompt, ocrSystemPrompt, docURL, doc.FileName)
	if err != nil {
		w.logger.Error("native torodb ocr: vision extraction failed", "doc_id", doc.ID, "error", err)
		if w.docStore != nil {
			_, _ = w.docStore.UpdateOCRStatus(ctx, doc.ID, "FAILED", map[string]any{"error": err.Error()}, "")
		}
		return nil, err
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
		w.logger.Warn("failed to parse structured JSON from LLM OCR, using fallback", "raw", respText, "error", err)
		parsed.DocType = "other"
		parsed.Data = map[string]interface{}{"raw_response": respText}
	}

	extraction := &OCRExtraction{
		DocType:       parsed.DocType,
		FileName:      doc.FileName,
		S3URL:         doc.S3URL,
		Data:          parsed.Data,
		ColumnMapping: parsed.ColumnMapping,
		Confidence:    parsed.Confidence,
		RawText:       respText,
	}

	// 2. Update toro_core.documents status to OCR_SUCCESS
	if w.docStore != nil {
		rawMap := map[string]any{
			"doc_type":       extraction.DocType,
			"confidence":     extraction.Confidence,
			"data":           extraction.Data,
			"column_mapping": extraction.ColumnMapping,
		}
		ocrDoc, err := w.docStore.UpdateOCRStatus(ctx, doc.ID, string(DocStatusOCRSuccess), rawMap, extraction.RawText)
		if err != nil {
			return nil, fmt.Errorf("document ocr worker: failed to update ocr status: %w", err)
		}

		// 3. Trigger IngestProcessedDocument into Layer 1 Facts, Layer 2 Edges, Layer 3 Vectors
		if w.kie != nil {
			if err := w.kie.IngestProcessedDocument(ctx, ocrDoc); err != nil {
				w.logger.Warn("document ocr worker: knowledge ingestion failed", "doc_id", doc.ID, "error", err)
			} else {
				_, _ = w.docStore.UpdateDocumentStatus(ctx, doc.ID, string(DocStatusEmbeddingsSuccess))
			}
		}
	}

	return extraction, nil
}
