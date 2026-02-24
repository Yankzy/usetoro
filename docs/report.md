## Toro Platform — Implementation Report (Engineering)

Prepared for: CTO  
Date: 2026-02-24  
Repository: `usetoro`

This report documents what is currently implemented in the codebase, with emphasis on the recently completed QBO integration hardening, the CPA review loop, month-end reconciliation, OTP-based auth flows, and the TAP settlement engine.

---

## Executive summary

The project now has end-to-end plumbing to (1) connect a client entity to QuickBooks Online via OAuth, (2) ingest QBO webhooks and perform event-driven CDC-style syncing into a shadow ERP schema, (3) tail Postgres WAL via logical replication and publish row-level ledger events to NATS JetStream, (4) run month-end reconciliation using official QBO financial reports (Balance Sheet + Profit & Loss) and return a structured discrepancy report, and (5) close the “human-in-the-loop” CPA feedback loop by allowing approval/correction of AI-proposed transactions via REST. In parallel, the GraphQL API gained production-grade OTP login flows (Redis-backed, constant-time comparison, email delivery), and the TAP settlement engine’s security posture was upgraded to real Ed25519 signature verification with Redis-backed contract persistence and JetStream durable consumers.

---

## What’s implemented (by capability)

### QuickBooks Online (QBO) integration

The QBO connector and client library support both initial full sync and incremental CDC sync patterns, with reliability controls to tolerate QBO throttling and transient failures.

- **OAuth callback ingestion (REST)**:
  - Processes `code`, `realmId`, and `state` in a small pipeline (extract → exchange → persist → notify → redirect).
  - Accepts `state` as either:
    - A legacy entity UUID, or
    - A signed JWT (verified via the authenticator) from a frontend-initiated flow.
- **Connection success notification**:
  - After OAuth success, the server publishes a QBO “connected” event for downstream automation (e.g., initial syncing).
- **Event-driven sync worker behavior**:
  - When a QBO “connected” event is received, the worker triggers:
    - `SyncCompanyInfo` (single API call, cached),
    - Full Chart of Accounts sync,
    - Full Customers sync.
- **Company metadata caching**:
  - Company profile data is mirrored into `shadow_erp.company_info` (one row per `realm_id`) to provide fast reads (e.g., UI display of connected company name) and avoid repeated QBO lookups.
- **Entity sync into shadow ERP**:
  - Supported entity types in the connector’s upsert path:
    - Accounts, Vendors, Customers, Invoices, Bills.
  - Numeric/date fields for invoices/bills are converted into proper Postgres types (numeric + date).
  - Soft-deletes are handled for the supported entity types (local `deleted_at` stamping).
- **CDC sync strategy (incremental, QBO API CDC)**:
  - CDC lookback is driven by per-entity “last webhook timestamp” fields (Account/Vendor/Customer/Invoice/Bill) when available, with a fallback timestamp and a hard cap for QBO’s 30-day limit.
- **Performance and resilience in the QBO HTTP client**:
  - Rate limiting, concurrency limiting, and a circuit breaker are built into the QBO client request path.
  - Automatic OAuth token refresh is supported via an `oauth2.TokenSource`, with a callback that persists refreshed tokens.
- **Financial report retrieval support**:
  - QBO report parsing structures and client methods exist for:
    - Balance Sheet (`reports/BalanceSheet`)
    - Profit & Loss (`reports/ProfitAndLoss`)
    - General Ledger (`reports/GeneralLedger`)

### Postgres WAL CDC pipeline (logical replication → NATS JetStream)

In addition to “upstream” CDC from QBO, the platform includes an internal CDC pipeline that tails Postgres WAL using logical replication and publishes an append-only change stream into JetStream. This is the foundation for an “executable ledger” event backbone.

- **Database configuration (logical replication enabled)**:
  - Development Compose starts Postgres with `wal_level=logical`, `max_replication_slots=5`, and `max_wal_senders=5`.
  - A Goose migration creates a logical publication named `toro_ledger_pub` that includes:
    - `toro_core.users`
    - `toro_core.qbo_connections`
    - All tables in the `shadow_erp` schema (added dynamically).
- **CDC worker service**:
  - Standalone binary (`go/cmd/cdc-worker`) with no HTTP surface area.
  - Uses `pglogrepl` to:
    - Create/ensure a persistent replication slot `toro_nats_slot` (plugin `pgoutput`),
    - Start replication against the `toro_ledger_pub` publication,
    - Receive and decode row-level change messages (INSERT/UPDATE/DELETE).
- **Event envelope + routing**:
  - Decodes pgoutput messages into a normalized JSON event with:
    - `event_id` = Postgres LSN string,
    - `table`, `action`, `timestamp`,
    - `data` map derived from tuples + relation metadata.
  - Publishes synchronously to JetStream subject: `ledger.<table>.<action>` (action lowercased).
- **Delivery semantics (practical exactly-once in JetStream)**:
  - Publishes with `Nats-Msg-Id = LSN` so JetStream deduplicates duplicates across retries/crashes.
  - If JetStream publish/ack fails, the worker exits without advancing its WAL cursor; on restart Postgres resends retained WAL from the slot.

### Month-end reconciliation (“Final exam”)

Month-end reconciliation compares QBO financial reports against the shadow ERP’s local state and produces a structured discrepancy report.

- **Inputs**:
  - QBO Balance Sheet and Profit & Loss reports, optionally constrained by `start_date` / `end_date` (YYYY-MM-DD).
  - Local accounts for the realm from `shadow_erp.accounts`.
- **Processing**:
  - A recursive row traversal walks QBO’s report row tree and extracts account IDs + amounts.
  - Accounts are indexed into an O(1) map keyed by QBO account ID to avoid repeated scans.
  - Comparisons use a small half-cent epsilon to absorb rounding drift.
- **Outputs**:
  - `DiscrepancyReport` with:
    - `TotalChecked` counters,
    - A list of discrepancies (e.g., missing local account, balance mismatch),
    - A boolean `IsReconciled`.
- **Important current behavior**:
  - Balance Sheet rows perform **presence + amount** checks (running balances).
  - Profit & Loss rows currently perform **presence-only** checks (period totals are not directly comparable to the shadow DB’s running balance field without additional modeling).

### CPA review loop (human-in-the-loop corrections)

The REST API supports a CPA workflow to approve/correct AI-proposed transactions and to trigger reconciliation runs.

- **Approve or correct a proposed transaction**:
  - Endpoint updates `shadow_erp.proposed_transactions`:
    - `predicted_account_id` is required,
    - `predicted_vendor_id` is optional,
    - `sync_status` transitions to `APPROVED`.
- **Trigger reconciliation**:
  - Endpoint triggers `ReconcileMonth` for a realm and returns the discrepancy report JSON.

### GraphQL authentication: OTP request + verification

The GraphQL API now includes a complete OTP flow that is suitable for production use (with Redis and email delivery).

- **`requestOTP(email)`**:
  - Generates a cryptographically secure 6-digit OTP.
  - Stores OTP in Redis with 10-minute TTL under `otp:<email>`.
  - Avoids user enumeration by not failing the mutation when the user doesn’t exist (only fails on internal errors).
  - Sends the OTP using an injected `EmailSender`.
- **`verifyOTP(email, otp)`**:
  - Fetches OTP from Redis; errors if expired/missing.
  - Uses constant-time compare to prevent timing attacks.
  - Deletes the OTP immediately (single-use).
  - Issues standard access/refresh tokens and persists refresh tokens (DB + Redis caching).

### TAP settlement engine (protocol / contracts / proofs)

The settlement engine is implemented as a service that consumes JetStream subjects, persists contract state, validates signatures, and settles contracts upon proof submission.

- **JetStream + durable consumers**:
  - Creates/ensures the settlement stream (config-driven).
  - Subscribes using durable consumers with manual ack (at-least-once semantics).
  - Detects consumer mismatch and recreates consumers when necessary.
- **Redis-backed persistence**:
  - Contracts are stored in Redis as JSON with no TTL to preserve an audit trail.
- **Cryptographic verification**:
  - Contract lock validates both party signatures.
  - Proof settlement validates proof type and verifies the proof signature using Ed25519, derived from `did:toro:<pubkeyHex>`.
- **Oracle signing for external ingestion**:
  - The settlement engine can generate/load an oracle Ed25519 keypair.
  - A Samsara webhook ingest path creates proofs and signs them with the oracle keypair before submission.

---

## API surface area (implemented)

### REST (Ingress service)

Endpoints currently registered:

- **Health**
  - `GET /health` (legacy alias for liveness)
  - `GET /health/live`
  - `GET /health/ready` (checks DB + NATS publisher health)
- **Webhooks**
  - `POST /webhooks/{provider}/{conn_id}` (generic verifier registry)
  - `POST /webhooks/stripe/{conn_id}` (backward-compatible wrapper)
- **QBO OAuth**
  - `GET /auth/qbo/callback`
- **CPA review + reconciliation**
  - `POST /transactions/{id}/approve`
  - `POST /reconcile/{realmId}` (optional `start_date`, `end_date` query params)

Behavioral notes:

- Webhook ingestion includes request-ID tracing, size limits, signature verification, rate limiting, failed-auth blocking, and JetStream publish with retry.
- The approval and reconciliation endpoints are dependency-injected through the server’s handler wiring.

### GraphQL (gqlgen service)

Notable implemented mutations/queries (non-exhaustive; focused on recently completed work):

- **Auth**
  - Mutations: `signup`, `login`, `requestOTP`, `verifyOTP`, `refreshToken`, `deleteToken`
- **QBO sync + QBO data access**
  - Queries: `qboConnection`, account/customer/vendor listing queries (by realm/tenant/entity)
  - Mutations: QBO sync calls and account CRUD (`createQboAccount`, `updateQboAccount`, `softDeleteQboAccount`)
- **AI feedback**
  - Mutation: `recordCorrection`
  - Query: `suggestAccounts`, `resolveEntity`

---

## Data model & persistence changes

The following database-layer capabilities are implemented (SQL + sqlc bindings + Go usage).

- **New/extended shadow ERP tables**
  - `shadow_erp.company_info` (migration `sql/schema/009_company_info.sql`)
    - Stores a mirror of QBO CompanyInfo, keyed by `realm_id`.
    - Includes JSONB address blobs and name/value preference bags for flexible schema.
- **Logical replication publication (WAL CDC)**
  - `toro_ledger_pub` (migration `sql/schema/005_logical_publication.sql`)
    - Streams `toro_core.users`, `toro_core.qbo_connections`, and all `shadow_erp.*` tables via logical decoding.
- **sqlc query surface additions**
  - Transaction approval:
    - `GetProposedTransactionByID`
    - `ApproveProposedTransaction`
  - Company info:
    - `UpsertCompanyInfo`
    - `GetCompanyInfo`
  - Event-driven CDC support:
    - Updates for `last_webhook_*` timestamps on the QBO connection row.

---

## Reliability, performance, and security posture

### Reliability & operations

The implemented approach favors at-least-once delivery + idempotent upserts.

- **JetStream durable consumers** are used for worker services (sync + settlement).
- **Postgres WAL tailing → JetStream** uses a replication slot (WAL retention while disconnected) and LSN-based JetStream deduplication (replay-safe publishing).
- **Consumer mismatch recovery** is implemented by deleting/recreating consumers when subjects/configs drift.
- **Connector batching** uses DB transactions for local upserts to reduce overhead and preserve consistency.
- **QBO HTTP client resilience**:
  - Rate limiting and concurrency limiting.
  - Circuit breaker to fail fast under persistent upstream issues.
  - Token refresh callback persists updated tokens without manual intervention.

### Security

- **Webhook verification**:
  - Provider-specific verifier registry, including HMAC verification for QBO (`intuit-signature`, SHA-256).
- **OTP security**:
  - Cryptographically secure OTP generation.
  - Constant-time OTP comparisons.
  - Short TTL + single-use deletion.
  - Reduced user enumeration risk in `requestOTP`.
- **TAP cryptography**:
  - Real Ed25519 verification (no placeholder logic).
  - Proof signatures are enforced for settlement.

---

## Test coverage and verification

Unit tests were updated to align with the evolved handler dependency graph and OAuth callback parameter extraction behavior.

- **API handler tests**:
  - `go/internal/api/handler_test.go` covers webhook handling paths and expected statuses (including publish failure returning 503).
  - `go/internal/api/handler_qbo_test.go` covers OAuth `state` parsing as UUID vs. JWT (authenticator verification).

Suggested local verification steps:

- Run unit tests: `make test` (or `go test ./...`)
- Apply migrations and regenerate sqlc when needed:
  - `make migrate`
  - `make sqlc`

---

## Known gaps, risks, and next steps

### Gaps / risks

- **P&L amount reconciliation**: P&L is currently presence-only to avoid false mismatches; to reconcile amounts, the shadow schema needs period-bounded aggregates or a separate “activity totals” source of truth.
- **Company name display**: `qboConnection.companyName` is populated from `shadow_erp.company_info` when available; if not yet synced it currently returns an empty string (schema requires a non-null string).
- **Operational sequencing**: the `shadow_erp.company_info` migration must be applied and sqlc code regenerated/committed for environments that are behind.
- **Replication slot operational risk (WAL disk growth)**: if `cdc-worker` is down or lagging, the `toro_nats_slot` replication slot can cause Postgres to retain WAL segments; this needs monitoring (replication lag + disk usage) in any persistent environment.

### Next steps (high-value)

- **P&L parity**: introduce period totals (either by storing GL lines or maintaining monthly rollups) so P&L amounts can be reconciled with the same rigor as Balance Sheet.
- **Broaden report-based validation**: leverage General Ledger report support to improve explainability of discrepancies.
- **Harden delivery dependencies**: monitor OTP email delivery errors and introduce bounded retries/queueing if needed (keep user-facing latency low).
- **End-to-end smoke tests**: add a light integration test path for “connect QBO → sync → approve tx → reconcile” in a staging environment.

