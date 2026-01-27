# Toro Technical Architecture: The Hybrid "Muscle & Brain" Model

## 1. High-Level Strategy
We do not try to embed Go inside Python. Instead, we run them as parallel services that share a Database (Postgres) and communicate via a Message Queue (Redis Streams).

- **The Muscle (Go)**: Sits at the edge. Receives high-volume webhooks (e.g. N8N, Slack, Stripe), handles ZERO logic, and instantly pushes to Redis.
- **The Brain (GO)**: Manages users, organization settings, and the "AI Analysis/Repair".
- **The Glue (Redis)**: Go pushes data into Streams; GO consumes, validates, and writes to Postgres.

### The Traffic Flow

1. **Ingest**: Webhook -> Go Service (`api.usetoro.io/hooks/{source}`).
2. **Buffer**: Go generates ID -> Pushes raw JSON to Redis Stream `toro:ingest`.
3. **Process**:
    - **Throttled Consume**: GO Worker reads `toro:ingest` (Consumer Group).
    - **AI Repair**: If JSON is valid -> Write to Postgres. If invalid -> Call LLM to repair -> Write to Postgres.

## 2. The Muscle: High-Performance Ingestion (Go)

**Role**: The Doorman. Fast, dumb, and resilient.
**Location**: `go/main.go`

- **Endpoint**: `/hooks/{source}` (Generic).
- **Behavior**: Zero validation. Immediate 200 OK. Pushes raw payload to Redis.
- **Concurrency**: Goroutines handle thousands of concurrent requests.

### Go Logic (Simplified)
```go
func handleWebhook(w http.ResponseWriter, r *http.Request) {
    // 1. Extract Source
    source := strings.Split(r.URL.Path, "/")[2]
    
    // 2. Queue to Redis (Fire & Forget)
    rdb.XAdd(ctx, &redis.XAddArgs{
        Stream: "toro:ingest",
        Values: map[string]interface{}{"source": source, "payload": body},
    })
    
    // 3. Respond
    w.WriteHeader(http.StatusOK)
}
```

## 3. The Glue: Redis Streams (Persistence)

**Role**: Storage & Durability.
**Streams**: `toro:ingest`, `toro:processed`
**Persistence**: Redis AOF (Append Only File) ensures safe "In-Transit" storage.
**Mechanism**: Consumer Groups allow multiple Django workers to process the stream without duplication.

## 4. The Brain: Intelligent Processing (Django)

**Role**: The Analyst.
**Location**: `webhookks/management/commands/run_consumer.py`

### Logic Flow
1. **Fetch**: Reads batch from `toro:ingest`.
2. **Validate**: Checks against Pydantic schema (e.g. `PaymentSchema`).
3. **AI Repair**: 
    - If validation fails, payload is sent to LLM: "Fix this JSON to match schema X".
    - Returns corrected JSON.
4. **Write**: Saves clean data to Postgres (`toro_webhook_events` table).
5. **Ack**: Sends `XACK` to Redis to confirm processing.

This isolates the Database from the high-velocity ingestion layer ("The Airbag" effect).

## 5. The Concierge: Managed Integrations (Plaid & QuickBooks)

*The Partner: Handling complex 3rd-party state machines so the user doesn't have to.*

### 5.1 Plaid Sync (Temporal + Go)
**Problem**: Syncing bank transactions requires cursors, webhooks, and complex retries.
**Solution**: Temporal Workflows manage the entire lifecycle.

**Workflow**:
1. **Trigger**: `SYNC_UPDATES_AVAILABLE` webhook -> Go Ingest.
2. **Signal**: Go signals Temporal Workflow.
3. **Execution**: Workflow wakes up -> Polling Loop (Plaid API) -> Save to Postgres.
4. **Push**: Forwards normalized transactions to User.

### 5.2 QuickBooks Sync (Available Worker Pool)
**Problem**: OAuth2 tokens expire every hour; invoices need retry logic.
**Solution**: A dedicated Go Worker for Token Management & Django/Celery for Invoice Logic.

**Components**:
- **Token Refresher (Go)**: Periodically scans DB for expiring tokens -> Refreshes via Intuit API -> Updates DB.
- **Invoice Worker (Celery)**:
    1.  Receives "Create Invoice" task from internal API.
    2.  Validates payload.
    3.  Uses valid token to POST to QuickBooks Online.
    4.  On 401 (Unauthorized): Signals Token Refresher + Retries.

## 6. Shared Database Strategy

**The Golden Rule:**
- **Django owns the schema** (Models, Migrations).
- **Go treats the database as "Read-Only"** mostly, or uses simple raw SQL for high-volume inserts (like the Write Buffer).

## 7. Responsibilities Summary

| Feature | Technology | Why? |
| :--- | :--- | :--- |
| **Ingest API** | Go | High concurrency (10k+ req/sec), low RAM. |
| **Tunnel Server** | Go | Efficient WebSocket management. |
| **Message Bus** | **Redis Streams** | Persistent, ordered log for Go <-> Python. |
| **AI Processing** | Django (Celery) | Access to Python AI ecosystem. |
| **Plaid Sync** | **Temporal + Go** | Durable execution, retry logic, state management. |
| **QuickBooks** | **Go + Celery** | Token refreshing (Go) & Invoice logic (Python). |
| **Write Buffer** | Go Worker | Connection pooling, protecting Postgres. |
| **Database** | Postgres | Shared source of truth. |
