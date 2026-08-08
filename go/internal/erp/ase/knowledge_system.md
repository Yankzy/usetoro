# Knowledge System (Epistemology)

**Location:** `go/internal/erp/ase/knowledge_system.md`  
**Package:** `github.com/Yankzy/usetoro/internal/erp/ase`  
**Subsystem:** System 1 of the 5 Core Harness Subsystems  

---

# 1. Philosophy & Core Principles ("Why")

## 1.1 The Fundamental Paradigm Shift
Traditional AI architectures rely on **naive Document Retrieval-Augmented Generation (RAG)**—chunking unstructured PDFs or text files into vectors and performing blind similarity search over text blocks. In enterprise and financial automation, this naive approach fails catastrophically:
* **Hallucinations & Noise**: Unverified document text snippets introduce irrelevant or contradictory context into agent prompts.
* **No Single Source of Truth**: Text fragments lack ACID transactional guarantees and structural relationship links.
* **Dangerous Automated Writes**: Reading ungrounded text snippets often leads agents to perform incorrect automated ledger writes.

The **Knowledge System** shifts the paradigm from searching document text to **retrieving historic enterprise situations grounded in a property graph**.

> **Core Principle:** *"The Harness Understands Situations, Not Documents."*

## 1.2 The Three Grounded Epistemological Layers
Reality is represented across three unified layers inside PostgreSQL / AlloyDB Omni:

```
+-----------------------------------------------------------------------------------+
| Layer 3: Situation Vector Memory (toro_core.ase_vector_memory)                   |
| -> ScaNN / pgvector ANN sensors over historical situations & agent memory rules   |
+-----------------------------------------------------------------------------------+
| Layer 2: Directional Entity Graph (toro_core.enterprise_relationships)            |
| -> Directional ACID relationship graph (e.g. Invoice --ISSUED_BY--> Supplier)     |
+-----------------------------------------------------------------------------------+
| Layer 1: Authoritative Facts (toro_core.enterprise_facts)                         |
| -> Ground truth single-source-of-truth ACID entities (Invoices, Accounts, Ledger) |
+-----------------------------------------------------------------------------------+
```

## 1.3 Shannon Entropy & Confidence Guardrails ($C \ge 0.98$)
Every fact candidate and classification choice evaluated by an autonomous agent (`ASENode`) in an ASE DAG tracks **Shannon Entropy**:

$$H(k) = -\sum_{j} p_j \log_2 p_j$$

And **Unified Confidence**:

$$C = 1 - \frac{\sum H(k)}{\text{Total Properties}}$$

### Architectural System Boundary & Guardrail Interaction

It is critical to distinguish between the **Knowledge System** and the **ASE DAG Execution Engine**:

* **Knowledge System (`VectorStore` + Property Graph)**: Acts as the **Epistemological Context Provider**. It stores and retrieves $\text{Layer 1}$ Ground Truth Facts, $\text{Layer 2}$ Graph Relationships, and $\text{Layer 3}$ Situation Memories.
* **ASE DAG Execution Engine (`ASENode`)**: Acts as the **Consumer & Decision Engine**. During node execution, micro-agents query the Knowledge System, calculate entropy/confidence, and enforce state transitions:
  * **Automated Sync Guardrail**: If Unified Confidence $C \ge 0.98$, the `ASENode` transitions to `StateReadyForSync` or `StateClassified` for ledger writeback.
  * **Hold Transition**: If $C < 0.98$ or required ground-truth context cannot be resolved, the **ASE DAG Node** transitions its execution state to `StateHoldMissingCtx` (`"HOLD_MISSING_CONTEXT"`), halting automated writeback and holding the agent and handing over to the `Recovery system` (Decision theory).

---

# 2. Architecture & Mechanism ("How")

## 2.1 Multi-Tenant Isolation & Contextual Namespacing
To deliver high precision without breaking unified enterprise knowledge:

1. **Multi-Tenant Boundary (`realm_id`)**: Matches the ERP company connection ID. Cross-realm data leakage is physically impossible at the database query planner level.
2. **Domain Scoping (`namespace`)**: Partitions situation memories into logical scopes (e.g., `accounting:invoices`, `tax_rules`, `bank_reconciliation`, `audit_logs`).

### Query Modes:
* **Scoped Search**: Filters vectors by `WHERE realm_id = $1 AND namespace = $2` to eliminate vector noise during specialized micro-agent tasks.
* **Unified Enterprise Search**: Queries `WHERE realm_id = $1` (by passing `namespace = ""` or `"*"`), searching across all namespaces when discovering holistic enterprise patterns.

## 2.2 ScaNN Vector Acceleration & Inline Filtering
Vectors are embedded into `vector(1536)` columns and indexed using **Google's ScaNN** (Sparsifying Category Nearest Neighbor) index bundled in AlloyDB Omni:

* **Bitmap-Assisted Pre-Filtering**: The query planner uses the composite B-Tree index on `(realm_id, namespace, source_type)` to construct a tight in-memory bitmap before ScaNN executes cosine similarity search over that tenant's vector subset.

## 2.3 The Hydration Lifecycle (`VectorHydrator`)
A background worker continuously synchronizes enterprise activity into Layer 3 situation memory:

```
[Agent Memory Rules] ------+
                           |---> [Register Pending Row (embedding = NULL)]
[Staging Transactions] ----+                      |
                                                  v
                                      [VectorHydrator Embedder]
                                                  |
                                                  v
                               [OpenAI text-embedding-3-small (1536d)]
                                                  |
                                                  v
                               [Update Embedding & Build ScaNN Index]
```

## 2.4 Master Documents Storage & PostgreSQL CDC Pipeline Architecture

> **⚠️ Ingestion Gap Analysis & Status Warning:**  
> In the current codebase:
> * Bank feed rows & CSV statements are staged in `fignode.staging_transactions`.
> * QBO synced invoices & bills reside in `shadow_erp.invoices` and `shadow_erp.bills`.
> * Physical files (PDFs, PNGs) live in **AWS S3**.
> 
> **The Missing Ingestion Link:** There is currently **no unified master document table** (`toro_core.documents`) storing OCR-structured JSON data across invoices, receipts, and bank statements. Furthermore, **automatic CDC event streams from document insertions to Knowledge System vector/fact tables are NOT YET WIRED in production**. Below is the technical specification to implement this pipeline.

### Physical Document Storage Policy (AWS S3)
* **Zero Database BLOBs**: Physical PDF files, scanned receipts, and bank statement images are **never stored inside AlloyDB / PostgreSQL**.
* **S3 URI Pointer**: Raw files are uploaded to **AWS S3** bucket storage (`s3://toro-enterprise-vault/{realm_id}/{document_id}.pdf`). The database stores only S3 URI metadata and structured OCR JSON payloads.

---

### Target Master Documents Table (`toro_core.documents`)

To unify all incoming ground-truth documents (invoices, receipts, bank statements, bills, tax forms) into a single CDC-monitored stream, we introduce the `toro_core.documents` schema:

```sql
CREATE TABLE IF NOT EXISTS toro_core.documents (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    realm_id       TEXT NOT NULL,
    document_type  TEXT NOT NULL CHECK (document_type IN ('INVOICE', 'RECEIPT', 'BANK_STATEMENT', 'BILL', 'TAX_FORM', 'OTHER')),
    file_name      TEXT NOT NULL,
    mime_type      TEXT NOT NULL,
    s3_url         TEXT NOT NULL, -- e.g., 's3://toro-vault/realm_123/doc_456.pdf'
    
    -- OCR Extraction Payload
    ocr_status     TEXT NOT NULL DEFAULT 'PENDING' CHECK (ocr_status IN ('PENDING', 'PROCESSING', 'PROCESSED', 'FAILED')),
    raw_ocr_json   JSONB NOT NULL DEFAULT '{}', -- Complete structured OCR JSON payload
    extracted_text TEXT,                        -- Clean text representation for embedding
    
    -- Entity & Audit Links
    sender_email   TEXT,
    source_channel TEXT NOT NULL DEFAULT 'EMAIL' CHECK (source_channel IN ('EMAIL', 'WEB_UPLOAD', 'MOBILE_SCAN', 'API_SYNC')),
    processed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_documents_realm_status ON toro_core.documents (realm_id, ocr_status);
CREATE INDEX idx_documents_type ON toro_core.documents (realm_id, document_type);
```

---

### PostgreSQL CDC (Logical Replication) Pipeline Architecture

When OCR completes and structured data is written into `toro_core.documents`, PostgreSQL Logical Replication (`toro_ledger_pub`) automatically emits a CDC change event to feed the Knowledge System:

```
[Inbound Document: PDF / Image]
               │
               ▼
      [Upload to AWS S3] ─────────> Returns s3:// URI
               │
               ▼
   [OCR Worker (Textract / Claude)]
               │
               ▼
[INSERT INTO toro_core.documents]  (ocr_status = 'PROCESSED', raw_ocr_json = {...})
               │
               ▼
 [PostgreSQL Logical Publication] (toro_ledger_pub WAL Stream)
               │
               ▼
  [Debezium / NATS CDC Connector] ──> Emits `toro.cdc.documents.processed`
               │
               ├──────────────────────────────────────────┐
               ▼                                          ▼
   [Knowledge Graph Ingestion]                [Vector Memory Registration]
  1. Insert Layer 1 Fact Nodes               3. Insert Layer 3 Pending Row
     (toro_core.enterprise_facts)               (VectorStore.Upsert)
  2. Insert Layer 2 Graph Edges              4. VectorHydrator Embeds Text
     (toro_core.enterprise_relationships)          & Refreshes ScaNN Index
               │                                          │
               └────────────────────┬─────────────────────┘
                                    ▼
                     [Resume ASE DAG Execution]
```

---

### Integration with Recovery System (Decision Theory)

When an ASE DAG micro-agent encounters a transaction with missing context or confidence $C < 0.98$:
1. The **ASE DAG Node** transitions to `StateHoldMissingCtx` (`"HOLD_MISSING_CONTEXT"`).
2. Control is handed over to the **Recovery System (Decision Theory / Mailroom Triage Agent)**, which sends a Daily Digest or clarification email to the client.
3. When the client replies with an attached receipt/invoice or clarification text:
   - The attachment is stored in S3 and registered in `toro_core.documents`.
   - OCR runs $\rightarrow$ `toro_core.documents` row updates to `PROCESSED`.
   - **CDC Event Fires**: The Knowledge System automatically ingests the new facts ($\text{L1}/\text{L2}$) and embeds the new vector memory ($\text{L3}$).
   - The Recovery Engine wakes up the paused ASE DAG Node, which now re-evaluates context with $C \ge 0.98$ and auto-posts the transaction to the ERP ledger!

---

### Roadmap Checklist (What Must Be Implemented to Wire CDC In)
- [x] **Create Migration `040_create_toro_core_knowledge_system.sql`**: Add the `toro_core.documents`, `toro_core.enterprise_facts`, and `toro_core.enterprise_relationships` table schemas and indexes.
- [x] **Update Publication (`004_logical_publication.sql`)**: Include `toro_core.documents`, `toro_core.enterprise_facts`, and `toro_core.enterprise_relationships` in `toro_ledger_pub`.
- [x] **Bridge OCR Worker to `toro_core.documents`**: Ensure OCR text extraction writes `raw_ocr_json` and `s3_url` upon completion.
- [x] **CDC Ingestion Worker Handler**: Wire the CDC event listener and `KnowledgeIngestionEngine` to execute `enterprise_facts` inserts, `enterprise_relationships` edges, and `VectorStore.Upsert()`.

---

# 3. How We Are Going to Use It

## 3.1 Micro-Agent Classification Flow
When a transaction or document arrives for classification in an Autonomous Semantic Engine (ASE) DAG:

1. **Context Resolution**:
   - The micro-agent requests vector context for the current node description.
   - `VectorStore.Search()` executes a ScaNN ANN query inside the relevant `realm_id` and `namespace`.
   - Past human decisions and high-confidence memory rules are attached to the agent prompt.
2. **Graph Traversal**:
   - Layer 1 facts and Layer 2 relationship edges are queried to verify entity histories and parent accounts.
3. **Guardrail Evaluation**:
   - If confidence $C \ge 0.98$, the decision is auto-resolved and scheduled for writeback.
   - If $C < 0.98$, the system refuses automated writeback and requests additional context.

---

# 4. Developer Documentation & API Guide

## 4.1 Database Schemas & Migrations

The Knowledge System database structures are declared in:
- `sql/schema/006_ase_vector_memory.sql` (Base table & indexes)
- `sql/schema/039_add_namespace_to_knowledge_system.sql` (Namespace migration & composite indexes)

### Table Schema (`toro_core.ase_vector_memory`)
```sql
CREATE TABLE IF NOT EXISTS toro_core.ase_vector_memory (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    realm_id      TEXT        NOT NULL,
    namespace     TEXT        NOT NULL DEFAULT 'general',
    source_type   TEXT        NOT NULL CHECK (source_type IN ('memory_rule', 'resolved_tx')),
    raw_text      TEXT        NOT NULL,
    embedding     vector(1536),
    source_row_id UUID        NOT NULL,
    metadata      JSONB       NOT NULL DEFAULT '{}',
    embedded_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT ase_vector_memory_realm_ns_type_row_key
        UNIQUE (realm_id, namespace, source_type, source_row_id)
);

CREATE INDEX idx_ase_vector_memory_realm_ns
    ON toro_core.ase_vector_memory (realm_id, namespace, source_type);
```

---

## 4.2 Go Package Structures & Methods

Package path: `github.com/Yankzy/usetoro/internal/erp/ase`

### 1. `VectorStore`
Primary struct handling embedding generation, upserts, and ScaNN semantic searches.

```go
type VectorStore struct {
    pool     *pgxpool.Pool
    embedder *vector.Embedder
    logger   *slog.Logger
}

func NewVectorStore(pool *pgxpool.Pool, embedder *vector.Embedder, logger *slog.Logger) *VectorStore
```

#### Key Methods:

##### **`Upsert`**
Inserts or updates a vector memory row. If `embedding` is `nil`, the row is registered as pending hydration for the `VectorHydrator`.

```go
func (vs *VectorStore) Upsert(
    ctx context.Context,
    realmID string,
    namespace string,
    sourceType VectorSourceType,
    rawText string,
    sourceRowID uuid.UUID,
    embedding []float32,
    metadata map[string]any,
) error
```

##### **`Search`**
Executes a ScaNN ANN cosine search. Pass a non-empty `namespace` for scoped search, or `""` / `"*"` for cross-namespace search.

```go
func (vs *VectorStore) Search(
    ctx context.Context,
    tenantID string,
    realmID string,
    namespace string,
    queryEmbedding []float32,
) ([]VectorMemoryRow, error)
```

##### **`GenerateEmbedding`**
Generates a vector embedding slice using OpenAI's API.

```go
func (vs *VectorStore) GenerateEmbedding(
    ctx context.Context, 
    tenantID, realmID, text string,
) ([]float32, error)
```

---

### 2. Code Examples for Developers

#### Example A: Scoped Vector Search within a Micro-Agent
```go
package main

import (
    "context"
    "log"

    "github.com/Yankzy/usetoro/internal/erp/ase"
)

func FindTaxRules(ctx context.Context, vs *ase.VectorStore, tenantID, realmID, queryText string) {
    // 1. Generate query embedding
    embedding, err := vs.GenerateEmbedding(ctx, tenantID, realmID, queryText)
    if err != nil {
        log.Fatalf("failed to generate embedding: %v", err)
    }

    // 2. Perform scoped search in the 'tax_rules' namespace
    results, err := vs.Search(ctx, tenantID, realmID, "tax_rules", embedding)
    if err != nil {
        log.Fatalf("vector search failed: %v", err)
    }

    for _, res := range results {
        log.Printf("[%s] (sim: %.4f): %s", res.Namespace, res.Similarity, res.RawText)
    }
}
```

#### Example B: Registering a New Pending Vector Memory
```go
func RegisterMemoryRule(ctx context.Context, vs *ase.VectorStore, realmID string, ruleID uuid.UUID, ruleText string) error {
    metadata := map[string]any{
        "source": "user_defined_rule",
    }
    // Passing nil embedding registers row as pending; VectorHydrator will embed it
    return vs.Upsert(
        ctx,
        realmID,
        "general",
        ase.VectorSourceMemoryRule,
        ruleText,
        ruleID,
        nil, // embedding = nil (pending hydration)
        metadata,
    )
}
```

---

## 4.3 Configuration Parameters (`ase.yml` / `VectorMemoryConfig`)

Vector memory behavior is hot-reloadable and configured via `VectorMemoryConfig`:

| Parameter | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `Enabled` | `bool` | `false` | Enables/disables vector memory retrieval and hydration |
| `EmbeddingProvider` | `string` | `"openai"` | Embedding API provider |
| `OpenAIEmbeddingModel` | `string` | `"text-embedding-3-small"` | OpenAI model name |
| `EmbeddingDimensions` | `int` | `1536` | Vector dimension size |
| `ScaNNNumLeaves` | `int` | `10` | Number of leaves for ScaNN ANN index |
| `RetrievalTopK` | `int` | `5` | Number of top nearest neighbors returned |
| `HydratorIntervalSeconds` | `int` | `30` | Background hydrator polling tick interval |
| `HydratorMinConfidence` | `float64` | `0.98` | Minimum transaction confidence threshold for auto-hydration |
