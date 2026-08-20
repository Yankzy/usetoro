package knowledge_system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

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

// DocumentStatus represents the granular lifecycle status of a document.
type DocumentStatus string

const (
	DocStatusPending           DocumentStatus = "PENDING"
	DocStatusProcessing        DocumentStatus = "PROCESSING"
	DocStatusOCRSuccess        DocumentStatus = "OCR_SUCCESS"
	DocStatusEmbeddingsSuccess DocumentStatus = "EMBEDDINGS_SUCCESS"
	DocStatusFailed            DocumentStatus = "FAILED"
)

// CreateDocumentResult represents the outcome of document registration in toro_core.documents.
type CreateDocumentResult string

const (
	CreateResultCreated  CreateDocumentResult = "CREATED"
	CreateResultExisting CreateDocumentResult = "EXISTING"
)

// Document represents a row in the master documents table toro_core.documents.
type Document struct {
	ID            uuid.UUID       `json:"id"`
	SessionID     string          `json:"session_id"`
	DocumentType  DocumentType    `json:"document_type"`
	FileName      string          `json:"file_name"`
	MimeType      string          `json:"mime_type"`
	S3URL         string          `json:"s3_url"`
	SHA256        string          `json:"sha256,omitempty"`
	OCRStatus     string          `json:"ocr_status"`
	RawOCRJSON    json.RawMessage `json:"raw_ocr_json"`
	ExtractedText string          `json:"extracted_text"`
	SenderEmail   string          `json:"sender_email,omitempty"`
	SourceChannel string          `json:"source_channel"`
	Metadata      json.RawMessage `json:"metadata"`
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

// CreateOrGetDocument registers a new incoming document or returns an existing document if a duplicate SHA256 is detected.
func (ds *DocumentStore) CreateOrGetDocument(
	ctx context.Context,
	sessionID string,
	docType DocumentType,
	fileName, mimeType, s3URL, sha256Sum, sourceChannel, senderEmail string,
	metadata map[string]any,
) (*Document, CreateDocumentResult, error) {
	if sourceChannel == "" {
		sourceChannel = "EMAIL"
	}
	if docType == "" {
		docType = DocTypeOther
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	metaBytes, errMeta := json.Marshal(metadata)
	if errMeta != nil || len(metaBytes) == 0 {
		metaBytes = []byte("{}")
	}

	doc := &Document{
		SessionID:     sessionID,
		DocumentType:  docType,
		FileName:      fileName,
		MimeType:      mimeType,
		S3URL:         s3URL,
		SHA256:        sha256Sum,
		OCRStatus:     string(DocStatusPending),
		SourceChannel: sourceChannel,
		SenderEmail:   senderEmail,
		RawOCRJSON:    json.RawMessage("{}"),
		Metadata:      metaBytes,
	}

	query := `
		INSERT INTO toro_core.documents
			(session_id, document_type, file_name, mime_type, s3_url, sha256, ocr_status, source_channel, sender_email, metadata)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), 'PENDING', $7, $8, $9)
		ON CONFLICT (session_id, sha256) WHERE sha256 IS NOT NULL AND sha256 != '' DO NOTHING
		RETURNING id, ocr_status, created_at, updated_at`

	err := ds.pool.QueryRow(ctx, query,
		sessionID, string(docType), fileName, mimeType, s3URL, sha256Sum, sourceChannel, senderEmail, metaBytes,
	).Scan(&doc.ID, &doc.OCRStatus, &doc.CreatedAt, &doc.UpdatedAt)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) && sha256Sum != "" {
			// Duplicate SHA256 detected - row was NOT inserted or modified.
			// Fetch the existing unchanged document from ToroDB.
			existingDoc, fetchErr := ds.GetDocumentBySHA256(ctx, sha256Sum)
			if fetchErr != nil {
				return nil, "", fmt.Errorf("document store fetch existing document after conflict: %w", fetchErr)
			}
			if existingDoc != nil {
				return existingDoc, CreateResultExisting, nil
			}
		}
		return nil, "", fmt.Errorf("document store create or get document: %w", err)
	}

	return doc, CreateResultCreated, nil
}

// CreateDocument registers a new incoming document record with optional metadata.
func (ds *DocumentStore) CreateDocument(
	ctx context.Context,
	sessionID string,
	docType DocumentType,
	fileName, mimeType, s3URL, sha256Sum, sourceChannel, senderEmail string,
	metadata map[string]any,
) (*Document, error) {
	doc, _, err := ds.CreateOrGetDocument(ctx, sessionID, docType, fileName, mimeType, s3URL, sha256Sum, sourceChannel, senderEmail, metadata)
	return doc, err
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
		RETURNING id, session_id, document_type, file_name, mime_type, s3_url, COALESCE(sha256, ''), ocr_status,
		          raw_ocr_json, COALESCE(extracted_text, ''), COALESCE(sender_email, ''), source_channel, metadata, processed_at, created_at, updated_at`,
		id, status, rawBytes, extractedText,
	).Scan(
		&doc.ID, &doc.SessionID, &doc.DocumentType, &doc.FileName, &doc.MimeType, &doc.S3URL, &doc.SHA256,
		&doc.OCRStatus, &doc.RawOCRJSON, &doc.ExtractedText, &doc.SenderEmail, &doc.SourceChannel,
		&doc.Metadata, &doc.ProcessedAt, &doc.CreatedAt, &doc.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("document store update ocr status: %w", err)
	}

	return doc, nil
}

// UpdateDocumentStatus updates only the ocr_status field of a document.
func (ds *DocumentStore) UpdateDocumentStatus(
	ctx context.Context,
	id uuid.UUID,
	status string,
) (*Document, error) {
	doc := &Document{}
	err := ds.pool.QueryRow(ctx, `
		UPDATE toro_core.documents
		SET ocr_status = $2,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING id, session_id, document_type, file_name, mime_type, s3_url, COALESCE(sha256, ''), ocr_status,
		          raw_ocr_json, COALESCE(extracted_text, ''), COALESCE(sender_email, ''), source_channel, metadata, processed_at, created_at, updated_at`,
		id, status,
	).Scan(
		&doc.ID, &doc.SessionID, &doc.DocumentType, &doc.FileName, &doc.MimeType, &doc.S3URL, &doc.SHA256,
		&doc.OCRStatus, &doc.RawOCRJSON, &doc.ExtractedText, &doc.SenderEmail, &doc.SourceChannel,
		&doc.Metadata, &doc.ProcessedAt, &doc.CreatedAt, &doc.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("document store update document status: %w", err)
	}

	return doc, nil
}

// GetDocumentByID retrieves a document by UUID.
func (ds *DocumentStore) GetDocumentByID(ctx context.Context, id uuid.UUID) (*Document, error) {
	doc := &Document{}
	err := ds.pool.QueryRow(ctx, `
		SELECT id, session_id, document_type, file_name, mime_type, s3_url, COALESCE(sha256, ''), ocr_status,
		       raw_ocr_json, COALESCE(extracted_text, ''), COALESCE(sender_email, ''), source_channel, metadata, processed_at, created_at, updated_at
		FROM toro_core.documents
		WHERE id = $1`, id,
	).Scan(
		&doc.ID, &doc.SessionID, &doc.DocumentType, &doc.FileName, &doc.MimeType, &doc.S3URL, &doc.SHA256,
		&doc.OCRStatus, &doc.RawOCRJSON, &doc.ExtractedText, &doc.SenderEmail, &doc.SourceChannel,
		&doc.Metadata, &doc.ProcessedAt, &doc.CreatedAt, &doc.UpdatedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("document store get document by id: %w", err)
	}

	return doc, nil
}

// GetDocumentBySHA256 retrieves a document by its unique sha256 fingerprint within a session ID.
func (ds *DocumentStore) GetDocumentBySHA256(ctx context.Context, sha256Str string) (*Document, error) {
	doc := &Document{}
	err := ds.pool.QueryRow(ctx, `
		SELECT id, session_id, document_type, file_name, mime_type, s3_url, COALESCE(sha256, ''), ocr_status,
		       raw_ocr_json, COALESCE(extracted_text, ''), COALESCE(sender_email, ''), source_channel, metadata, processed_at, created_at, updated_at
		FROM toro_core.documents
		WHERE sha256 = $1 LIMIT 1`, sha256Str,
	).Scan(
		&doc.ID, &doc.SessionID, &doc.DocumentType, &doc.FileName, &doc.MimeType, &doc.S3URL, &doc.SHA256,
		&doc.OCRStatus, &doc.RawOCRJSON, &doc.ExtractedText, &doc.SenderEmail, &doc.SourceChannel,
		&doc.Metadata, &doc.ProcessedAt, &doc.CreatedAt, &doc.UpdatedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("document store get document by sha256: %w", err)
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

// IngestProcessedDocument ingests an OCR_SUCCESS document into Layer 1, Layer 2, and Layer 3.
func (kie *KnowledgeIngestionEngine) IngestProcessedDocument(ctx context.Context, doc *Document) error {
	if doc == nil || (doc.OCRStatus != string(DocStatusOCRSuccess) && doc.OCRStatus != string(DocStatusEmbeddingsSuccess)) {
		return fmt.Errorf("knowledge ingestion engine: document is not in OCR_SUCCESS or EMBEDDINGS_SUCCESS status")
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

	docFact, err := kie.graphStore.CreateFact(ctx, doc.SessionID, string(doc.DocumentType), string(doc.DocumentType), docURI, docPayload)
	if err != nil {
		return fmt.Errorf("knowledge ingestion: failed to create L1 document fact: %w", err)
	}

	// 2. Extract Entity Counterparty (Supplier/Customer) Fact and Layer 2 Directional Edge if present
	if vendorName, ok := ocrMap["vendor_name"].(string); ok && strings.TrimSpace(vendorName) != "" {
		vendorName = strings.TrimSpace(vendorName)
		var slugBuf strings.Builder
		for _, r := range strings.ToLower(vendorName) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				slugBuf.WriteRune(r)
			} else if r == ' ' || r == '-' || r == '_' {
				slugBuf.WriteRune('-')
			}
		}
		normalizedVendor := strings.Trim(slugBuf.String(), "-")
		if normalizedVendor == "" {
			normalizedVendor = "unknown"
		}
		vendorURI := fmt.Sprintf("fact:entity:vendor:%s:%s", doc.SessionID, normalizedVendor)
		vendorPayload := map[string]any{
			"name": vendorName,
			"type": "VENDOR",
		}
		vendorFact, err := kie.graphStore.CreateFact(ctx, doc.SessionID, "entities", "vendor", vendorURI, vendorPayload)
		if err == nil && vendorFact != nil {
			// Link Layer 2 Directional Relationship Edge: Document --ISSUED_BY--> Vendor
			_, _ = kie.graphStore.CreateRelationship(ctx, doc.SessionID, string(doc.DocumentType), docFact.FactID, vendorFact.FactID, "ISSUED_BY", 1.0)
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
			doc.SessionID,
			string(doc.DocumentType),
			ase.VectorSourceDocument,
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

// DispatchOCR publishes a document OCR extraction task to the native Go OCR agent subject ("tasks.perception.1.ocr").
func (kie *KnowledgeIngestionEngine) DispatchOCR(ctx context.Context, nc *nats.Conn, doc *Document, taskPayload map[string]any) error {
	if nc == nil {
		return fmt.Errorf("knowledge ingestion engine dispatch ocr: nats connection is nil")
	}
	if doc == nil {
		return fmt.Errorf("knowledge ingestion engine dispatch ocr: document is nil")
	}

	if taskPayload == nil {
		taskPayload = make(map[string]any)
	}

	taskPayload["document_id"] = doc.ID.String()
	taskPayload["document_type"] = string(doc.DocumentType)
	taskPayload["s3_url"] = doc.S3URL
	taskPayload["file_name"] = doc.FileName
	if doc.S3URL != "" && taskPayload["document_url"] == nil {
		taskPayload["document_url"] = doc.S3URL
	}
	if taskPayload["final_destination_subject"] == nil {
		taskPayload["final_destination_subject"] = "events.accounting.1.pcm_bookkeeping"
	}

	payloadBytes, err := json.Marshal(taskPayload)
	if err != nil {
		return fmt.Errorf("knowledge ingestion engine dispatch ocr: marshal payload: %w", err)
	}

	kie.logger.Info("knowledge ingestion: DispatchOCR payload", "payload", string(payloadBytes))

	ocrSubject := "tasks.perception.1.ocr"
	if targetSub, ok := taskPayload["ocr_target_subject"].(string); ok && targetSub != "" {
		ocrSubject = targetSub
	}

	kie.logger.Info("knowledge ingestion engine: dispatching document task to native Go OCR agent",
		"doc_id", doc.ID,
		"file_name", doc.FileName,
		"ocr_subject", ocrSubject,
		"final_destination_subject", taskPayload["final_destination_subject"],
	)

	if err := nc.Publish(ocrSubject, payloadBytes); err != nil {
		return fmt.Errorf("knowledge ingestion engine dispatch ocr: publish nats: %w", err)
	}

	return nil
}
