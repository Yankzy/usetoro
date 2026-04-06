# Product Requirements Document

## Product: AI-Native Accounting Engine (Parallel to Traditional Accounting)

---

## 1. Objective

Build an AI-native accounting system that models **financial reality as economic events**, while remaining fully compliant with accounting standards by generating:

* General Ledger
* Profit & Loss
* Balance Sheet
* Cash Flow
* Tax-ready outputs

The system will run **in parallel** with existing workflows (e.g. QuickBooks) and gradually become the source of truth.

---

## 2. Core Principles

1. **Reality First**

   * Store raw and enriched financial events, not journal entries

2. **Derived Accounting**

   * All accounting artifacts are generated, not stored

3. **Reversibility**

   * Ability to recompute financials under different assumptions

4. **Continuous State**

   * No “closing”; system is always up to date

5. **Auditability**

   * Every output must trace back to source data + reasoning

---

## 3. System Architecture

### 3.1 High-Level Components

1. **Ingestion Layer**
2. **Event Store (Core)**
3. **AI Interpretation Layer**
4. **Compliance Projection Engine**
5. **Output Layer**
6. **Sync Layer (External Systems)**

---

## 4. Functional Requirements

---

## 4.1 Ingestion Layer

### Description

Collect financial data from multiple sources.

### Inputs

* Bank feeds via Plaid
* CSV uploads
* Invoices (PDF, email)
* Payroll systems
* Manual entries

### Requirements

* Normalize all inputs into a standard schema
* Store raw data unchanged
* Deduplicate transactions
* Assign unique `source_event_id`

### Output Schema (RawTransaction)

```json
{
  "id": "uuid",
  "source": "plaid",
  "timestamp": "datetime",
  "amount": 5000,
  "currency": "USD",
  "description": "META ADS 123",
  "counterparty_raw": "META",
  "metadata": {}
}
```

---

## 4.2 Event Store (Core Layer)

### Description

Canonical source of truth. Stores **economic events**, not accounting entries.

### Requirements

* Immutable event log
* Versioned updates (event revisions)
* Linked relationships between events

### Event Schema

```json
{
  "event_id": "uuid",
  "event_type": "payment",
  "timestamp": "datetime",
  "amount": 5000,
  "currency": "USD",

  "counterparty": {
    "normalized_name": "Meta Platforms Inc",
    "entity_id": "meta_001"
  },

  "context": {
    "purpose": "advertising",
    "linked_campaign": "campaign_123",
    "contract_id": null
  },

  "financial_dimensions": {
    "cash_impact": true,
    "accrual_impact": true,
    "time_horizon": "immediate|deferred"
  },

  "source_references": ["raw_txn_id"],

  "confidence_score": 0.92,

  "created_at": "datetime"
}
```

---

## 4.3 AI Interpretation Layer

### Description

Transforms raw events into structured economic meaning.

### Responsibilities

* Counterparty resolution (entity matching)
* Intent classification
* Accrual inference
* Tax treatment estimation
* Relationship linking (e.g. payment ↔ invoice)

### Output Schema (Interpretation)

```json
{
  "event_id": "uuid",
  "interpretations": [
    {
      "type": "expense",
      "subtype": "marketing",
      "confidence": 0.8
    },
    {
      "type": "prepaid_asset",
      "confidence": 0.2,
      "amortization_period_days": 30
    }
  ],

  "tax_tags": [
    {
      "jurisdiction": "US",
      "deductible": true,
      "confidence": 0.85
    }
  ]
}
```

### Requirements

* Support multiple hypotheses
* Store confidence scores
* Maintain reasoning logs (for audit)

---

## 4.4 Compliance Projection Engine

### Description

Generates accounting artifacts from events.

### Responsibilities

* Map interpretations to chart of accounts
* Generate double-entry journal entries
* Apply accounting rules (GAAP/IFRS)

### Journal Entry Schema

```json
{
  "journal_id": "uuid",
  "event_id": "uuid",

  "entries": [
    {
      "account": "Marketing Expense",
      "debit": 5000,
      "credit": 0
    },
    {
      "account": "Cash",
      "debit": 0,
      "credit": 5000
    }
  ],

  "standard": "GAAP",
  "generated_at": "datetime"
}
```

### Requirements

* Deterministic generation
* Re-runnable (idempotent)
* Versioned outputs (for audit trails)

---

## 4.5 Output Layer

### Description

Produces financial statements.

### Outputs

* Profit & Loss
* Balance Sheet
* Cash Flow Statement
* Trial Balance

### Requirements

* Real-time generation
* Snapshot capability (e.g. month-end)
* Multi-standard support (GAAP, IFRS)

---

## 4.6 Sync Layer

### Description

Ensures compatibility with legacy systems.

### Responsibilities

* Push journal entries to QuickBooks
* Pull existing ledger data for reconciliation

### Requirements

* Two-way sync (optional)
* Conflict detection
* Logging of sync operations

---

## 5. Non-Functional Requirements

### Performance

* Handle 1M+ events per company
* Query latency < 300ms for financial summaries

### Scalability

* Event store must be horizontally scalable
* Use append-only architecture

### Reliability

* 99.9% uptime
* Event immutability guarantees

### Security

* Encrypt all financial data at rest and in transit
* Role-based access control

---

## 6. Audit & Traceability

### Requirements

Every financial output must trace to:

* Source transaction
* Event transformation
* AI interpretation
* Generated journal entries

### Audit API Example

```json
{
  "statement_line": "Marketing Expense",
  "amount": 5000,

  "trace": [
    {
      "event_id": "uuid",
      "source": "plaid_txn_123",
      "interpretation": "marketing expense",
      "confidence": 0.8
    }
  ]
}
```

---

## 7. Key APIs

### 7.1 Event Creation

`POST /events`

### 7.2 Interpret Event

`POST /events/{id}/interpret`

### 7.3 Generate Journals

`POST /projection/run`

### 7.4 Get Financial Statements

`GET /statements?pnl&start=...&end=...`

### 7.5 Audit Trail

`GET /audit/{statement_line_id}`

---

## 8. Data Storage Design

### Recommended Stack

* Event Store: PostgreSQL (append-only tables) or Kafka + OLAP store
* Vector DB: for embeddings (event similarity, classification)
* Object Storage: raw documents (PDFs, invoices)

---

## 9. Rollout Plan

### Phase 1

* Ingestion + QuickBooks sync
* Basic categorization (existing system)

### Phase 2

* Build Event Store
* Run parallel interpretations

### Phase 3

* Generate shadow financial statements
* Compare vs QuickBooks outputs

### Phase 4

* Introduce insights layer (anomalies, suggestions)

### Phase 5

* Transition to native-first, QuickBooks as export

---

## 10. Risks & Mitigations

### Risk: Accountant distrust

* Mitigation: full audit trail + explainability

### Risk: Incorrect AI interpretations

* Mitigation: confidence scores + human override

### Risk: Compliance errors

* Mitigation: strict projection layer validation

---

## 11. Success Metrics

* % of transactions auto-interpreted correctly
* Reduction in manual bookkeeping time
* Time to generate financial statements
* Auditor acceptance rate

---

## 12. Strategic Positioning

This system is not:

* bookkeeping automation

This system is:

> **A financial reality engine that compiles into accounting standards**
