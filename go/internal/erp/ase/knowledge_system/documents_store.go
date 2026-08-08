package knowledge_system

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Yankzy/usetoro/internal/erp/ase"
)

// DocumentType represents the category of ground-truth master document.
type DocumentType string

const (
	DocTypeInvoice       DocumentType = "INVOICE"
	DocTypeReceipt       DocumentType = "RECEIPT"
	DocTypeBankStatement DocumentType = "BANK_STATEMENT"
	DocTypeBill          DocumentType = "BILL"
	DocTypeTaxForm       DocumentType = "TAX_FORM"
	DocTypeOther         DocumentType = "OTHER"
)

// Document represents a row in the master documents table toro_core.documents.
type Document struct {
	ID            uuid.UUID       `json:"id"`
	RealmID       string          `json:"realm_id"`
	DocumentType  DocumentType    `json:"document_type"`
	FileName      string          `json:"file_name"`
	MimeType      string          `json:"mime_type"`
	S3URL         string          `json:"s3_url"`
	OCRStatus     string          `json:"ocr_status"`
	RawOCRJSON    json.RawMessage `json:"raw_ocr_json"`
	ExtractedText string          `json:"extracted_text"`
	SenderEmail   string          `json:"sender_email,omitempty"`
	SourceChannel string          `json:"source_channel"`
	ProcessedAt   *time.Time      `json:"processed_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// DocumentStore handles CRUD operations for the toro_core.documents table.
type DocumentStore struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// NewDocumentStore creates a new DocumentStore.
func NewDocumentStore(pool *pgxpool.Pool, logger *slog.Logger) *DocumentStore {
	if logger == nil {
		logger = slog.Default()
	}
	return &DocumentStore{
		pool:   pool,
		logger: logger.With("component", "knowledge_system.documents_store"),
	}
}

// CreateDocument registers a new incoming document record.
func (ds *DocumentStore) CreateDocument(
	ctx context.Context,
	realmID string,
	docType DocumentType,
	fileName, mimeType, s3URL, sourceChannel, senderEmail string,
) (*Document, error) {
	if sourceChannel == "" {
		sourceChannel = "EMAIL"
	}
	doc := &Document{
		RealmID:       realmID,
		DocumentType:  docType,
		FileName:      fileName,
		MimeType:      mimeType,
		S3URL:         s3URL,
		OCRStatus:     "PENDING",
		SourceChannel: sourceChannel,
		SenderEmail:   senderEmail,
		RawOCRJSON:    json.RawMessage("{}"),
	}

	err := ds.pool.QueryRow(ctx, `
		INSERT INTO toro_core.documents
			(realm_id, document_type, file_name, mime_type, s3_url, ocr_status, source_channel, sender_email)
		VALUES ($1, $2, $3, $4, $5, 'PENDING', $6, $7)
		RETURNING id, created_at, updated_at`,
		realmID, string(docType), fileName, mimeType, s3URL, sourceChannel, senderEmail,
	).Scan(&doc.ID, &doc.CreatedAt, &doc.UpdatedAt)

	if err != nil {
		return nil, fmt.Errorf("document store create document: %w", err)
	}

	return doc, nil
}

// UpdateOCRStatus updates document OCR extraction state and payload.
func (ds *DocumentStore) UpdateOCRStatus(
	ctx context.Context,
	id uuid.UUID,
	status string,
	rawOCRPayload map[string]any,
	extractedText string,
) (*Document, error) {
	rawBytes, err := json.Marshal(rawOCRPayload)
	if err != nil {
		return nil, fmt.Errorf("document store update ocr status: marshal json: %w", err)
	}

	doc := &Document{}
	err = ds.pool.QueryRow(ctx, `
		UPDATE toro_core.documents
		SET ocr_status     = $2,
		    raw_ocr_json   = $3,
		    extracted_text = $4,
		    processed_at   = NOW(),
		    updated_at     = NOW()
		WHERE id = $1
		RETURNING id, realm_id, document_type, file_name, mime_type, s3_url, ocr_status,
		          raw_ocr_json, extracted_text, sender_email, source_channel, processed_at, created_at, updated_at`,
		id, status, rawBytes, extractedText,
	).Scan(
		&doc.ID, &doc.RealmID, &doc.DocumentType, &doc.FileName, &doc.MimeType, &doc.S3URL,
		&doc.OCRStatus, &doc.RawOCRJSON, &doc.ExtractedText, &doc.SenderEmail, &doc.SourceChannel,
		&doc.ProcessedAt, &doc.CreatedAt, &doc.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("document store update ocr status: %w", err)
	}

	return doc, nil
}

// GetDocumentByID retrieves a document by UUID.
func (ds *DocumentStore) GetDocumentByID(ctx context.Context, id uuid.UUID) (*Document, error) {
	doc := &Document{}
	err := ds.pool.QueryRow(ctx, `
		SELECT id, realm_id, document_type, file_name, mime_type, s3_url, ocr_status,
		       raw_ocr_json, extracted_text, sender_email, source_channel, processed_at, created_at, updated_at
		FROM toro_core.documents
		WHERE id = $1`, id,
	).Scan(
		&doc.ID, &doc.RealmID, &doc.DocumentType, &doc.FileName, &doc.MimeType, &doc.S3URL,
		&doc.OCRStatus, &doc.RawOCRJSON, &doc.ExtractedText, &doc.SenderEmail, &doc.SourceChannel,
		&doc.ProcessedAt, &doc.CreatedAt, &doc.UpdatedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("document store get document by id: %w", err)
	}

	return doc, nil
}

// KnowledgeIngestionEngine unifies Document processing into Layer 1 Facts, Layer 2 Graph Edges, and Layer 3 Vector Memory.
type KnowledgeIngestionEngine struct {
	docStore    *DocumentStore
	graphStore  *GraphStore
	vectorStore *ase.VectorStore
	logger      *slog.Logger
}

// NewKnowledgeIngestionEngine creates a new KnowledgeIngestionEngine.
func NewKnowledgeIngestionEngine(
	docStore *DocumentStore,
	graphStore *GraphStore,
	vectorStore *ase.VectorStore,
	logger *slog.Logger,
) *KnowledgeIngestionEngine {
	if logger == nil {
		logger = slog.Default()
	}
	return &KnowledgeIngestionEngine{
		docStore:    docStore,
		graphStore:  graphStore,
		vectorStore: vectorStore,
		logger:      logger.With("component", "knowledge_system.ingestion_engine"),
	}
}

// IngestProcessedDocument ingests a PROCESSED OCR document into Layer 1, Layer 2, and Layer 3.
func (kie *KnowledgeIngestionEngine) IngestProcessedDocument(ctx context.Context, doc *Document) error {
	if doc == nil || doc.OCRStatus != "PROCESSED" {
		return fmt.Errorf("knowledge ingestion engine: document is not in PROCESSED status")
	}

	// 1. Create Layer 1 Ground Truth Fact Node for the Document
	docURI := fmt.Sprintf("fact:document:%s:%s", doc.DocumentType, doc.ID.String())
	docPayload := map[string]any{
		"document_id":    doc.ID.String(),
		"document_type":  string(doc.DocumentType),
		"file_name":      doc.FileName,
		"s3_url":         doc.S3URL,
		"source_channel": doc.SourceChannel,
	}

	// Extract vendor/counterparty info if available in raw OCR JSON
	var ocrMap map[string]any
	if len(doc.RawOCRJSON) > 0 {
		_ = json.Unmarshal(doc.RawOCRJSON, &ocrMap)
	}

	docFact, err := kie.graphStore.CreateFact(ctx, doc.RealmID, string(doc.DocumentType), string(doc.DocumentType), docURI, docPayload)
	if err != nil {
		return fmt.Errorf("knowledge ingestion: failed to create L1 document fact: %w", err)
	}

	// 2. Extract Entity Counterparty (Supplier/Customer) Fact and Layer 2 Directional Edge if present
	if vendorName, ok := ocrMap["vendor_name"].(string); ok && vendorName != "" {
		vendorURI := fmt.Sprintf("fact:entity:vendor:%s", vendorName)
		vendorPayload := map[string]any{
			"name": vendorName,
			"type": "VENDOR",
		}
		vendorFact, err := kie.graphStore.CreateFact(ctx, doc.RealmID, "entities", "vendor", vendorURI, vendorPayload)
		if err == nil && vendorFact != nil {
			// Link Layer 2 Directional Relationship Edge: Document --ISSUED_BY--> Vendor
			_, _ = kie.graphStore.CreateRelationship(ctx, doc.RealmID, string(doc.DocumentType), docFact.FactID, vendorFact.FactID, "ISSUED_BY", 1.0)
		}
	}

	// 3. Register Layer 3 Situation Vector Memory (passing nil embedding registers as pending hydration)
	if kie.vectorStore != nil && doc.ExtractedText != "" {
		vecMeta := map[string]any{
			"document_id":   doc.ID.String(),
			"document_type": string(doc.DocumentType),
			"file_name":     doc.FileName,
			"s3_url":        doc.S3URL,
		}
		err = kie.vectorStore.Upsert(
			ctx,
			doc.RealmID,
			string(doc.DocumentType),
			ase.VectorSourceResolvedTx,
			doc.ExtractedText,
			doc.ID,
			nil, // Pending hydration by VectorHydrator
			vecMeta,
		)
		if err != nil {
			kie.logger.Warn("knowledge ingestion: failed to queue L3 vector memory", "doc_id", doc.ID, "error", err)
		}
	}

	kie.logger.Info("knowledge ingestion: document successfully ingested into L1/L2/L3", "doc_id", doc.ID, "type", doc.DocumentType)
	return nil
}
