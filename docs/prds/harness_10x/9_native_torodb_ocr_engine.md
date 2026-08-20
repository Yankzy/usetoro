# PRD 9: Native ToroDB OCR Engine & Agent Refactoring

**Document Location:** `docs/prds/harness_10x/9_native_torodb_ocr_engine.md`  
**Package:** `github.com/Yankzy/usetoro/internal/erp/ase/knowledge_system` & `github.com/Yankzy/usetoro/tap/agents/ocr_agent`  
**Status:** Implemented 
**Target Architecture:** Native ToroDB Container In-Process OCR Engine (Replacing External Python Sidecar)

---

## 1. Executive Summary & Problem Statement

### 1.1 Current Limitations
Currently, visual document OCR is delegated to an external Python sidecar service (`python-worker/app/openai/ocr.py`) listening on NATS topic `worker.inbox.python.ocr`. Meanwhile, the Go OCR agent (`tap/agents/ocr_agent/agent.go`) relies on experimental TAP Redux state wrappers.

This external sidecar approach creates operational friction:
* **Sidecar Dependency**: Running document perception outside ToroDB requires an extra container (`python-worker`), network RPCs over NATS, and dual-language maintenance.
* **Asymmetric Architecture**: While ToroDB generates vector embeddings natively in-process via `VectorHydrator` ([vector_hydrator.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/vector_hydrator.go)), document OCR perception remains outside the database container.

### 1.2 The Target Native Solution
This PRD specifies the **Native ToroDB OCR Engine**. Visual document perception is refactored into a high-performance Go worker (`DocumentOCRWorker`) built directly into ToroDB's Knowledge System (`go/internal/erp/ase/knowledge_system/`).

Like vector embedding generation, **OCR perception becomes a native database capability**:
1. Documents are saved to `toro_core.documents` with `ocr_status = 'PENDING'`.
2. Native ToroDB `DocumentOCRWorker` (or CDC event listener) detects the `PENDING` record.
3. The native Go OCR engine fetches document bytes, executes visual LLM extraction via Go OpenAI Vision API, writes `raw_ocr_json` to `toro_core.documents` (`ocr_status = 'PROCESSED'`), and triggers `KnowledgeIngestionEngine.IngestProcessedDocument` to populate L1 Facts, L2 Graph Edges, and L3 Vector Memory.

---

## 2. Core Philosophy & System Parity

```
+-----------------------------------------------------------------------------------------------+
|                                    ToroDB Container Image                                     |
|                                (container/torodb/Dockerfile)                                  |
|                                                                                               |
|  +-----------------------------------------------------------------------------------------+  |
|  |                 Go Execution Harness Binary (/usr/local/bin/torodb)                     |  |
|  |                             (go/cmd/torodb/main.go)                                     |  |
|  |                                                                                         |  |
|  |  +-----------------------------------------------------------------------------------+  |  |
|  |  |           Knowledge System Engine (go/internal/erp/ase/knowledge_system/)        |  |  |
|  |  |                                                                                   |  |  |
|  |  |   [GraphStore]             [DocumentStore]         [KnowledgeIngestionEngine]     |  |  |
|  |  |   (L1 Facts & L2 Edges)   (toro_core.documents)    (OCR -> L1/L2/L3 pipeline)      |  |  |
|  |  |                                                                                   |  |  |
|  |  |   [DocumentOCRWorker]      [VectorStore]           [VectorHydrator]               |  |  |
|  |  |   (Native Vision OCR)      (ScaNN search)          (Async embedding background)   |  |  |
|  |  +-----------------------------------------------------------------------------------+  |  |
|  +-----------------------------------------------------------------------------------------+  |
|                                              │                                                |
|                                              ▼ Unix Socket / Localhost 5432                   |
|  +-----------------------------------------------------------------------------------------+  |
|  |                         Google AlloyDB Omni Engine (PostgreSQL 17)                      |  |
|  |   Master Documents Store              (toro_core.documents)                             |  |
|  |   Layer 1: Ground Truth Facts         (toro_core.enterprise_facts)                      |  |
|  |   Layer 2: Directional Entity Graph   (toro_core.enterprise_relationships)              |  |
|  |   Layer 3: Situation Vector Memory    (toro_core.ase_vector_memory + ScaNN index)       |  |
|  +-----------------------------------------------------------------------------------------+  |
+-----------------------------------------------------------------------------------------------+
```

---

## 3. Functional Specification & Parity Requirements

The native Go OCR engine must replicate 100% of the extraction capabilities currently implemented in `python-worker/app/openai/ocr.py` and `tap/agents/ocr_agent/agent.go`.

### 3.1 Document Extraction Categories

#### 1. Bank Statements (`doc_type: "bank_statement"`)
Must extract:
* `bank_name` (string)
* `account_number` (string)
* `statement_date` (string)
* `period` (string)
* `starting_balance` (float64)
* `ending_balance` (float64)
* `transactions` (array of objects: `date`, `description`, `amount`, `type`: `"debit"` | `"credit"`)
* `column_mapping` (structural metadata object):
  ```json
  {
    "date_col_idx": 0,
    "description_col_idx": 1,
    "amount_col_idx": 2,
    "is_split_amount": false,
    "debit_col_idx": null,
    "credit_col_idx": null,
    "vendor_col_idx": null,
    "customer_col_idx": null,
    "confidence_score": 0.98,
    "is_ambiguous": false,
    "ambiguity_reason": null,
    "polarity_sign": "minus",
    "source_account": "Attijariwafa Bank"
  }
  ```

#### 2. Invoices (`doc_type: "invoice"`)
Must extract:
* `vendor_name` (string)
* `invoice_number` (string)
* `date` (string)
* `total_mad` (float64)
* `ht_mad` (float64)
* `tva_mad` (float64)
* `line_items` (array of objects: `description`, `quantity`, `unit_price`, `total`)

#### 3. Receipts (`doc_type: "receipt"`)
Must extract:
* `vendor_name` (string)
* `date` (string)
* `total_mad` (float64)
* `payment_method` (string)

#### 4. General / Other (`doc_type: "other"`)
Must extract key-value metadata and full unstructured text block.

### 3.2 Presigned S3 URL Direct Vision Ingestion (`file_url`)
Matching line 101 of `python-worker/app/openai/ocr.py` and lines 330-337 of `postmark_inbound_email.go`, `PostmarkInboundEmailWorker` uploads document attachments to AWS S3 and generates a cryptographically signed public S3 URL via `infra.S3Service.GeneratePresignedURL(ctx, s3Key, 24*time.Hour)`.

The native Go OCR engine passes this presigned S3 HTTPS URL directly to OpenAI as an `"input_file"` / `"image_url"` payload:

```json
{
  "role": "user",
  "content": [
    { "type": "input_text", "text": "<system_ocr_prompt>" },
    {
      "type": "input_file",
      "file_url": "https://toro-vault.s3.amazonaws.com/statement.pdf?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=...&X-Amz-Signature=..."
    }
  ]
}
```

* **Presigning Resilience**: `DocumentOCRWorker` checks document metadata for `s3_key`. If the presigned URL is expired or unavailable, it invokes `infra.S3Service.GeneratePresignedURL(ctx, s3Key, 24*time.Hour)` to re-sign the S3 URL before executing OpenAI Vision API calls.
* **Zero Memory Overhead**: Presigned S3 URLs allow OpenAI to fetch the document directly from S3, eliminating local byte buffer downloads and base64 string allocations inside ToroDB.

---

## 4. TAP Agent Runtime Cleanup (`tap/pkg/agent/runtime.go`)

### 4.1 Root Cause Analysis of Legacy Failure
The legacy implementation of `ExecDocument` in `tap/pkg/agent/runtime.go` relied on a naive regex string parser (`extractTextFromPDF`) that attempted to extract plain text from binary PDF bytes by searching for ASCII parentheses `(` and `)`. 

This produced corrupt, truncated outputs on complex PDFs and bank statements, breaking visual perception and forcing fallback to the Python sidecar.

### 4.2 Required Runtime Enhancements
To restore clean, native Go execution, `tap/pkg/agent/runtime.go` must be cleaned up as follows:

1. **Deprecate & Remove `extractTextFromPDF`**: Delete fragile raw byte parsing routines from `runtime.go`.
2. **Add `ExecDocumentURL` Method**: Add a clean, first-class `ExecDocumentURL` method to `Runtime`:
   ```go
   // ExecDocumentURL passes a presigned HTTPS URL directly to OpenAI using the input_file / image_url paradigm.
   func (r *Runtime) ExecDocumentURL(ctx context.Context, prompt string, systemPrompt string, fileURL string, fileName string) (string, error)
   ```
3. **OpenAI Responses API Integration**: In `ExecDocumentURL`, construct the input payload using `file_url` directly matching OpenAI Responses API:
   ```go
   // ParadigmResponses execution
   resp, err := client.Responses.New(ctx, responses.ResponseNewParams{
       Input: responses.ResponseNewParamsInputUnion{
           OfInputItemList: []responses.ResponseInputItemParam{
               {
                   Role: "user",
                   Content: []responses.ResponseContentPartParam{
                       {Type: "input_text", Text: prompt},
                       {Type: "input_file", FileURL: fileURL},
                   },
               },
           },
       },
       Model: shared.ChatModel(r.effectiveModel(ctx)),
   })
   ```

---

## 5. Technical Go Interface & Implementation

### 4.1 Struct Definitions (`go/internal/erp/ase/knowledge_system/document_ocr_worker.go`)

```go
package knowledge_system

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	csvmapping "github.com/Yankzy/usetoro/tap/agents/csv_mapping"
)

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
	logger   *slog.Logger
	mu       sync.Mutex
}

// NewDocumentOCRWorker instantiates a native ToroDB DocumentOCRWorker.
func NewDocumentOCRWorker(
	docStore *DocumentStore,
	kie *KnowledgeIngestionEngine,
	pool *pgxpool.Pool,
	nc *nats.Conn,
	logger *slog.Logger,
) *DocumentOCRWorker {
	if logger == nil {
		logger = slog.Default()
	}
	return &DocumentOCRWorker{
		docStore: docStore,
		kie:      kie,
		pool:     pool,
		nc:       nc,
		logger:   logger.With("component", "torodb.document_ocr_worker"),
	}
}

// ProcessPendingDocument performs native visual LLM inspection, updates toro_core.documents
// to 'PROCESSED', and triggers L1/L2/L3 fact ingestion.
func (w *DocumentOCRWorker) ProcessPendingDocument(ctx context.Context, doc *Document) (*OCRExtraction, error) {
	if doc == nil || doc.OCRStatus != "PENDING" {
		return nil, fmt.Errorf("document ocr worker: document is nil or not in PENDING state")
	}

	w.logger.Info("native torodb ocr: starting visual inspection", "doc_id", doc.ID, "file_name", doc.FileName)

	// 1. Execute LLM Vision Extraction
	extraction, err := w.extractDocumentUsingLLM(ctx, doc)
	if err != nil {
		w.logger.Error("native torodb ocr: vision extraction failed", "doc_id", doc.ID, "error", err)
		_, _ = w.docStore.UpdateOCRStatus(ctx, doc.ID, "FAILED", map[string]any{"error": err.Error()}, "")
		return nil, err
	}

	// 2. Update toro_core.documents status to PROCESSED
	rawMap := map[string]any{
		"doc_type":       extraction.DocType,
		"confidence":     extraction.Confidence,
		"data":           extraction.Data,
		"column_mapping": extraction.ColumnMapping,
	}
	processedDoc, err := w.docStore.UpdateOCRStatus(ctx, doc.ID, "PROCESSED", rawMap, extraction.RawText)
	if err != nil {
		return nil, fmt.Errorf("document ocr worker: failed to update ocr status: %w", err)
	}

	// 3. Trigger IngestProcessedDocument into Layer 1 Facts, Layer 2 Edges, Layer 3 Vectors
	if w.kie != nil {
		if err := w.kie.IngestProcessedDocument(ctx, processedDoc); err != nil {
			w.logger.Warn("document ocr worker: knowledge ingestion failed", "doc_id", doc.ID, "error", err)
		}
	}

	return extraction, nil
}
```

---

## 5. Migration & Deprecation Plan

1. **Refactor `tap/agents/ocr_agent/agent.go`**:
   - Update `ocr_agent` to use native ToroDB `DocumentOCRWorker` Go engine.
2. **Deprecate `python-worker/app/openai/ocr.py`**:
   - Remove Python NATS subscription on `worker.inbox.python.ocr`.
   - Update NATS routing to target native Go ToroDB OCR worker subject (`worker.inbox.ocr`).
3. **Container Packaging (`container/torodb/Dockerfile`)**:
   - Include native Go OCR worker inside `/usr/local/bin/torodb` binary.

---

## 6. Verification Criteria

* **Unit Tests**: Executed via `go test ./internal/erp/ase/knowledge_system/...` testing `ProcessPendingDocument` state transitions and LLM mock extractions.
* **Parity Benchmark**: Verify that bank statement column mapping outputs match exact JSON structures produced by `python-worker/app/openai/ocr.py`.
