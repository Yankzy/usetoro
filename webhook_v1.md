# Technical PRD

## Project: Webhook Ingestion Engine v1

### Internal Codename: Atlas Ingest

---

## 1. Purpose and Non-Goals

### Purpose

Build a **centralized webhook ingestion and event routing system** that:

* Reliably receives third-party events
* Normalizes and validates them
* Persists them durably
* Delivers them deterministically to customer-defined destinations
* Supports replay, audit, and local development via CLI

This system becomes the **single source of truth for inbound events**.

---

### Explicit Non-Goals (v1)

* No real-time analytics dashboards
* No customer-defined transformations beyond schema normalization
* No multi-tenant compute execution (no customer code execution)
* No UI-heavy workflow builder

Those come later.

---

## 2. High-Level Architecture

### Services

1. **Go Ingestion Gateway** (edge, performance-critical)
2. **Django Control Plane** (state, config, auth, lifecycle)
3. **Intelligence Service** (AI-assisted schema handling)
4. **Event Store** (durable log)
5. **Delivery Engine** (outbound fan-out)
6. **CLI (Go)** (local development and testing)

---

### Event Lifecycle (Canonical)

1. Event received
2. Event authenticated
3. Event persisted (raw)
4. Event normalized
5. Event validated
6. Event routed
7. Event delivered
8. Delivery acknowledged or retried
9. Event archived and replayable

Persistence happens **before any business logic**.

---

## 3. Go Ingestion Gateway (Core)

### Responsibilities

* Expose public webhook endpoints
* Authenticate incoming requests
* Accept extremely high concurrency
* Persist events immediately
* Return fast acknowledgments

This service must be **stateless**.

---

### Endpoints

#### `POST /v1/webhooks/{source}/{connection_id}`

* Accepts raw payload
* Headers preserved
* Body preserved verbatim

**Rules**

* Never block on downstream processing
* Acknowledge once persisted
* Do not validate schema here

---

### Authentication

* HMAC verification (Stripe-style)
* Shared secrets per connection
* Pluggable verifier interface

Failure modes:

* Invalid signature returns 401
* Malformed request returns 400
* Internal failure returns 202 only if persisted

---

### Persistence Contract

On success, the Go service must write:

* Raw payload
* Headers
* Source
* Connection ID
* Timestamp
* Request ID
* Idempotency key (derived)

Only after durable write can it respond `200 OK`.

---

### Output

Push event reference into the internal event pipeline (stream or queue).

---

## 4. Django Control Plane

### Responsibilities

* Tenant management
* Connection configuration
* Destination configuration
* Event state tracking
* Replay orchestration
* Access control
* Audit metadata

Django owns **truth and intent**, not performance paths.

---

### Core Models

#### Tenant

* id
* name
* plan
* limits

#### Connection

* tenant_id
* source_type (stripe, custom, github)
* secret
* status

#### Destination

* type (http, database, local, cli)
* config (encrypted)
* retry_policy

#### Event

* id
* tenant_id
* source
* status
* created_at

#### DeliveryAttempt

* event_id
* destination_id
* status
* error
* attempt_number

---

### Control APIs

* Create connection
* Rotate secrets
* Register destination
* Trigger replay
* Inspect event history

No ingestion traffic goes through Django.

---

## 5. Intelligence Service (AI Layer)

### Purpose

Absorb **schema chaos** so customers do not.

This service never blocks ingestion.

---

### Responsibilities

* Infer event schema
* Detect breaking changes
* Normalize payloads into canonical forms
* Classify event types when missing or ambiguous

---

### Inputs

* Raw event payload
* Historical schema versions
* Source metadata

---

### Outputs

* Normalized JSON
* Schema version ID
* Confidence score
* Change classification:

  * backward compatible
  * additive
  * breaking

---

### AI Constraints

* Deterministic output required
* Versioned models
* Explainable diffs stored

No black-box mutation.

---

## 6. Event Store

### Requirements

* Append-only
* Ordered per connection
* Replayable
* Immutable raw events

Kafka-like semantics are assumed, even if implementation differs.

---

### Stored Artifacts

* Raw event
* Normalized event
* Metadata
* Delivery status

Retention policy configurable.

---

## 7. Delivery Engine (Go)

### Responsibilities

* Consume normalized events
* Apply routing rules
* Deliver to destinations
* Handle retries
* Ensure idempotency

---

### Delivery Types

#### HTTP

* Signed outbound requests
* Retry with backoff
* Dead-letter support

#### Database

* Batched inserts
* Transactional guarantees

#### Localhost (CLI)

* Forward to local agent
* Encrypted tunnel or signed stream

---

### Retry Semantics

* At-least-once delivery
* Idempotency keys mandatory
* Replay safe by design

---

## 8. CLI (Go)

### Purpose

Local development and debugging.

This is not optional. It is strategic.

---

### CLI Responsibilities

* Authenticate with platform
* Subscribe to event streams
* Forward events to localhost
* Replay historical events
* Simulate sources

---

### Commands

#### `ingest login`

Authenticate and store token securely.

#### `ingest listen`

Subscribe to live events and forward to local endpoint.

#### `ingest replay`

Replay selected events to local service.

#### `ingest trigger`

Send synthetic events for testing.

---

### Architecture

* Persistent connection to Go backend
* Event stream over gRPC or WebSocket
* Local forwarding via HTTP

CLI mirrors production behavior exactly.

---

## 9. Cross-Cutting Concerns

### Security

* Secrets encrypted at rest
* Signed internal messages
* Tenant isolation enforced everywhere

---

### Observability

* Correlation IDs end-to-end
* Event-level tracing
* Delivery metrics per destination

---

### Failure Philosophy

* Never lose events
* Prefer duplication over loss
* Replay is the escape hatch

---

## 10. V1 Success Criteria

* Zero event loss under load
* Replay works without side effects
* CLI mirrors production behavior
* Customers never touch webhook code

---

## 11. Why This Architecture Matters

This design:

* Turns webhooks into **data infrastructure**
* Shifts responsibility from customer to platform
* Creates long-term lock-in through trust, not friction

---

This is the **technical backbone**.
No fluff. No premature features.

When you are ready, say **NEXT** and we will design the **first automatic sales email**, tightly aligned with:

* Eliminate
* Reduce
* Raise
* Create

Exactly as you requested.
