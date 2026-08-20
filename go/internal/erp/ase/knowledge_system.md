# Knowledge System (Epistemology) & ToroDB Engine Architecture

**Document Location:** `go/internal/erp/ase/knowledge_system.md`  
**Package:** `github.com/Yankzy/usetoro/internal/erp/ase/knowledge_system` & `github.com/Yankzy/usetoro/internal/erp/ase`  
**Subsystem:** System 1 of the 5 Core Harness Subsystems  
**Target Architecture:** ToroDB Engine (Standalone Agentic Database in a Box)  
**Status:** Fully Implemented & Integrated  

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
Every fact candidate and classification choice evaluated by an autonomous micro-agent (`ASENode`) in an Autonomous Semantic Engine (ASE) DAG tracks **Shannon Entropy**:

$$H(k) = -\sum_{j} p_j \log_2 p_j$$

And **Unified Confidence**:

$$C = 1 - \frac{\sum H(k)}{\text{Total Properties}}$$

### Architectural System Boundary & Guardrail Interaction
* **Knowledge System (`VectorStore` + `GraphStore`)**: Acts as the **Epistemological Context Provider**. It stores and retrieves $\text{Layer 1}$ Ground Truth Facts, $\text{Layer 2}$ Graph Relationships, and $\text{Layer 3}$ Situation Vector Memories.
* **ASE DAG Execution Engine (`ASENode`)**: Acts as the **Consumer & Decision Engine**. During node execution, micro-agents query the Knowledge System via `GraphContextProvider`, calculate entropy/confidence, and enforce state transitions:
  * **Automated Sync Guardrail**: If Unified Confidence $C \ge 0.98$, the `ASENode` transitions to `StateReadyForSync` or `StateClassified` for ledger writeback.
  * **Hold Transition**: If $C < 0.98$ or required ground-truth context cannot be resolved, the `ASENode` transitions its execution state to `StateHoldMissingCtx` (`"HOLD_MISSING_CONTEXT"`), halting automated writeback and handing over to the Recovery Engine.

---

# 2. ToroDB Standalone Packaging & Container Architecture

## 2.1 The "Agentic Database in a Box" Concept
ToroDB is not just a database image; it is an **OCI-compliant standalone container artifact** (`container/torodb/Dockerfile`) that packages:
1. **Google AlloyDB Omni Core**: High-performance PostgreSQL 17 database with Google ScaNN ANN vector search and Columnar HTAP analytics.
2. **The Go Execution Harness Binary (`/usr/local/bin/torodb`)**: Compiled from `go/cmd/torodb/main.go`, running natively alongside AlloyDB inside the container.
3. **Native PostgreSQL CDC Replicator**: Tailings WAL logs (`toro_ledger_pub`) directly inside the Go harness via `pglogrepl`, eliminating external sidecar containers.
4. **Knowledge System Engine**: Managing Layer 1 Facts, Layer 2 Directional Relationships, Layer 3 Vector Memories, Master Documents, and `GraphContextProvider`.

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
|  |  |   [GraphContextProvider]   [VectorStore]           [VectorHydrator]               |  |  |
|  |  |   (DAG Node context)       (ScaNN search)          (Async embedding background)   |  |  |
|  |  +-----------------------------------------------------------------------------------+  |  |
|  |                                                                                         |  |
|  |  +-----------------------------------------------------------------------------------+  |  |
|  |  |           Native CDC Replicator Engine (go/internal/cdc/replicator.go)            |  |  |
|  |  |   - Tails WAL via pglogrepl on publication 'toro_ledger_pub'                      |  |  |
|  |  |   - Publishes change events synchronously to NATS JetStream (ledger.table.action)  |  |  |
|  |  +-----------------------------------------------------------------------------------+  |  |
|  +-----------------------------------------------------------------------------------------+  |
|                                              │                                                |
|                                              ▼ Unix Socket / Localhost 5432                   |
|  +-----------------------------------------------------------------------------------------+  |
|  |                         Google AlloyDB Omni Engine (PostgreSQL 17)                      |  |
|  |                                                                                         |  |
|  |   Layer 3: Situation Vector Memory    (toro_core.ase_vector_memory + ScaNN index)       |  |
|  |   Layer 2: Directional Entity Graph   (toro_core.enterprise_relationships)              |  |
|  |   Layer 1: Ground Truth Facts         (toro_core.enterprise_facts)                      |  |
|  |   Master Documents Store              (toro_core.documents)                             |  |
|  +-----------------------------------------------------------------------------------------+  |
+-----------------------------------------------------------------------------------------------+
```

## 2.2 Docker Service & Network Integration
In `container/docker-compose.yml`, the primary database service `db` is defined as:

```yaml
  db:
    build:
      context: ../
      dockerfile: container/torodb/Dockerfile
    restart: always
    environment:
      POSTGRES_DB: toro
      POSTGRES_USER: toro
      POSTGRES_PASSWORD: toro_password
      PGDATA: /var/lib/postgresql/data/pgdata
      NATS_URL: nats://nats-1:4222,nats://nats-2:4222,nats://nats-3:4222
      DATABASE_URL: postgres://toro:toro_password@localhost:5432/toro?sslmode=disable
      OPENAI_API_KEY: ${OPENAI_API_KEY}
    volumes:
      - ./postgres/db_data:/var/lib/postgresql/data
      - ./postgres/init-multiple-dbs.sh:/docker-entrypoint-initdb.d/init-multiple-dbs.sh
    ports:
      - "5435:5432"
    healthcheck:
      test: [ "CMD-SHELL", "pg_isready -h localhost -U toro" ]
      interval: 5s
      timeout: 5s
      retries: 5
      start_period: 60s
    networks:
      - toro-net
```

All application microservices (`gate`, `graphql`, `sync`, `fignode`, `protocol`, `python-worker`) connect directly to `db:5432` / `DATABASE_URL`.

---

# 3. Database Schemas & Migrations

The Knowledge System database schemas are declared across four migration files under `sql/schema/`:

### 3.1 Migration `040_create_toro_core_knowledge_system.sql`

```sql
-- Layer 1: Authoritative Fact Nodes
CREATE TABLE IF NOT EXISTS toro_core.enterprise_facts (
    fact_id     UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    realm_id    TEXT        NOT NULL,
    namespace   TEXT        NOT NULL DEFAULT 'general',
    entity_type TEXT        NOT NULL, -- e.g. 'invoice', 'supplier', 'bank_account'
    uri         TEXT        UNIQUE NOT NULL, -- e.g. 'fact:accounting:invoice:284'
    payload     JSONB       NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_facts_realm_ns_type 
    ON toro_core.enterprise_facts (realm_id, namespace, entity_type);

-- Layer 2: Directional Relationships
CREATE TABLE IF NOT EXISTS toro_core.enterprise_relationships (
    relationship_id UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    realm_id        TEXT        NOT NULL,
    namespace       TEXT        NOT NULL DEFAULT 'general',
    from_fact_id    UUID        NOT NULL REFERENCES toro_core.enterprise_facts(fact_id) ON DELETE CASCADE,
    to_fact_id      UUID        NOT NULL REFERENCES toro_core.enterprise_facts(fact_id) ON DELETE CASCADE,
    relation_type   TEXT        NOT NULL, -- e.g. 'ISSUED_BY', 'PAID_BY', 'SETTLES'
    weight          FLOAT8      NOT NULL DEFAULT 1.0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_relation UNIQUE (realm_id, namespace, from_fact_id, to_fact_id, relation_type)
);

CREATE INDEX idx_rel_from_to 
    ON toro_core.enterprise_relationships (from_fact_id, to_fact_id);

-- Master Documents Table
CREATE TABLE IF NOT EXISTS toro_core.documents (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    realm_id       TEXT        NOT NULL,
    document_type  TEXT        NOT NULL CHECK (document_type IN ('INVOICE', 'RECEIPT', 'BANK_STATEMENT', 'BILL', 'TAX_FORM', 'OTHER')),
    file_name      TEXT        NOT NULL,
    mime_type      TEXT        NOT NULL,
    s3_url         TEXT        NOT NULL,
    ocr_status     TEXT        NOT NULL DEFAULT 'PENDING' CHECK (ocr_status IN ('PENDING', 'PROCESSING', 'PROCESSED', 'FAILED')),
    raw_ocr_json   JSONB       NOT NULL DEFAULT '{}',
    extracted_text TEXT,
    sender_email   TEXT,
    source_channel TEXT        NOT NULL DEFAULT 'EMAIL' CHECK (source_channel IN ('EMAIL', 'WEB_UPLOAD', 'MOBILE_SCAN', 'API_SYNC')),
    processed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_documents_realm_status ON toro_core.documents (realm_id, ocr_status);
CREATE INDEX idx_documents_type ON toro_core.documents (realm_id, document_type);
```

### 3.2 Migration `006_ase_vector_memory.sql` & `039_add_namespace_to_knowledge_system.sql`

```sql
CREATE TABLE IF NOT EXISTS toro_core.ase_vector_memory (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    realm_id      TEXT        NOT NULL,
    namespace     TEXT        NOT NULL DEFAULT 'general',
    source_type   TEXT        NOT NULL CHECK (source_type IN ('memory_rule', 'resolved_tx')),
    raw_text      TEXT        NOT NULL,
    embedding     vector(1536),              -- NULL until hydrated by VectorHydrator
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

### 3.3 Logical Publication `004_logical_publication.sql`

```sql
DROP PUBLICATION IF EXISTS toro_ledger_pub;

CREATE PUBLICATION toro_ledger_pub FOR TABLE
    toro_core.users,
    toro_core.erp_connections,
    toro_core.documents,
    toro_core.enterprise_facts,
    toro_core.enterprise_relationships;
```

---

# 4. Go Implementation & API Reference

Package paths:
- `github.com/Yankzy/usetoro/internal/erp/ase/knowledge_system`
- `github.com/Yankzy/usetoro/internal/erp/ase`

## 4.1 `GraphStore` (`graph_store.go`)
Manages Layer 1 Facts and Layer 2 Directional Relationships.

```go
type Fact struct {
    FactID     uuid.UUID       `json:"fact_id"`
    RealmID    string          `json:"realm_id"`
    Namespace  string          `json:"namespace"`
    EntityType string          `json:"entity_type"`
    URI        string          `json:"uri"`
    Payload    json.RawMessage `json:"payload"`
    CreatedAt  time.Time       `json:"created_at"`
}

type Relationship struct {
    RelationshipID uuid.UUID `json:"relationship_id"`
    RealmID        string    `json:"realm_id"`
    Namespace      string    `json:"namespace"`
    FromFactID     uuid.UUID `json:"from_fact_id"`
    ToFactID       uuid.UUID `json:"to_fact_id"`
    RelationType   string    `json:"relation_type"`
    Weight         float64   `json:"weight"`
    CreatedAt      time.Time `json:"created_at"`
}

type GraphNeighbor struct {
    Relationship Relationship `json:"relationship"`
    Fact         Fact         `json:"fact"`
    Direction    string       `json:"direction"` // "OUTBOUND" or "INBOUND"
}

type GraphStore struct { ... }
```

### Key Methods:
- `CreateFact(ctx, sessionID, namespace, entityType, uri, payload)`
- `GetFactByURI(ctx, sessionID, uri)`
- `GetFactByID(ctx, factID)`
- `CreateRelationship(ctx, sessionID, namespace, fromFactID, toFactID, relationType, weight)`
- `GetOutboundRelationships(ctx, factID)`
- `GetInboundRelationships(ctx, factID)`
- `TraverseGraph(ctx, factID)`

---

## 4.2 `DocumentStore` & `KnowledgeIngestionEngine` (`documents_store.go`)
Handles master document lifecycle and automatic ingestion into L1 facts, L2 graph edges, and L3 vector memory.

```go
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
```

### Key Methods:
- `CreateDocument(ctx, sessionID, docType, fileName, mimeType, s3URL, sourceChannel, senderEmail)`
- `UpdateOCRStatus(ctx, id, status, rawOCRPayload, extractedText)`
- `GetDocumentByID(ctx, id)`
- `IngestProcessedDocument(ctx, doc)`:
  1. Creates Layer 1 document fact (`fact:document:{type}:{id}`).
  2. Extracts vendor/counterparty facts and creates Layer 2 directional relationship (`Document --ISSUED_BY--> Vendor`).
  3. Registers Layer 3 vector memory (`VectorStore.Upsert` with `embedding = nil` for async hydration).

---

## 4.3 `GraphContextProvider` (`graph_context_provider.go`)
Implements `ase.ContextProvider` to resolve graph subgraphs during ASE DAG micro-agent node execution.

```go
type GraphContextProvider struct { ... }

func (gcp *GraphContextProvider) Name() string // "graph_knowledge_provider"
func (gcp *GraphContextProvider) Resolve(ctx context.Context, node *ase.AutonomousSemanticEngineNode, config map[string]any, deps ase.ProviderDependencies) (any, error)
```

---

## 4.4 `VectorStore` & `VectorHydrator` (`vector_store.go` & `vector_hydrator.go`)
Manages ScaNN ANN cosine vector retrieval and background embedding generation.

- `VectorStore.Upsert(ctx, realmID, namespace, sourceType, rawText, sourceRowID, embedding, metadata)`
- `VectorStore.Search(ctx, tenantID, realmID, namespace, queryEmbedding)`
- `VectorStore.EnsureScaNNIndex(ctx, tenantID, realmID)`
- `VectorHydrator.Run(ctx)`: Polling background loop generating embeddings via OpenAI and maintaining ScaNN indices.

---

# 5. End-to-End Ingestion & Processing Lifecycle

```
[Inbound Document Ingress (e.g. Postmark Email)]
                       │
                       ▼
       [Upload Attachments to AWS S3]
                       │
                       ▼
    [INSERT INTO toro_core.documents]      (ocr_status = 'PENDING', metadata = {"callback_topic": "..."})
                       │
                       ▼
         [Relinquish Control to ToroDB]    (Handoff to ToroDB Ingestion Engine)
                       │
                       ▼
 [ToroDB Dispatches to Python OCR Worker]  (Subject: worker.inbox.python.ocr)
                       │
                       ▼
    [Python OCR Microservice Execution]    (OpenAI Vision visual extraction)
                       │
                       ▼
    [UPDATE toro_core.documents]           (ocr_status = 'PROCESSED', raw_ocr_json = {...})
                       │
                       ▼
    [KnowledgeIngestionEngine Ingest]
     1. Insert Layer 1 Fact Nodes          (toro_core.enterprise_facts)
     2. Insert Layer 2 Graph Edges         (toro_core.enterprise_relationships)
     3. Queue Layer 3 Vector Row           (toro_core.ase_vector_memory)
                       │
                       ▼
 [Publish Event to TAP Orchestrator]       (Topic: events.accounting.1.pcm_bookkeeping)
                       │
                       ▼
  [TAP Orchestrator Manages Workflow]      (tap/workflows/pcm_bookkeeping.yml)
   1. pcm_worker (ingests ToroDB facts into staging_transactions)
   2. run_enrichment (database.enrich_rows)
   3. ase_bridge (fans out ASE DAG micro-agents)
   4. pcm_export (generates export artifact)
   5. human_review (HITL review step)
```

---

# 6. Developer Code Examples

### Example A: Ingesting an OCR Document into Knowledge Graph & Vector Memory
```go
package main

import (
    "context"
    "log"

    "github.com/Yankzy/usetoro/internal/erp/ase/knowledge_system"
)

func ProcessIncomingInvoice(ctx context.Context, kie *knowledge_system.KnowledgeIngestionEngine, doc *knowledge_system.Document) {
    err := kie.IngestProcessedDocument(ctx, doc)
    if err != nil {
        log.Fatalf("failed to ingest document: %v", err)
    }
    log.Println("Document successfully ingested into L1 Facts, L2 Graph, and L3 Vector Memory.")
}
```

### Example B: Traversing Entity Graph Neighborhood
```go
func InspectSupplierHistory(ctx context.Context, gs *knowledge_system.GraphStore, factID uuid.UUID) {
    neighbors, err := gs.TraverseGraph(ctx, factID)
    if err != nil {
        log.Fatalf("failed to traverse graph: %v", err)
    }
    for _, n := range neighbors {
        log.Printf("[%s] %s -> Edge: %s -> Target URI: %s", n.Direction, n.Relationship.RelationType, n.Fact.URI)
    }
}
```

### Example C: Scoped ScaNN Vector Search
```go
func SearchTaxContext(ctx context.Context, vs *ase.VectorStore, tenantID, realmID, queryText string) {
    embedding, err := vs.GenerateEmbedding(ctx, tenantID, realmID, queryText)
    if err != nil {
        log.Fatalf("failed to generate embedding: %v", err)
    }
    results, err := vs.Search(ctx, tenantID, realmID, "tax_rules", embedding)
    if err != nil {
        log.Fatalf("vector search failed: %v", err)
    }
    for _, res := range results {
        log.Printf("Similarity: %.4f | Text: %s", res.Similarity, res.RawText)
    }
}
```
