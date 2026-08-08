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
		RealmID:       "realm_123",
		DocumentType:  DocTypeInvoice,
		FileName:      "invoice_1001.pdf",
		MimeType:      "application/pdf",
		S3URL:         "s3://vault/realm_123/invoice_1001.pdf",
		OCRStatus:     "PROCESSED",
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
	if doc.OCRStatus != "PROCESSED" {
		t.Fatalf("expected PROCESSED, got %s", doc.OCRStatus)
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
		t.Fatal("expected error when document is not in PROCESSED status, got nil")
	}
}
