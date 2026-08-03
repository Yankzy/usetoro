# Product Requirements Document (PRD)

**Project Name:** EventOS — AI-Native Email & Enterprise Event Bus

**Document Status:** Draft / v1.0

**Target Audience:** Product, Engineering, AI Research, & Ops Teams

**Strategic Positioning:** Turning asynchronous business communication into a real-time AI operating system

---

## 1. Executive Summary & Vision

> **Vision:** Re-architect email from a static document inbox into an active event bus that automatically triggers, contextualizes, and executes enterprise workflows across domain agents.

Traditional email clients treat messages as isolated documents to be manually read, processed, and filed by humans. **EventOS** fundamentally decouples email transport from email workflow. By using Amazon SES as an ingestion gateway and S3/PostgreSQL as an indexing store, every incoming and outgoing message is parsed into an **Enterprise Event**.

These events flow through an AI pipeline that performs OCR, classification, thread-level contextualization, and vector embedding, routing the message to specialized domain agents (Accounting, HR, Sales, Legal) capable of taking direct operational actions.

---

## 2. System Architecture & The 9-Layer Stack

```
[ Incoming Internet / MX ]
          │
  (Layer 1: Gateway) ──────> Amazon SES
                              │
  (Layer 2 & 3: Storage) ───> S3 (Raw .eml + Attachments) + Relational DB (Metadata)
                              │
  (Layer 4: Threading) ────> Conversation Threading Engine
                              │
  (Layer 5 & 6: AI Ingest) ─> OCR ──> Parsing ──> Summarization ──> Vector Embeddings
                              │
  (Layer 7: RAG Context) ───> Enterprise Knowledge Base Sync (Contracts, SOPs, ERP)
                              │
  (Layer 8: UI Layer) ──────> Summary-First Actionable Inbox
                              │
  (Layer 9: Execution) ────> Event Bus ──> Domain Agents (Sales, Acct, HR)

```

---

## 3. Layer-by-Layer Functional Requirements

### Layer 1: Gateway & Transport (Amazon SES)

* **Inbound Ingestion:** SES acts as the stateless entry point. Upon receiving an MX record ping, SES fires an event via SNS/SQS to trigger processing Lambdas. Raw emails (`.eml`) pass directly to storage without permanent retention in SES.
* **Outbound Relay:** Agent-generated responses or human replies pass through an outbound API to SES for DKIM/SPF-signed transmission.

### Layer 2 & 3: Storage, Metadata & Attachments

* **Unstructured Persistence:** Raw `.eml` files and extracted binary attachments (PDF, DOCX, JPG) are permanently stored in S3 under `s3://tenant-id/emails/YYYY/MM/DD/{email_id}.eml`.
* **Structured Indexing:** Metadata is parsed from MIME headers and stored in a relational database for high-speed indexing and filtering.

### Layer 4: Thread-First Data Engine

* **Thread Resolution:** Incoming emails are mapped to parent thread IDs via `In-Reply-To` and `References` headers, fallbacking to fuzzy subject/participant matching.
* **Thread-Level Context Window:** AI agents never evaluate an email in isolation; prompt context windows are populated with the full conversational history of the `thread_id`.

### Layer 5 & 6: Intelligence & Ingestion Pipeline

Every incoming message triggers an asynchronous pipeline:

1. **Spam & Security Verification:** Check SPF/DKIM validation and run heuristic security screening.
2. **Attachment Extraction & OCR:** Convert PDFs/images into clean text markdown using OCR models.
3. **Structured Classification:** Classify intent (e.g., `Invoice`, `Lead`, `Complaint`, `Job Application`, `General Query`).
4. **Thread Summarization:** Generate a concise 2-sentence executive summary.
5. **Vector Embedding:** Compute dense vector embeddings across text body and attachment text for semantic search storage in a Vector DB.

### Layer 7: Deep Business Context (RAG Layer)

* **Knowledge Fusion:** Before routing an email to an agent or user interface, the system executes a retrieval step across connected enterprise tools (ERP balances, CRM deals, Notion/SOPs, past contracts).
* **Contextual Draft Generation:** If action is required, the model generates a context-aware proposed response incorporating retrieved business rules.

### Layer 8: Summary-First Actionable UI

* **UI Paradigm Shift:** The inbox renders **AI Summaries and Key Extraction Data** ahead of raw message text.
* **One-Click Execution Buttons:** Inline action triggers rendered alongside messages (e.g., `[✓ Pay Invoice]`, `[✓ Forward to CFO]`, `[✓ Update Lead Status]`).

### Layer 9: Autonomous Domain Agents

* **Agent Dispatcher:** Emails classified into specific domains are routed to dedicated agent execution loops.
* **Human-In-The-Loop (HITL):**
* If Agent Confidence **$\ge$ 95%**: Agent executes transaction autonomously (e.g., posts invoice directly to NetSuite).
* If Agent Confidence **< 95%**: Agent prepares draft/action card and prompts human operator for one-click approval.



---

## 4. The Meta-Layer: Enterprise Event Bus Architecture

Email is merely the first driver of this event architecture. The underlying engine functions as a unified **Enterprise Event Bus**.

```
                ┌────────────────────────────────────────┐
                │        ENTERPRISE EVENT BUS            │
                └───────────────────┬────────────────────┘
                                    │
    ┌───────────────┬───────────────┼───────────────┬───────────────┐
    │               │               │               │               │
  Email          WhatsApp         Slack          Stripe         Shopify
(SES/S3)         (API)         (Webhooks)      (Webhooks)      (Webhooks)
    │               │               │               │               │
    └───────────────┴───────────────┼───────────────┴───────────────┘
                                    │
                                    ▼
                     Unified Event Normalizer
                                    │
        ┌───────────────────────────┼───────────────────────────┐
        │                           │                           │
        ▼                           ▼                           ▼
  Sales Agent                Accounting Agent              HR Agent
  • Qualify Lead             • Book Invoice                • Parse CV
  • Update Pipeline          • Verify Payment              • Schedule Interview

```

### Event Normalization Schema

All external inputs—whether an SES email or a Stripe webhook—are normalized into a standardized event format:

```json
{
  "event_id": "evt_987654321",
  "tenant_id": "tenant_acme_corp",
  "source": "email.ses",
  "event_type": "communication.received",
  "timestamp": "2026-07-29T09:00:00Z",
  "actor": {
    "identifier": "vendor@supplier.com",
    "name": "Acme Supplier Inc"
  },
  "payload": {
    "thread_id": "thd_12345",
    "subject": "Invoice #1092",
    "summary": "Invoice for July services ($1,250), due Aug 15.",
    "classification": "INVOICE",
    "confidence_score": 0.98,
    "raw_ref": "s3://tenant-acme/emails/2026/07/29/msg_001.eml"
  },
  "context": {
    "crm_account_id": "acc_5544",
    "open_po_exists": true
  }
}

```

---

## 5. Core Data Models

### Database Schema Definition

#### `threads` Table

| Field | Type | Description |
| --- | --- | --- |
| `id` | UUID (PK) | Unique thread identifier |
| `tenant_id` | UUID (FK) | Multi-tenant isolation ID |
| `subject` | VARCHAR(255) | Canonical thread subject |
| `last_event_at` | TIMESTAMP | Timestamp of most recent message |
| `status` | ENUM | `OPEN`, `AGENT_PROCESSING`, `NEEDS_HUMAN`, `CLOSED` |
| `assigned_agent` | VARCHAR(50) | `ACCOUNTING`, `SALES`, `SUPPORT`, `NONE` |

#### `emails` Table

| Field | Type | Description |
| --- | --- | --- |
| `id` | UUID (PK) | Unique message identifier |
| `tenant_id` | UUID (FK) | Multi-tenant isolation ID |
| `thread_id` | UUID (FK) | References parent thread |
| `from_address` | VARCHAR(255) | Sender email |
| `to_addresses` | JSONB | Array of recipient emails |
| `s3_object_key` | VARCHAR(512) | S3 key location of raw `.eml` |
| `ai_summary` | TEXT | Auto-generated summary |
| `classification` | VARCHAR(50) | Inferred email type |
| `read` | BOOLEAN | Read/unread flag |

#### `attachments` Table

| Field | Type | Description |
| --- | --- | --- |
| `id` | UUID (PK) | Unique attachment identifier |
| `email_id` | UUID (FK) | Parent email |
| `filename` | VARCHAR(255) | Original file name |
| `mime_type` | VARCHAR(100) | MIME type (e.g., `application/pdf`) |
| `s3_key` | VARCHAR(512) | S3 storage location |
| `extracted_text` | TEXT | OCR / text dump for indexing |

---

## 6. Functional Requirements Matrix

| ID | Module | Feature | Requirement Description | Priority |
| --- | --- | --- | --- | --- |
| **FR-01** | Ingestion | SES Receiver | Process raw MIME email from SES via SQS trigger within < 500ms. | P0 |
| **FR-02** | Parsing | S3 & DB Split | Store raw `.eml` in S3; parse headers/body into relational DB metadata. | P0 |
| **FR-03** | Threading | Conversation Stitching | Automatically group messages using `In-Reply-To` and semantic matching. | P0 |
| **FR-04** | AI Pipeline | Extraction & OCR | Automatically OCR attached PDFs/Images and extract structured JSON (amount, date, vendor). | P0 |
| **FR-05** | Vector DB | Semantic Indexing | Generate and index dense embeddings for body text and attachment dumps. | P1 |
| **FR-06** | Governance | Confidence Thresholds | Implement $\ge$ 95% auto-execution vs < 95% Human-in-the-Loop review routing. | P0 |
| **FR-07** | UI | Action Cards | Render summary UI with single-click operational buttons mapped to API endpoints. | P1 |
| **FR-08** | Event Bus | Modular Connectors | Expose standardized event bus architecture to consume non-email inputs (Webhooks). | P2 |

---

## 7. Non-Functional Requirements

* **Performance & Latency:** Ingestion pipeline (SES to DB insertion & AI classification) must execute in `< 3.5 seconds` end-to-end.
* **Security & Compliance:**
* SOC2 Type II compliance.
* Encrypted storage at rest via AWS KMS (AES-256) for S3 objects and database instances.
* Strict multi-tenant row-level security (RLS) ensuring `tenant_id` isolation across vector embeddings and relational databases.


* **Scale & Throughput:** Architecture must handle up to 10,000 incoming events per minute per tenant using serverless autoscaling infrastructure (AWS Lambda, Aurora Serverless).

---

## 8. Key Success Metrics (KPIs)

| Metric | Target | Strategic Impact |
| --- | --- | --- |
| **Time-to-Action (TTA)** | Reduced by 75% | Drastically lowers human latency in processing invoices, leads, and support queries. |
| **Autonomous Resolution Rate** | > 60% of routine emails | Measures the percentage of emails processed by domain agents without manual human typing. |
| **Extraction Accuracy** | > 99% for financial metadata | Ensures high trust in automated bookkeeping and system updates. |
| **User Review Time** | < 5 seconds per email | Demonstrates the efficiency gains of summary-first UI cards over reading full emails. |