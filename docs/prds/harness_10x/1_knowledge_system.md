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
