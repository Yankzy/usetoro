# Clean-Up Mode Pipeline

## Philosophy

CPAs spend 30–40 hours on "messy books" engagements — hunting through thousands of uncategorized transactions from bank feeds, manually assigning vendors and accounts. Clean-Up Mode reduces that to a 2-hour review by front-loading the tedious work onto an AI clerk.

The core principle is **purgatory before permanence**: raw data never touches the live shadow ERP tables or QuickBooks until a human has explicitly blessed it. All work happens in a temporary staging table that mirrors the shape of the live tables but carries no production weight. The CPA reviews AI suggestions, approves, overrides, or rejects each row, and only then are transactions committed to QBO.

A secondary principle is **graceful degradation**: if a CPA has no QuickBooks connection (a prospect evaluating the tool, or a firm on a new engagement), Clean-Up Mode still runs fully — the AI enriches the data, the CPA reviews it, and they download a polished Excel file. QBO posting is simply disabled. No feature is gated on a connection that may not yet exist.

---

## End-to-End Flow

```
CPA uploads CSV/XLSX
        │
        ▼
POST /cleanup/upload (gate service)
        │  parse → validate → DB transaction
        ▼
cleanup_sessions  ──────────────────────────────────────────────┐
cleanup_staging (N rows, status = PENDING)                       │
        │                                                        │
        │  if realm_id set: publish cleanup.enrich.{realmID}    │
        ▼                                                        │
NATS JetStream (CLEANUP stream)                                  │
        │                                                        │
        ▼                                                        │
CleanupEnricher (graphql service, background goroutine)         │
        │                                                        │
        │  per-row pipeline (≤5 concurrent workers):            │
        │    Layer 0 → ai_corrections lookup (learning memory)  │
        │    Layer 1 → EntityResolver  (vendor)                 │
        │    Layer 2 → CoAMapper       (account)                │
        │    Post-pass → Deduplicator (duplicate + recurring)   │
        │                                                        │
        │  persist → cleanup_staging (status = ENRICHED)        │
        │  session → status = ENRICHED                          │
        │                                                        │
        ▼                                                        │
CPA Review Dashboard (GraphQL API)                              │
        │  queries:  cleanupSessions, cleanupRows               │
        │  mutations: approveCleanupRow                         │
        │             overrideCleanupRow  ─► EntityResolver.Learn│
        │             rejectCleanupRow                          │
        │             approveAllByVendor (bulk)                 │
        │             postCleanupSession                        │
        │                                                        │
        ▼             if realm_id IS NULL                       │
POST to QBO ◄────────────────────────────────────────────────── ┘
(QBO Batch API, 30 ops/call)          │
                                      └─► download Excel instead
        │
        ▼
GET /cleanup/{id}/export   →  4-sheet XLSX
GET /cleanup/{id}/audit    →  PDF audit report
```

---

## Database Schema

**Migration:** `sql/schema/011_cleanup_mode.sql`

### CDC exclusion

Both tables are **explicitly excluded from `toro_ledger_pub`** via a `DO $$` guard at the bottom of migration `011`. They are transient scratch space — every upload, enrichment tick, and CPA click generates writes, which would flood `ledger.*` NATS subjects with noise that no consumer should react to. They also lack the `event_source` column that the `IsInternal()` guard pattern requires, so accidental inclusion would be unsafe.

The guard is idempotent: it only calls `ALTER PUBLICATION … DROP TABLE` when the tables are actually present in the publication (the defensive case where migration `005` was re-run after `011` had already created the tables).

### `shadow_erp.cleanup_sessions`

One row per upload. Tracks the lifecycle of the entire batch.

| Column | Type | Notes |
|---|---|---|
| `id` | UUID PK | Auto-generated |
| `realm_id` | TEXT **nullable** | NULL = Excel-only (no QBO) |
| `created_by` | UUID FK → `toro_core.users` | The CPA who uploaded |
| `file_name` | TEXT | Original filename |
| `row_count` | INT | Total rows parsed |
| `status` | TEXT | `PENDING → ENRICHING → ENRICHED → POSTED` |

### `shadow_erp.cleanup_staging`

One row per raw transaction. Accumulates AI predictions and CPA decisions.

| Column | Type | Notes |
|---|---|---|
| `realm_id` | TEXT **nullable** | Mirrors session's realm_id |
| `raw_*` | TEXT / DECIMAL / DATE | Verbatim from the uploaded file |
| `predicted_vendor_id` | UUID FK → `vendors` | AI best guess |
| `predicted_account_id` | UUID FK → `accounts` | AI best guess |
| `normalized_vendor` | TEXT | Canonical name after entity resolution |
| `confidence_score` | DECIMAL(3,2) | 0.00–1.00 combined score |
| `ai_reasoning` | TEXT | Human-readable rationale |
| `duplicate_of` | UUID FK → self | Set when dedup detects a duplicate |
| `is_recurring` | BOOLEAN | True when same vendor+amount ≥3× in session |
| `split_suggestion` | JSONB | `[{"account_id":"…","amount":…}]` |
| `override_vendor_id` | UUID FK → `vendors` | CPA correction |
| `override_account_id` | UUID FK → `accounts` | CPA correction |
| `qbo_transaction_id` | TEXT | Populated after successful QBO post |
| `status` | TEXT | `PENDING → ENRICHED → APPROVED/REJECTED → POSTED` |

**Indexes:** partial indexes on `realm_id` (`WHERE realm_id IS NOT NULL`) keep index size minimal for the common case where realm is present, while a separate `created_by` index serves the Excel-only listing path.

---

## Row Status Lifecycle

```
PENDING
  └── (enricher runs) ──► ENRICHED
                              ├── approveCleanupRow     ──► APPROVED
                              ├── overrideCleanupRow    ──► APPROVED
                              └── rejectCleanupRow      ──► REJECTED
                                       APPROVED
                                          └── postCleanupSession ──► POSTED
```

---

## Ingestion Engine

**File:** `go/internal/api/upload_handler.go`  
**Endpoint:** `POST /files/upload` (gate service, `go/internal/api/router.go`)

### What it does

1. Authenticates the CPA via Ed25519 JWT (`extractUserID`).
2. Parses a `multipart/form-data` body. `realm_id` is **optional** — absence triggers Excel-only mode.
3. Dispatches to `parseCSV` or `parseXLSX` based on file extension (20 MiB cap, 10 000 row limit).
4. Inserts `cleanup_sessions` + all `cleanup_staging` rows inside a single `pgxpool` transaction — atomicity ensures no orphan rows if something fails mid-insert.
5. If `realm_id` is set, publishes a `cleanup.enrich.{realmID}` message to NATS JetStream (best-effort, non-fatal if NATS is unavailable).
6. Returns `{"session_id": "…", "status": "PENDING"}` immediately — the enrichment is asynchronous.

### Header normalization

The parser tolerates messy real-world exports (bank CSV, QBO export, Excel template) by mapping a range of synonyms to four canonical keys:

| Canonical | Accepted headers |
|---|---|
| `date` | `date`, `txn date`, `transaction date`, `post date` |
| `description` | `description`, `memo`, `details`, `narration`, `transaction` |
| `amount` | `amount`, `debit`, `credit`, `value`, `total` |
| `vendor` | `vendor`, `payee`, `merchant`, `name` |

### Date parsing

12 date layouts are tried in order (ISO 8601, US, European, named month). Rows with unparseable dates still insert — `raw_date` is left NULL rather than rejecting the row.

---

## AI Enrichment Worker

**Files:**  
- `go/internal/services/cleanup/enricher.go` (orchestration)  
- `go/internal/services/cleanup/deduplicator.go` (smart discovery)

**Runtime:** started as a background goroutine inside `go/cmd/graphql/server.go`, alongside the GraphQL server.

### NATS wiring

The worker creates (or reuses) a JetStream stream named `CLEANUP` covering subject `cleanup.>`. A durable push consumer (`toro-cleanup-enricher`) subscribes to `cleanup.enrich.>` with `ManualAck` and `AckExplicit`. On enrichment failure the message is `Nak()`-d for redelivery; a poison-pill message (bad payload) is `Ack()`-d immediately to prevent infinite loops.

### Per-session concurrency model

```
rawRows  ────► [channel semaphore, size=5]
                    │
              ┌─────┴─────┐
              │ goroutine │  (up to 5 in flight)
              │ enrichRow │
              └─────┬─────┘
                    │
              errgroup.Wait()
                    │
              AnnotateDuplicates(enriched)
              AnnotateRecurring(enriched)
                    │
              persist each row → DB
```

`errgroup` provides context cancellation propagation. The semaphore channel (`chan struct{}`, capacity 5) limits concurrent OpenAI/Pinecone API calls to stay within rate limits without a third-party rate-limiter library.

Row failures are **non-fatal** — a failed row is stored with zero confidence and flagged for manual review; the rest of the session continues.

### Per-row pipeline (`enrichRow`)

```
Layer 0: ai_corrections lookup
         ▼ hit → use stored correction (confidence = 1.0), skip AI
Layer 1: EntityResolver.ResolveEntity (vendor)
         3-layer: PostgreSQL exact/synonym → Pinecone semantic → fuzzy ranking
         ▼
Layer 2: CoAMapper.MapDescriptionToAccount (account)
         Pinecone semantic search over chart-of-accounts embeddings
         ▼
Confidence = avg(vendorScore, accountScore) when both resolved,
             single score when only one resolved,
             0.0 when neither resolved.
```

Layer 0 is skipped entirely when `realm_id` is NULL (Excel-only session has no QBO chart of accounts to compare against). Layers 1 and 2 are also skipped, leaving the row in a zero-confidence state ready for manual categorization.

### Smart Discovery (`Deduplicator`)

Both passes operate **in-memory** on the enriched slice, after all API calls complete, to avoid N+1 DB round-trips.

**`AnnotateDuplicates`**  
Builds a `map[key]canonicalID` where `key = vendor|cents|date`. A ±1-day window is checked (3 candidate keys per row) to catch near-misses from slightly different posting dates. The first-seen row keeps `DuplicateOf = ""` (canonical); subsequent matches point to it.

**`AnnotateRecurring`**  
Counts `(vendor, rounded_amount)` signatures across non-duplicate rows. Any signature appearing ≥3 times in the session is flagged `IsRecurring = true`, signalling a probable subscription charge. Duplicates are excluded from the count to avoid false positives.

---

## Learning Loop

When a CPA overrides the AI's suggestion via `overrideCleanupRow`, the GraphQL resolver calls `EntityResolver.Learn(ctx, realmID, rawInput, correctionID, correctionType)`. This writes to the existing `shadow_erp.ai_corrections` table.

On the next session for the same realm, `enrichRow` queries that table first (Layer 0). If the same raw vendor name or description was corrected before, the stored answer is used directly — no API call, confidence = 1.0. The AI progressively adapts to each firm's naming conventions without any retraining.

---

## GraphQL API

**Schema:** `go/cmd/graphql/graph/schema.graphqls`  
**Resolvers:** `go/cmd/graphql/graph/schema.resolvers.go`  
**Helpers:** `go/cmd/graphql/graph/schema_helpers.go`

### Queries

| Query | Description |
|---|---|
| `cleanupSessions(realmId: ID)` | List sessions for a realm, or by `created_by` when `realmId` is omitted |
| `cleanupRows(sessionId: ID!, status: String)` | Rows for a session, optionally filtered by status |

### Mutations

| Mutation | Description |
|---|---|
| `approveCleanupRow(rowId)` | Mark a row `APPROVED` |
| `overrideCleanupRow(input)` | Set CPA vendor/account override → `APPROVED` + triggers learning loop |
| `rejectCleanupRow(rowId)` | Mark a row `REJECTED` |
| `approveAllByVendor(sessionId, vendorId)` | Bulk approve all `ENRICHED` rows for a given vendor |
| `postCleanupSession(sessionId)` | Post all `APPROVED` rows to QBO; fails fast if `realm_id IS NULL` |

### QBO batch posting (`postCleanupSession`)

1. Guard: if `session.RealmID` is NULL, return a user-friendly error directing the CPA to download Excel instead.
2. Fetches all `APPROVED` rows for the session.
3. Builds `quickbooks.Purchase` objects via `buildQBOPurchase` (resolves QBO account and vendor IDs from shadow tables).
4. Batches into groups of 30 (QBO Batch API limit) using `quickbooks.NewBatchBuilder`.
5. Executes each batch via `qboClient.BatchContext`.
6. Iterates responses: successes call `MarkRowPosted` (saves `qbo_transaction_id`), failures are collected and returned in `CleanupPostResult.errors`.
7. Updates session status to `POSTED` when all rows are done.

---

## Export Layer

**File:** `go/internal/services/cleanup/exporter.go`  
**REST endpoints:**
- `GET /cleanup/{session_id}/export` → XLSX (`HandleCleanupExport`)
- `GET /cleanup/{session_id}/audit` → PDF (`HandleCleanupAudit`)

### Excel export (`ExportExcel`)

4-sheet workbook generated with `github.com/xuri/excelize/v2`:

| Sheet | Contents |
|---|---|
| `Cleaned Transactions` | `APPROVED` and `POSTED` rows |
| `Flagged / Rejected` | `REJECTED`, `ENRICHED` (unreviewed), `PENDING` rows |
| `Duplicates` | All rows where `duplicate_of IS NOT NULL`, with the canonical row ID appended |
| `Summary` | Session metadata (ID, file, realm, row count, status, export timestamp) |

Vendor names prefer `normalized_vendor → predicted_vendor_name → raw_vendor_name` in that order, so the CPA always sees the cleanest available label. Account names prefer CPA override over AI prediction.

### PDF audit report (`ExportAuditPDF`)

Generated with `github.com/go-pdf/fpdf`. Used as a client-facing deliverable:

- **Page 1:** Session metadata + automation statistics (total rows, % automated, overrides, duplicates, recurring charges, posted count).
- **Page 2+:** Appendix A — table of every manually overridden transaction (date, description, amount, overridden account, AI reasoning). Auto-paginates when rows exceed the page.

The automation percentage is `(total - overridden) / total × 100`, giving the CPA a headline like "98% automated" to show their client.

---

## Dependency Injection & Wiring

```
cmd/gate/main.go
    └── cleanup.NewExporter(db)
    └── api.NewServer(natsClient, exporter)
            └── api.NewHandler(…, cleanupDB, natsClient, exporter)
                    ├── POST /cleanup/upload  → HandleFileIngestion
                    ├── GET  /cleanup/{id}/export → HandleCleanupExport
                    └── GET  /cleanup/{id}/audit  → HandleCleanupAudit

cmd/graphql/server.go
    └── cleanup.NewCleanupEnricher(db, entityResolver, coaMapper, nats, logger)
    └── go enricher.Start(ctx)   ← background goroutine
    └── graph.Resolver{ CleanupEnricher: enricher }
            └── GraphQL mutations/queries
```

The `CleanupExporter` interface in `go/internal/api/upload_handler.go` decouples the REST handler from the concrete `cleanup.Exporter` struct, enabling mock injection in tests.

---

## Key Files

| File | Role |
|---|---|
| `sql/schema/011_cleanup_mode.sql` | Migration: `cleanup_sessions` + `cleanup_staging` tables |
| `sql/queries/cleanup.sql` | 18 sqlc queries for the staging pipeline |
| `go/internal/database/cleanup.sql.go` | Auto-generated type-safe DB accessors |
| `go/internal/api/upload_handler.go` | REST: upload, export, audit endpoints |
| `go/internal/api/router.go` | Route registration |
| `go/internal/api/handler.go` | `Handler` struct with injected cleanup dependencies |
| `go/internal/services/cleanup/enricher.go` | NATS consumer + AI enrichment orchestration |
| `go/internal/services/cleanup/deduplicator.go` | In-memory duplicate + recurring detection |
| `go/internal/services/cleanup/exporter.go` | Excel (excelize) + PDF (fpdf) export |
| `go/cmd/graphql/graph/schema.graphqls` | GraphQL type definitions for Clean-Up Mode |
| `go/cmd/graphql/graph/schema.resolvers.go` | Resolver implementations |
| `go/cmd/graphql/graph/schema_helpers.go` | DB → GraphQL model mappers, QBO object builders |
| `go/cmd/graphql/server.go` | Enricher lifecycle (start / graceful shutdown) |
| `go/qbo/batch.go` | QBO `BatchItemResponse` with `Purchase` field |

---

## Engineering Decisions

### Staging table as purgatory
Rather than writing enriched data to the live `shadow_erp` tables, everything stays in `cleanup_staging` until explicitly approved. This means a crashed enricher run, a bad file, or a rejected AI guess has zero impact on production data. The staging table is expendable — it can be truncated, retried, or simply abandoned without consequence.

### Async enrichment via NATS
The HTTP upload endpoint returns in milliseconds. Enrichment (which involves multiple OpenAI/Pinecone API calls per row, potentially for thousands of rows) happens asynchronously. NATS JetStream provides at-least-once delivery with redelivery on failure, durable across service restarts. The CPA polls the session status via GraphQL to know when enrichment is complete.

### Semaphore-based concurrency
Rather than a worker pool with channels, a buffered `chan struct{}` semaphore inside `errgroup` keeps the goroutine count bounded at 5 without additional abstractions. `errgroup` handles context propagation and error collection. Individual row failures are downgraded to warnings to preserve partial results.

### In-memory dedup after AI pass
Running `AnnotateDuplicates` and `AnnotateRecurring` after all API calls complete (rather than per-row) lets both algorithms operate on the full enriched picture. Vendor IDs from entity resolution are available by then, making duplicate detection far more accurate than operating on raw strings alone. The cost is holding the full session's enriched rows in memory, which at 10 000 rows × ~300 bytes/row is ~3 MB — well within acceptable limits.

### Nullable `realm_id`
Making `realm_id` optional was a deliberate product decision: a CPA without a QBO connection should still get value from the cleanup pipeline (Excel export, AI categorization). The null check in `postCleanupSession` provides a clear, user-facing error message rather than a cryptic DB or API error. Partial indexes (`WHERE realm_id IS NOT NULL`) keep the realm-scoped query paths fast while adding zero overhead for the null case.

### Learning loop via `ai_corrections`
Reusing the existing `ai_corrections` table means the cleanup pipeline immediately benefits from corrections made anywhere else in the platform (e.g., during normal transaction review). There is no separate "cleanup memory" — corrections accumulate in one place and apply everywhere. `EntityResolver.Learn` is called on `overrideCleanupRow` so the signal is captured at the moment of human judgment.

### `sqlc.narg` for nullable params
Where a column is nullable (e.g., `realm_id`, `override_vendor_id`), sqlc's `narg` syntax generates a `pgtype.Text` / `pgtype.UUID` param that the Go compiler forces you to handle explicitly. This eliminates the class of bugs where an empty string is written as a non-null value, or where a nil pointer is silently coerced.
