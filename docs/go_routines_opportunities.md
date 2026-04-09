# Event-Driven Architecture: NATS JetStream & Postgres CDC Opportunities

This document outlines areas in the Toro codebase where we currently use hardcoded, synchronous execution paths that block HTTP requests or rely on periodic polling. By leveraging the existing **Postgres WAL CDC replication** ([go/internal/cdc/publisher.go](file:///Users/Yankz/programming/usetoro/go/internal/cdc/publisher.go)) and **NATS JetStream**, we can decouple these processes into dedicated background workers (Goroutines) reacting to database events. 

Moving these to a dedicated folder (e.g., `go/internal/workers/` or `go/internal/events/`) will centralize our async event architecture.

---

### 1. Vector Database Synchronization (Pinecone)
**Current State**: 
[VectorSyncWorker](file:///Users/Yankz/programming/usetoro/go/internal/services/ai/vector_worker.go#23-30) ([go/internal/services/ai/vector_worker.go](file:///Users/Yankz/programming/usetoro/go/internal/services/ai/vector_worker.go)) uses a `time.Ticker` to periodically poll the database for newly inserted/updated Accounts, Vendors, and Customers. It then batches and upserts them to Pinecone.
**Event-Driven Opportunity**:
- Eliminate polling. Have an embedding event-worker listen to CDC events like `ledger.shadow_erp_accounts.insert` or `ledger.shadow_erp_accounts.update`.
- When an event arrives, a goroutine adds the entity to a buffered channel. Once the batch size is reached (or a short timeout occurs), it bulk embeds and upserts to Pinecone in real-time.

### 2. Receipt Processing & ERP Uploading ([AttachableService](file:///Users/Yankz/programming/usetoro/go/internal/services/accounting/attachable_service.go#29-34))
**Current State**: 
[HandleUploadAttachable](file:///Users/Yankz/programming/usetoro/go/internal/api/attachable_handler.go#9-71) ([go/internal/api/attachable_handler.go](file:///Users/Yankz/programming/usetoro/go/internal/api/attachable_handler.go)) blocks the HTTP request while it parses the file, builds QBO multipart API requests, and waits for the QuickBooks HTTP response to confirm the upload.
**Event-Driven Opportunity**:
- The API handler simply uploads the raw file to S3/blob storage and inserts a pending record into a `receipts` or `shadow_erp_attachables` table.
- The CDC publisher emits `ledger.shadow_erp_attachables.insert`.
- A dedicated Goroutine worker picks up this event, downloads the file, processes it (OCR/Classification if needed), and asynchronously pushes it to QBO using the QBO Connector.

### 3. Expense/Transaction Creation ([TransactionService](file:///Users/Yankz/programming/usetoro/go/internal/services/accounting/transaction_service.go#35-45))
**Current State**: 
[PostExpense](file:///Users/Yankz/programming/usetoro/go/internal/erp/adapters/quickbooks/adapter.go#127-201) ([go/internal/services/accounting/transaction_service.go](file:///Users/Yankz/programming/usetoro/go/internal/services/accounting/transaction_service.go)) synchronously updates the local database to create a `proposed_transaction`, runs the Rule Engine for AI categorization, and then synchronously executes the `provider.PostExpense` call to QBO.
**Event-Driven Opportunity**:
- **Phase 1 (Creation)**: The UI or external system inserts a newly fetched transaction into the DB.
- **Phase 2 (Categorization)**: CDC emits `ledger.shadow_erp_proposed_transactions.insert`. An AI worker reacts to this event, runs the Rule Engine, and updates the DB with the assigned Account/Vendor.
- **Phase 3 (ERP Push)**: CDC emits `ledger.shadow_erp_proposed_transactions.update` (where `status = PENDING_SYNC`). Another worker reacts, pushes the transaction to QBO, and finalizes the DB status to `SYNCED`.

### 4. Bulk Cleanup Ingestion (`CleanupHandler`)
**Current State**: 
[HandleFileIngestion](file:///Users/Yankz/programming/usetoro/go/internal/api/upload_handler.go#41-201) ([go/internal/api/upload_handler.go](file:///Users/Yankz/programming/usetoro/go/internal/api/upload_handler.go)) parses CSV/XLSX files and synchronously inserts up to 10,000 rows into the database while the user's HTTP request hangs open waiting for it to finish.
**Event-Driven Opportunity**:
- The API handler creates a single `cleanup_sessions` row in the DB with the uploaded file payload/reference and immediately returns a `201 Accepted`.
- CDC emits `ledger.cleanup_sessions.insert`.
- A Goroutine background worker picks up the session ID, parses the bulky Excel file in the background, and streams the inserts into the DB. Once finished, it updates the session status to `COMPLETED`, which could trigger a WebSocket notification to the frontend.

### 5. Webhook Ingestion (`WebhookProcessor`) -> *Already Transitioning*
**Current State**: 
We recently updated [webhook_processor.go](file:///Users/Yankz/programming/usetoro/go/internal/connectors/webhook_processor.go) to construct an `EventCDCSync` and publish it to NATS instead of executing the DB upserts sequentially.
**Event-Driven Opportunity**:
- This is the exact pattern we want to replicate across the system. We should move the [ERPEventWorker](file:///Users/Yankz/programming/usetoro/go/internal/services/accounting/event_worker.go#25-35) and all future NATS JetStream subscribers into a unified `event` or `worker` package to centralize our asynchronous architecture.
