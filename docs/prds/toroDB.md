# PRODUCT REQUIREMENT DOCUMENT (PRD)

## Project: ToroDB Engine (Standalone Agentic Database)

## Status
- [x] Draft
- [x] Review
- [x] Approved
- [x] Executed

**Document Version:** 4.1 (Unified Image & Deployment Architecture)
**Target Architecture:** Go (Harness), Directed Acyclic Graph (DAG), AlloyDB Omni (Embedded), Unified OCI Container
**Core Objective:** Build a deterministic, AI-native execution database packaged as a single, deployable image. It ingests unstructured streams, synthesizes them into ordered relational state, and executes autonomous workflows.

---

## 1. Executive Summary & Core Philosophy

### 1.1 The "Agentic Database in a Box"
While the Toro Main App DB manages the internal high-velocity application state (via Fignode, Redis, and a centralized Postgres instance), **ToroDB Engine** is a distinct, distributable infrastructure primitive. 

It goes beyond standard Retrieval-Augmented Generation (RAG) by offering **Ordered Data Synthesis**. It doesn't just store embeddings; it uses an embedded Go-based LLM harness to ingest chaotic streams (Slack, emails, PDFs), extract strictly typed variables, and write them to deterministic relational tables.

Crucially, **ToroDB is distributed as its own standalone container image**. Users do not need to stitch together a database, a vector store, and a workflow engine. Deploying the ToroDB image spins up both the high-performance PostgreSQL/ScaNN storage layer and the autonomous DAG execution harness in one unified artifact.

---

## 2. Core Technical Stack Architecture

### 2.1 Packaging & Distribution: The Unified Image
ToroDB is shipped as an OCI-compliant container image (e.g., `torodb/engine:latest`), tailored for absolute data sovereignty.

| Deployment Variant | Architecture | Value Proposition |
| :--- | :--- | :--- |
| **ToroDB Enterprise (The Omni-Bundle)** | Bundles the ToroDB Go Harness tightly coupled with **AlloyDB Omni** (Google’s install-anywhere, containerized AlloyDB engine). | **100% data sovereignty and zero vendor lock-in.** Air-gapped deployment for enterprises (e.g., Healthcare, Defense). |

---

## 3. Inside the Image: The Go Execution Harness

The ToroDB image does not just expose port `5432` for SQL. It exposes the **ToroDB Stream Engine**, a Go-based wrapper that sits in front of the AlloyDB core.

### 3.1 Lightning-Fast Stream Ingestion
Built natively in Go using `goroutines`, the ingestion layer acts as a high-throughput event sink for API webhooks, Slack sockets, and file uploads.

### 3.2 Topological DAG Orchestration
Every incoming event triggers a Directed Acyclic Graph (DAG). The DAG guarantees that execution order is strictly enforced (e.g., *Extract Invoice Data $\rightarrow$ Verify Math $\rightarrow$ Write to AlloyDB*). 

### 3.3 The Sync Layer
The Go harness manages the state transition between unstructured input and the highly structured AlloyDB columnar and relational tables.

---

## 4. Ordered Data Synthesis (LLM + SQL)

Instead of dumping raw text into a vector database, the ToroDB image uses LLMs as high-speed data parsers to enforce relational integrity.

### 4.1 The Synthesis Loop
1. **Intercept:** The Go harness intercepts the raw stream (e.g., a messy Slack thread discussing a project deadline).
2. **Extract:** The LLM is prompted strictly to output structured JSON representing the *state changes* in the conversation.
3. **Validate:** The Go harness validates this JSON against a rigid schema.
4. **Collapse:** The validated data is written into structured relational tables in AlloyDB.

**The Result:** Agents querying ToroDB don't have to "read" the chat history to know the budget; they query the structured table with standard, lightning-fast SQL.

---

## 5. Leveraging AlloyDB's Core Capabilities

Because the ToroDB image uses AlloyDB as its base storage primitive, it inherits massive architectural advantages over standard PostgreSQL:

### 5.1 High-Speed AI Queries (ScaNN)
Instead of using standard `pgvector` HNSW indexes, ToroDB leverages AlloyDB AI's integration of the Google ScaNN algorithm. This allows local agents to perform semantic vector searches up to 6x faster (and filtered searches up to 10x faster) than standard PostgreSQL.

### 5.2 Real-Time Analytics (Columnar Engine)
As the AI synthesizes massive amounts of ordered data, agents and humans need to analyze it. AlloyDB includes an in-memory columnar engine that processes analytical queries up to 100x faster than standard Postgres, allowing ToroDB to run heavy HTAP workloads natively.

---

## 6. Future Considerations

### 6.1 Enterprise Orchestration
When packaging the ToroDB Enterprise image with AlloyDB Omni embedded, consider providing a complete **Kubernetes Operator** (Helm chart) alongside standard `docker-compose` setups. This enables enterprises to easily manage failovers and read-replicas for both the Go harness and database seamlessly.