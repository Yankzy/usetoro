package knowledge_system

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDocument_StructFields(t *testing.T) {
	docID := uuid.New()
	now := time.Now().UTC()

	doc := &Document{
		ID:            docID,
		SessionID:     "session_123",
		DocumentType:  DocTypeInvoice,
		FileName:      "invoice_1001.pdf",
		MimeType:      "application/pdf",
		S3URL:         "s3://vault/realm_123/invoice_1001.pdf",
		OCRStatus:     "OCR_SUCCESS",
		RawOCRJSON:    json.RawMessage(`{"vendor_name": "Acme Corp", "total": 250.00}`),
		ExtractedText: "Invoice 1001 Acme Corp Total: $250.00",
		SourceChannel: "EMAIL",
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if doc.ID != docID {
		t.Fatalf("expected docID %v, got %v", docID, doc.ID)
	}
	if doc.DocumentType != DocTypeInvoice {
		t.Fatalf("expected INVOICE, got %s", doc.DocumentType)
	}
	if doc.OCRStatus != "OCR_SUCCESS" {
		t.Fatalf("expected OCR_SUCCESS, got %s", doc.OCRStatus)
	}
}

func TestKnowledgeIngestionEngine_NilValidation(t *testing.T) {
	kie := NewKnowledgeIngestionEngine(nil, nil, nil, nil)
	err := kie.IngestProcessedDocument(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when ingesting nil document, got nil")
	}

	doc := &Document{
		ID:        uuid.New(),
		OCRStatus: "PENDING",
	}
	err = kie.IngestProcessedDocument(context.Background(), doc)
	if err == nil {
		t.Fatal("expected error when document is not in OCR_SUCCESS or EMBEDDINGS_SUCCESS status, got nil")
	}
}

func TestKnowledgeIngestionEngine_DispatchOCR_NilValidation(t *testing.T) {
	kie := NewKnowledgeIngestionEngine(nil, nil, nil, nil)
	err := kie.DispatchOCR(context.Background(), nil, nil, nil)
	if err == nil {
		t.Fatal("expected error when NATS connection is nil, got nil")
	}

	doc := &Document{
		ID:           uuid.New(),
		DocumentType: DocTypeBankStatement,
		S3URL:        "s3://vault/bank_statement.pdf",
		FileName:     "bank_statement.pdf",
	}
	err = kie.DispatchOCR(context.Background(), nil, doc, nil)
	if err == nil {
		t.Fatal("expected error when NATS connection is nil for valid doc, got nil")
	}
}

func TestDocument_MetadataField(t *testing.T) {
	metaMap := map[string]any{
		"callback_topic": "events.accounting.1.pcm_bookkeeping",
		"session_id":     "session_abc_123",
	}
	metaBytes, err := json.Marshal(metaMap)
	if err != nil {
		t.Fatalf("failed to marshal metadata map: %v", err)
	}

	doc := &Document{
		ID:           uuid.New(),
		SessionID:    "session_rap_pcm",
		DocumentType: DocTypeBankStatement,
		FileName:     "statement.pdf",
		MimeType:     "application/pdf",
		S3URL:        "s3://vault/statement.pdf",
		OCRStatus:    "PENDING",
		Metadata:     metaBytes,
	}

	if len(doc.Metadata) == 0 {
		t.Fatal("expected metadata to be non-empty")
	}
	var resMap map[string]any
	if err := json.Unmarshal(doc.Metadata, &resMap); err != nil {
		t.Fatalf("failed to unmarshal metadata: %v", err)
	}
	if resMap["callback_topic"] != "events.accounting.1.pcm_bookkeeping" {
		t.Fatalf("expected callback_topic events.accounting.1.pcm_bookkeeping, got %v", resMap["callback_topic"])
	}
}
