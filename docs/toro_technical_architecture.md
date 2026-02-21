# Toro Technical Architecture

## Executive Summary

Toro is designed as a **high‑reliability financial data control plane**. Its architecture prioritizes durability, throughput isolation, and operational safety over convenience. The system deliberately separates high‑velocity ingestion from intelligent processing to ensure that no upstream volatility (bursty traffic, malformed payloads, third‑party instability) can compromise core data integrity.

The architecture follows a **Hybrid Muscle & Brain model**:
- **The Muscle (Go)** handles external pressure and scale.
- **The Brain (Django + Python)** handles correctness, intelligence, and domain logic.
- **The Glue (Redis Streams)** provides durable, ordered, back‑pressure‑aware communication between the two.

This separation allows Toro to absorb unpredictable real‑world financial traffic while maintaining deterministic, auditable outcomes.

---

## 1. Architectural Principles

Toro is built around the following non‑negotiable principles:

1. **Never Drop Financial Data**
   Every inbound event must be captured durably, even if downstream systems are unavailable.

2. **Isolate Volatility**
   External systems (Stripe, Plaid, QuickBooks, Slack, custom webhooks) are inherently unreliable. Their failure modes must never propagate into the core database or application layer.

3. **Optimize Each Layer for Its Job**
   - Network concurrency belongs in Go.
   - Business logic, AI processing, and schema evolution belong in Python.
   - Coordination and buffering belong in Redis.

4. **Design for Audits and Replay**
   Every event must be traceable, replayable, and explainable months or years later.

---

## 2. High‑Level System Overview

Toro consists of three primary subsystems:

1. **Ingestion Layer (Go Services)**
2. **Message and Buffering Layer (Redis Streams)**
3. **Processing and Intelligence Layer (Django + Workers)**

All systems share a single source of truth: **PostgreSQL**, whose schema is fully owned by Django.

---

## 3. End‑to‑End Traffic Flow

### Step 1: External Event Ingestion

- External providers send HTTP webhooks to Toro endpoints.
- Example endpoint:
  `/hooks/{source}`

Sources include:
- Stripe
- Plaid
- QuickBooks
- Slack
- Internal systems
- Customer‑defined webhooks

### Step 2: Immediate Buffering

- The Go service immediately assigns a unique event ID.
- The raw payload is written verbatim to a Redis Stream.
- No validation, parsing, or transformation occurs at this stage.
- The HTTP request is acknowledged with **200 OK** immediately.

This ensures:
- Minimal latency
- No coupling to downstream health
- Protection against retry storms

### Step 3: Controlled Consumption

- Django workers consume events from Redis Streams using **consumer groups**.
- Consumption is throttled and back‑pressure‑aware.
- Each event is processed exactly once per consumer group.

### Step 4: Validation and Repair

- Events are validated against strict Pydantic schemas.
- If validation fails:
  - The raw payload is sent to an LLM with explicit schema constraints.
  - The LLM returns a corrected payload.
  - The corrected payload is re‑validated.

### Step 5: Persistence and Acknowledgement

- Clean, validated data is written to PostgreSQL.
- Metadata about repairs, retries, and transformations is stored alongside the event.
- The event is acknowledged in Redis (`XACK`).

---

## 4. The Muscle: Go Ingestion Layer

### Role

The Go layer acts as a **high‑performance shock absorber** between the internet and Toro’s internal systems.

### Responsibilities

- Accept extremely high request concurrency
- Handle slow or malicious clients safely
- Never perform business logic
- Never block on downstream services

### Key Characteristics

- Stateless HTTP services
- Goroutine‑based concurrency
- Minimal memory footprint
- Horizontal scalability

### Example Logic (Conceptual)

```go
func handleWebhook(w http.ResponseWriter, r *http.Request) {
    source := extractSource(r.URL.Path)
    body := readRequestBody(r)

    rdb.XAdd(ctx, &redis.XAddArgs{
        Stream: "toro:ingest",
        Values: map[string]interface{}{
            "source": source,
            "payload": body,
            "received_at": time.Now().UTC(),
        },
    })

    w.WriteHeader(http.StatusOK)
}
```

### Design Rationale

- Go is chosen for predictable latency under load.
- Fire‑and‑forget semantics prevent cascading failures.
- Any logic here would increase blast radius and is explicitly avoided.

---

## 5. The Glue: Redis Streams

### Role

Redis Streams serve as Toro’s **durable event log and pressure valve**.

### Why Redis Streams

- Ordered message delivery
- Consumer group support
- Persistence via AOF
- Low operational overhead
- Clear failure semantics

### Streams

- `toro:ingest` — Raw inbound events
- `toro:processed` — Successfully processed events
- Additional streams may exist for retries or dead‑letter handling

### Consumer Groups

- Multiple Django workers share a consumer group
- Each message is delivered to exactly one worker
- Unacked messages can be reclaimed

This allows horizontal scaling without duplication or loss.

---

## 6. The Brain: Django Processing Layer

### Role

The Django layer is the **authoritative decision‑maker**.

### Responsibilities

- Schema definition and ownership
- Validation and normalization
- AI‑assisted correction
- Database persistence
- Audit logging

### Worker Model

- Long‑running management command or Celery worker
- Batch consumption from Redis
- Explicit retry and failure handling

### Validation Strategy

- **Dynamic Schema Generation**: Instead of hardcoding Pydantic models, schemas are defined as data (JSON/YAML).
- **Metaprogramming**: The system dynamically constructs Pydantic models at runtime using `pydantic.create_model`.
- **User-Defined Schemas**: Customers can define or update the expected structure of their webhooks without code deployment.
- **Strict Validation**: Despite being dynamic, the generated models enforce strict type checking and validation rules.

### AI Repair Strategy

- LLM prompts include:
  - Exact schema
  - Field‑level constraints
  - Instructions to preserve semantic meaning
- Repaired payloads are always re‑validated
- Repair metadata is stored for auditability

---

## 7. Managed Integrations (The Concierge Layer)

Toro provides managed stateful integrations that abstract third‑party complexity.

### 7.1 Plaid Integration (Temporal + Go)

#### Problem

Plaid requires:
- Cursor‑based incremental sync
- Webhook‑driven updates
- Robust retry and idempotency

#### Solution

- Temporal workflows orchestrate Plaid sync lifecycles
- Go workers execute polling and persistence

#### Flow

1. Plaid webhook ingested by Go
2. Signal sent to Temporal workflow
3. Workflow resumes and polls Plaid API
4. Transactions are normalized and stored
5. Cursor state is persisted

Temporal provides durability, replay, and visibility into long‑running workflows.

---

### 7.2 QuickBooks Integration (Go + Celery)

#### Problem

- OAuth2 tokens expire frequently
- Rate limits and transient failures are common
- Invoice creation must be idempotent

#### Solution

- Go worker manages token refresh lifecycle
- Django/Celery handles invoice business logic

#### Flow

1. Internal request to create invoice
2. Celery task validates payload
3. API call to QuickBooks
4. On 401:
   - Signal token refresher
   - Retry with fresh token

---

## 8. Shared Database Strategy

### PostgreSQL as Source of Truth

- All schemas and migrations are owned by Django
- Strong consistency is required

### Access Rules

- Django has full read/write access
- Go services:
  - Prefer write‑buffer patterns
  - Use raw SQL only for high‑volume inserts
  - Avoid complex queries or schema ownership

This prevents schema drift and maintains a single authoritative data model.

---

## 9. Failure Handling and Safety Guarantees

Toro is explicitly designed to survive:

- Upstream retry storms
- Downstream database outages
- Malformed or malicious payloads
- Partial system failures

Safety mechanisms include:
- Redis buffering
- Idempotent processing
- Explicit acknowledgements
- Replay and reprocessing tools

---

## 10. Responsibility Matrix

| Capability | Technology | Rationale |
|---------|-----------|-----------|
| Ingestion API | Go | High concurrency, low latency |
| Event Buffering | Redis Streams | Durable, ordered, back‑pressure aware |
| Validation & AI | Django + Python | Rich ecosystem, schema control |
| Workflow Orchestration | Temporal | Durable long‑running state |
| Async Jobs | Celery | Reliable background execution |
| Database | PostgreSQL | Strong consistency, auditability |

---

## Closing Note

Toro is not optimized for convenience. It is optimized for **correctness under stress**.

This architecture exists to ensure that financial data remains accurate, traceable, and reliable even when everything around it fails.

