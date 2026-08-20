package enrichment

import (
	"context"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DocumentMock represents a verified document in memory or DB.
type DocumentRecord struct {
	ID            uuid.UUID
	SessionID     string
	DocumentType  string
	FileName      string
	InvoiceNumber string
	SupplierICE   string
	SupplierName  string
	TotalTTC      float64
	TotalHT       float64
	TotalTVA      float64
	InvoiceDate   time.Time
	S3URL         string
}

// DocumentCorrelator correlates transactions with uploaded invoices/receipts.
type DocumentCorrelator struct {
	mu        sync.RWMutex
	documents map[string][]*DocumentRecord // sessionId -> slice of docs
}

// NewDocumentCorrelator initializes the correlator.
func NewDocumentCorrelator() *DocumentCorrelator {
	return &DocumentCorrelator{
		documents: make(map[string][]*DocumentRecord),
	}
}

// RegisterDocument adds a document to the index.
func (dc *DocumentCorrelator) RegisterDocument(doc *DocumentRecord) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.documents[doc.SessionID] = append(dc.documents[doc.SessionID], doc)
}

// Correlate searches for matching documents based on amount, date window, and counterparty.
func (dc *DocumentCorrelator) Correlate(
	ctx context.Context,
	sessionID string,
	amountMAD float64,
	txnDate time.Time,
	supplierICE string,
	supplierStem string,
) DocumentEvidenceInfo {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	docs, ok := dc.documents[sessionID]
	if !ok || len(docs) == 0 {
		return DocumentEvidenceInfo{
			HasReceipt:    false,
			ReceiptStatus: ReceiptPendingAttachment,
		}
	}

	normStem := strings.ToUpper(supplierStem)

	for _, doc := range docs {
		// Amount tolerance: +- 0.05 MAD
		amountDiff := math.Abs(doc.TotalTTC - amountMAD)
		if amountDiff > 0.05 {
			continue
		}

		// Date window: +- 7 calendar days
		dayDiff := math.Abs(doc.InvoiceDate.Sub(txnDate).Hours() / 24.0)
		if dayDiff > 7.0 {
			continue
		}

		// ICE or stem match
		iceMatch := (supplierICE != "" && doc.SupplierICE == supplierICE)
		stemMatch := (supplierStem != "" && strings.Contains(strings.ToUpper(doc.SupplierName), normStem))

		if iceMatch || stemMatch {
			docIDStr := doc.ID.String()
			return DocumentEvidenceInfo{
				HasReceipt:             true,
				MatchedDocumentID:      &docIDStr,
				ReceiptStatus:          ReceiptMatchedHighConfidence,
				ExtractedInvoiceNumber: doc.InvoiceNumber,
				ReceiptURL:             doc.S3URL,
				ConfidenceScore:        0.98,
			}
		}
	}

	return DocumentEvidenceInfo{
		HasReceipt:    false,
		ReceiptStatus: ReceiptPendingAttachment,
	}
}
