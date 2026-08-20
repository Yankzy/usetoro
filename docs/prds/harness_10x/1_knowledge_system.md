# Product Requirements Document (PRD)

## 1. Knowledge System (Epistemology)

**Document Reference:** `docs/prds/harness_10x/1_knowledge_system.md`  
**Status:** Fully Implemented (Integrated into ToroDB Engine) 
**Owner:** Core Architecture & Epistemology Engineering  
**Subsystem:** System 1 of the 5 Core Harness Subsystems

---

# 1. Executive Summary & Core Principle

The **Knowledge System** is the epistemological foundation of the harness. It defines how reality is represented, grounded, and verified.

### Core Principle: "The Harness Understands Situations, Not Documents"
Traditional AI architectures rely on naive document retrieval (RAG). The Knowledge System shifts the paradigm from searching document text to **retrieving historic enterprise situations**. Vector embeddings (`pgvector`/ScaNN) are recognized as **implementation sensors**, not top-level features.

---

# 2. Epistemological Architecture & Data Schema

The Knowledge System unifies reality into three distinct layers:

```
+-----------------------------------------------------------------------+
|  Layer 3: Semantic Situation Memory (ScaNN / pgvector Sensors)       |
+-----------------------------------------------------------------------+
|  Layer 2: Directional Entity Graph (enterprise_relationships)          |
+-----------------------------------------------------------------------+
|  Layer 1: Ground Truth Authoritative Facts (enterprise_facts)          |
+-----------------------------------------------------------------------+
```

## 2.1 Native AlloyDB Property Graph Schema

The property graph is persisted directly inside AlloyDB / PostgreSQL to preserve single-source-of-truth ACID guarantees and zero operational overhead:

```sql
CREATE SCHEMA IF NOT EXISTS toro_core;

-- Layer 1: Authoritative Fact Nodes
CREATE TABLE IF NOT EXISTS toro_core.enterprise_facts (
    fact_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    realm_id VARCHAR(64) NOT NULL,
    namespace VARCHAR(64) NOT NULL DEFAULT 'general',
    entity_type VARCHAR(64) NOT NULL, -- e.g. 'invoice', 'supplier', 'bank_account'
    uri VARCHAR(255) UNIQUE NOT NULL, -- e.g. 'fact:accounting:invoice:284'
    payload JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Layer 2: Directional Relationships
CREATE TABLE IF NOT EXISTS toro_core.enterprise_relationships (
    relationship_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    realm_id VARCHAR(64) NOT NULL,
    namespace VARCHAR(64) NOT NULL DEFAULT 'general',
    from_fact_id UUID NOT NULL REFERENCES toro_core.enterprise_facts(fact_id) ON DELETE CASCADE,
    to_fact_id UUID NOT NULL REFERENCES toro_core.enterprise_facts(fact_id) ON DELETE CASCADE,
    relation_type VARCHAR(64) NOT NULL, -- e.g. 'ISSUED_BY', 'PAID_BY', 'SETTLES'
    weight FLOAT8 NOT NULL DEFAULT 1.0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_relation UNIQUE (realm_id, namespace, from_fact_id, to_fact_id, relation_type)
);

CREATE INDEX idx_facts_realm_ns_type ON toro_core.enterprise_facts(realm_id, namespace, entity_type);
CREATE INDEX idx_rel_from_to ON toro_core.enterprise_relationships(from_fact_id, to_fact_id);
```

## 2.2 Situation Vector Memory (`ase_vector_memory`)

Situations are embedded using AlloyDB Omni ScaNN ANN indexes (`idx_ase_vector_memory_scann`) to execute fast cosine similarity over historical situation patterns:

```sql
CREATE TABLE IF NOT EXISTS toro_core.ase_vector_memory (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    realm_id VARCHAR(64) NOT NULL,
    namespace VARCHAR(64) NOT NULL DEFAULT 'general',
    source_type VARCHAR(32) NOT NULL,
    source_row_id UUID NOT NULL,
    raw_text TEXT NOT NULL,
    embedding VECTOR(1536),
    metadata JSONB NOT NULL DEFAULT '{}',
    embedded_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_vector_memory UNIQUE (realm_id, namespace, source_type, source_row_id)
);

CREATE INDEX idx_ase_vector_memory_realm_ns ON toro_core.ase_vector_memory (realm_id, namespace, source_type);
```

## 2.3 Contextual Scoping & Namespacing Architecture

The Knowledge System supports **Contextual Scoping** to eliminate vector noise during specialized agent reasoning while maintaining single-source-of-truth graph integrity:

* **Strict Multi-Tenant Boundary (`realm_id`)**: Isolates enterprise data across organizations. Cross-realm data leakage is physically impossible at the query planner level.
* **Domain Namespacing (`namespace`)**: Allows partition of vector situation memories and graph facts into domain scopes (e.g. `accounting:invoices`, `tax_rules`, `agent_memory`, `audit_logs`).
* **Dual Query Capabilities**:
  * **Scoped Retrieval**: Agents search within a specific domain namespace (e.g. `WHERE realm_id = $1 AND namespace = 'tax_rules'`) to maximize cosine relevance for domain tasks.
  * **Unified Enterprise Retrieval**: Agents execute cross-namespace searches (`WHERE realm_id = $1`) when discovering holistic enterprise relationships and multi-domain situation patterns.

---

# 3. Shannon Entropy & Confidence Guardrails

Every fact and candidate choice evaluated by an autonomous micro-agent (`ASENode`) in an ASE DAG tracks Shannon Entropy $H(k) = -\sum p_j \log_2 p_j$ and Unified Confidence $C = 1 - \frac{\sum H(k)}{\text{Total Properties}}$.

* **Context Provider Role**: The Knowledge System (`VectorStore` + Property Graph) supplies ground-truth facts ($\text{L1}$), entity links ($\text{L2}$), and situation memories ($\text{L3}$).
* **Execution Guardrail (`HOLD_MISSING_CONTEXT`)**: The consuming ASE DAG Engine evaluates confidence $C$. If $C < 0.98$ or required ground-truth context cannot be resolved, the **ASE DAG Node transitions its execution state to `HOLD_MISSING_CONTEXT`**, halting automated ledger writes until context is resolved or verified by a human.

# Comprehensive Implementation Plan: ToroDB Standalone Engine + Knowledge System + Native CDC

This plan details the complete unification of **ToroDB Engine** (the standalone "Agentic Database in a Box" container image), **System 1: Knowledge System (Epistemology)** as specified in [1_knowledge_system.md](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/1_knowledge_system.md) and [toroDB.md](file:///Users/Yankz/programming/usetoro/docs/prds/toroDB.md), and the **PostgreSQL Logical Replication (CDC) Pipeline**.

---

## 1. Executive Summary & Unified Architecture

Instead of having separate database and CDC worker containers, **ToroDB is packaged as a single deployable OCI container image** (`container/torodb/Dockerfile`).

The compiled Go execution binary (`/usr/local/bin/torodb`) runs natively alongside AlloyDB Omni inside the container:

```
+---------------------------------------------------------------------------------------------------+
|                                      ToroDB Container Image                                       |
|                                  (container/torodb/Dockerfile)                                    |
|                                                                                                   |
|  +---------------------------------------------------------------------------------------------+  |
|  |                 Go Execution Harness Binary (/usr/local/bin/torodb)                         |  |
|  |                             (go/cmd/torodb/main.go)                                         |  |
|  |                                                                                             |  |
|  |  +-----------------------------------+     +---------------------------------------------+  |  |
|  |  |   Knowledge System Engine         |     |   Native CDC Replicator                     |  |  |
|  |  |   (go/internal/erp/ase/           |     |   (go/internal/cdc/)                        |  |  |
|  |  |    knowledge_system/)             |     |                                             |  |  |
|  |  |                                   |     |   Tails WAL via pglogrepl on                |  |  |
|  |  |   - GraphStore (L1 Facts, L2 Graph)|    |   toro_ledger_pub slot; emits               |  |  |
|  |  |   - DocumentStore & Ingestion     |     |   NATS events (ledger.documents.insert)    |  |  |
|  |  |   - GraphContextProvider          |     +---------------------------------------------+  |  |
|  |  |   - VectorStore & Hydrator        |                            │                           |  |
|  |  +-----------------------------------+                            │ CDC Event Stream          |  |
|  |                    ▲                                              ▼                           |  |
|  |                    └──────────────────────────────────────────────┘                           |  |
|  +---------------------------------------------------------------------------------------------+  |
|                                              │                                                    |
|                                              ▼ Unix Socket / Localhost 5432                       |
|  +---------------------------------------------------------------------------------------------+  |
|  |                         Google AlloyDB Omni Engine (PostgreSQL 17)                          |  |
|  |                                                                                             |  |
|  |   Layer 3: Situation Vector Memory    (toro_core.ase_vector_memory + ScaNN index)           |  |
|  |   Layer 2: Directional Entity Graph   (toro_core.enterprise_relationships)                  |  |
|  |   Layer 1: Ground Truth Facts         (toro_core.enterprise_facts)                          |  |
|  |   Master Documents Store              (toro_core.documents)                                 |  |
|  +---------------------------------------------------------------------------------------------+  |
+---------------------------------------------------------------------------------------------------+
```

---

## User Review Required

> [!IMPORTANT]
> - **CDC Worker Consolidation**: Merges `cdc.RunReplicator` directly into `go/cmd/torodb/main.go`. Replaces/deprecates the standalone `cdc-worker` service in `docker-compose.yml` so CDC replication is natively driven by ToroDB.
> - **Container Migration**: Replaces the generic `google/alloydbomni` container in `docker-compose.yml` with the built **`torodb` container** (`container/torodb/Dockerfile`).
> - **Migration `040_create_toro_core_knowledge_system.sql`**: Creates `toro_core.enterprise_facts` (Layer 1), `toro_core.enterprise_relationships` (Layer 2), and `toro_core.documents` (Master Documents).
> - **Go Knowledge System Package (`go/internal/erp/ase/knowledge_system/`)**: Contains `GraphStore`, `DocumentStore`, `KnowledgeIngestionEngine`, `GraphContextProvider`, and unit tests.

---

## Proposed Changes

### 1. Database Migrations & Schemas

#### [NEW] [040_create_toro_core_knowledge_system.sql](file:///Users/Yankz/programming/usetoro/sql/schema/040_create_toro_core_knowledge_system.sql)
- `toro_core.enterprise_facts`: Layer 1 ground truth facts (`fact_id`, `realm_id`, `namespace`, `entity_type`, `uri`, `payload`, `created_at`).
- `toro_core.enterprise_relationships`: Layer 2 directional relationship graph edges (`relationship_id`, `realm_id`, `namespace`, `from_fact_id`, `to_fact_id`, `relation_type`, `weight`, `created_at`, `unique_relation` constraint).
- `toro_core.documents`: Master documents table for OCR payloads (`id`, `realm_id`, `document_type`, `file_name`, `mime_type`, `s3_url`, `ocr_status`, `raw_ocr_json`, `extracted_text`, `sender_email`, `source_channel`, `processed_at`).

#### [MODIFY] [004_logical_publication.sql](file:///Users/Yankz/programming/usetoro/sql/schema/004_logical_publication.sql)
- Includes `toro_core.documents`, `toro_core.enterprise_facts`, and `toro_core.enterprise_relationships` in publication `toro_ledger_pub`.

#### [NEW] [knowledge_system.sql](file:///Users/Yankz/programming/usetoro/sql/queries/knowledge_system.sql)
- SQL queries for fact CRUD, URI lookups, relationship edge insertion, graph traversal, and document queue management.

---

### 2. Go Knowledge System Engine (`go/internal/erp/ase/knowledge_system/`)

#### [NEW] [graph_store.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/knowledge_system/graph_store.go)
- Implements `GraphStore` managing Layer 1 `Fact` nodes and Layer 2 `Relationship` directional edges.
- Methods: `CreateFact`, `GetFactByURI`, `GetFactByID`, `QueryFacts`, `CreateRelationship`, `GetOutboundRelationships`, `GetInboundRelationships`, `TraverseGraph`.

#### [NEW] [documents_store.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/knowledge_system/documents_store.go)
- Implements `DocumentStore` for `toro_core.documents`.
- Implements `KnowledgeIngestionEngine`: parses OCR JSON payloads into Layer 1 facts, constructs Layer 2 directional graph edges, and registers Layer 3 pending vector memory rows in `VectorStore`.

#### [NEW] [graph_context_provider.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/knowledge_system/graph_context_provider.go)
- Implements `GraphContextProvider` conforming to `ase.ContextProvider`. Resolves grounded facts and relationship subgraphs into `ASENode` micro-agent prompts during DAG node execution.

#### [NEW] [graph_store_test.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/knowledge_system/graph_store_test.go)
- Unit tests for `GraphStore`: fact creation, URI uniqueness, directional edge creation, graph traversal.

#### [NEW] [knowledge_ingestion_test.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/knowledge_system/knowledge_ingestion_test.go)
- Unit tests for `KnowledgeIngestionEngine`: OCR payload ingestion into L1 facts, L2 relationships, and L3 vector memory.

#### [NEW] [graph_context_provider_test.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/knowledge_system/graph_context_provider_test.go)
- Unit tests for `GraphContextProvider`: verifying graph node context resolution for ASE DAG nodes.

---

### 3. ToroDB Main Binary & CDC Replicator Integration

#### [MODIFY] [main.go](file:///Users/Yankz/programming/usetoro/go/cmd/torodb/main.go)
- Starts the native CDC Replicator (`cdc.RunReplicator`) in a concurrent goroutine within ToroDB, tailing WAL logs and publishing events to NATS JetStream.
- Initializes `GraphStore`, `DocumentStore`, `KnowledgeIngestionEngine`, `VectorStore`, and `VectorHydrator`.
- Listens to incoming document CDC events and processes OCR JSON payloads directly into Knowledge System L1/L2/L3 stores.

#### [MODIFY] [Dockerfile](file:///Users/Yankz/programming/usetoro/container/torodb/Dockerfile)
- Multi-stage build compiling `./cmd/torodb/main.go` and copying it into `google/alloydbomni:17.7-ubi9`.

#### [MODIFY] [docker-compose.yml](file:///Users/Yankz/programming/usetoro/container/docker-compose.yml)
- Update `db` service definition to build from `container/torodb/Dockerfile`.
- Consolidate `cdc-worker` service into ToroDB (standalone `cdc-worker` container removed as ToroDB handles its own CDC replication natively).

#### [MODIFY] [1_knowledge_system.md](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/1_knowledge_system.md)
- Update status to **Fully Implemented** with ToroDB + CDC integration details.

---

## Verification Plan

### Automated Tests
- Run `go test -v ./go/internal/erp/ase/knowledge_system/...` to verify graph, ingestion, and context provider unit tests pass.
- Run `go test -v ./go/internal/cdc/...` to verify CDC decoding and publishing tests pass.
- Run `go test -v ./go/internal/erp/ase/...` to ensure all existing ASE tests pass cleanly.
- Run `go build ./go/cmd/torodb` to ensure the ToroDB binary compiles without errors.

### Manual Verification
- Verify Go build output, schema creation, unit test pass rates, and clean compilation.
