# Change Data Capture (CDC) in Toro

Toro uses two distinct CDC pipelines that serve different purposes. Understanding both — and how each prevents circular write loops — is essential before wiring up new consumers.

---

## 1. QBO CDC (QuickBooks Online Webhook Echo Prevention)

### What it is

QuickBooks Online sends CloudEvents webhooks to Toro whenever data changes in the customer's QBO company (accounts, vendors, customers, invoices, bills). Toro fetches the changed entity from the QBO API and upserts it into the `shadow_erp` schema (the local mirror).

The problem: **QBO fires a new webhook for every write, including writes made by Toro itself**. If Toro upserts an account in response to a webhook, QBO sends another webhook for the same account — causing an infinite fetch-and-upsert loop.

### Data flow

```
QBO API change
    → POST /webhooks/qbo/{conn_id}          (handler.go)
    → NATS: qbo.events.webhook              (published by HandleWebhook)
    → Worker.processQBOWebhook              (worker.go / webhook_processor.go)
    → QBOConnector.FetchEntity              (qbo.go)
    → QBO API fetch (by entity ID)
    → shadow_erp.accounts / vendors / etc.  (upsert)
    → QBO fires ANOTHER webhook             ← loop risk
```

### The fix: SyncToken comparison

QBO assigns a monotonically increasing `SyncToken` integer to every entity. Each time an entity is modified, its `SyncToken` increments. Toro stores the token locally alongside the row.

Before upserting, `FetchEntity` compares the local `SyncToken` against the fetched entity's token:

```go
// go/internal/connectors/qbo.go
func shouldSkipSync(localToken, remoteToken string) bool {
    localInt, err1 := strconv.Atoi(localToken)
    remoteInt, err2 := strconv.Atoi(remoteToken)
    if err1 == nil && err2 == nil {
        return localInt >= remoteInt
    }
    return localToken == remoteToken
}
```

If `local >= remote`, Toro already has this version (or newer) — the webhook is an echo of Toro's own write. The event is logged and skipped.

### Entities covered

Accounts, Vendors, Customers, Invoices, Bills. Each has a `sync_token TEXT NOT NULL` column in `shadow_erp`.

---

## 2. Postgres CDC (WAL → NATS `ledger.*` Event Source Guard)

### What it is

Toro's `cdc-worker` service tails the PostgreSQL Write-Ahead Log (WAL) via logical replication and publishes every row-level change to NATS JetStream. This is the internal event bus for Toro's own services — it is separate from the QBO webhook pipeline.

The problem: **every write to `shadow_erp` — regardless of who caused it — produces a `ledger.*` NATS event**. If a `ledger.*` consumer reacts by writing back to `shadow_erp`, the CDC worker picks up that write and publishes another event, creating a loop.

### Data flow

```
Any writer (QBOConnector, CPA approval, AI engine, etc.)
    → INSERT/UPDATE shadow_erp.*
    → Postgres WAL (toro_ledger_pub publication)
    → cdc-worker reads WAL via pgoutput logical replication
    → NATS: ledger.<table>.<action>         (e.g. ledger.accounts.update)
    → ledger.* consumer reacts
    → writes back to shadow_erp.*           ← loop risk
```

### The publication

`toro_ledger_pub` (defined in `sql/schema/005_logical_publication.sql`) watches:
- `toro_core.users`
- `toro_core.qbo_connections`
- All tables in `shadow_erp` schema

NATS subject format: `ledger.<table_name>.<action>` (e.g. `ledger.proposed_transactions.update`).

### The fix: `event_source` column

Every `shadow_erp` table has an `event_source TEXT NOT NULL DEFAULT 'toro_internal'` column (added in `sql/schema/010_event_source.sql`). All write queries hardcode the correct value directly in SQL — no application-layer parameter is required.

| Writer | `event_source` value | SQL location |
|---|---|---|
| `QBOConnector` upserts (webhook, CDC, full sync) | `'qbo_sync'` | `UpsertAccount`, `UpsertVendor`, `UpsertCustomer`, `UpsertInvoice`, `UpsertBill`, `UpsertCompanyInfo` in `sql/queries/qbo.sql` |
| `QBOConnector` soft-deletes | `'qbo_sync'` | `SoftDeleteAccount` … `SoftDeleteBill` |
| CPA approval | `'toro_internal'` | `ApproveProposedTransaction` |
| Transaction status update | `'toro_internal'` | `UpdateProposedTransactionSyncStatus` |
| AI pipeline insert | `'toro_internal'` | `CreateProposedTransaction` |
| Any other internal write | `'toro_internal'` | Column `DEFAULT` — no explicit SET needed |

### How the CDC decoder exposes the source

`go/internal/cdc/publisher.go` promotes `event_source` from the raw row data into the top-level `Event.Source` field before publishing to NATS:

```go
// cdc/publisher.go — runs before json.Marshal
if src, ok := event.Data["event_source"].(string); ok {
    event.Source = src
}
```

The `Event` struct (`cdc/decoder.go`) provides a helper method consumers call:

```go
type Event struct {
    EventID   string         `json:"event_id"`
    Table     string         `json:"table"`
    Action    string         `json:"action"`
    Source    string         `json:"source"`  // "qbo_sync" | "toro_internal"
    Timestamp time.Time      `json:"timestamp"`
    Data      map[string]any `json:"data"`
}

func (e *Event) IsInternal() bool {
    return e.Source == "toro_internal"
}
```

### Consumer guard pattern

Every `ledger.*` consumer **must** begin with this check:

```go
func (w *Worker) handleLedgerEvent(msg *nats.Msg) {
    var event cdc.Event
    if err := json.Unmarshal(msg.Data, &event); err != nil {
        msg.Nak()
        return
    }

    // Break the write-back loop: skip events caused by our own writes.
    if event.IsInternal() {
        msg.Ack()
        return
    }

    // Safe to react — this event originated from an external system (e.g. QBO).
    // ...
}
```

### Flow after the fix

```
QBOConnector upserts account (event_source = 'qbo_sync')
    → WAL → cdc-worker → NATS ledger.accounts.update (Source = "qbo_sync")
    → Consumer: IsInternal() = false → reacts

Consumer reacts → writes to proposed_transactions (event_source = 'toro_internal')
    → WAL → cdc-worker → NATS ledger.proposed_transactions.insert (Source = "toro_internal")
    → Consumer: IsInternal() = true → Ack, skip → loop stopped
```

---

## Side-by-side comparison

| | QBO CDC | Postgres CDC |
|---|---|---|
| **Trigger** | QBO API change fires webhook to Toro HTTP endpoint | Any `shadow_erp` write generates a WAL event |
| **Transport** | NATS `qbo.events.*` | NATS `ledger.*` |
| **Echo source** | QBO re-notifies after Toro's own API write | `cdc-worker` re-publishes Toro's own DB write |
| **Guard mechanism** | `SyncToken` integer comparison (`shouldSkipSync`) | `event_source` column + `event.IsInternal()` |
| **Guard lives in** | `QBOConnector.FetchEntity` (`connectors/qbo.go`) | Every `ledger.*` consumer |
| **What triggers the guard** | Remote `SyncToken` ≤ local `SyncToken` | `event.Source == "toro_internal"` |

---

## Adding a new `ledger.*` consumer checklist

1. Start handler with `event.IsInternal()` check — Ack and return if true.
2. If the handler writes to `shadow_erp`, the column `DEFAULT 'toro_internal'` handles it automatically — no extra work needed.
3. If the handler writes to `shadow_erp` on behalf of an external system, explicitly set `event_source = 'qbo_sync'` in the SQL query.
4. Never write to tables inside `toro_ledger_pub` without setting `event_source`.
