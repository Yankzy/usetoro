## Phase 3 — Accounting Intelligence: TODO (modular-monolith architecture)

This document captures the current implementation status for Phase 3 (the "Accounting Intelligence" work described in `go/qbo/road_map.md`) and reconciles it with this repository's modular-monolith constraints:

- All AI code should be implemented under `cmd/protocol`.
- All QBO/sync code (the code that actually calls QuickBooks or manages sync jobs) should be implemented under `cmd/sync`.
- Orchestration between AI and QBO is message-driven and should use NATS JetStream. AI agents in `cmd/protocol` may request QBO actions, but `cmd/sync` (and `go/qbo`) MUST NOT instantiate AI agents directly.

### Summary (architectural constraints applied)

- Status: The underlying vector & AI infra (embedder, pinecone client, vector sync, CoA mapper, entity resolver) and QBO primitives (attachable, bill, client) are present in the codebase.
- Structural requirement: Any orchestration code must respect module boundaries. `cmd/protocol` contains AI logic and publishes JetStream messages to request QBO actions. `cmd/sync` subscribes to those subjects and performs QBO API calls. `cmd/sync` should implement handlers/subscribers — not agents — and must not instantiate protocol agents.

---

### Done (implemented)

- Pinecone client and vector primitives
  - `go/internal/infrastructure/vector/pinecone_client.go` — Upsert, Query, Delete, DescribeIndexStats.
- Embedder (OpenAI)
  - `go/internal/infrastructure/vector/embedder.go` — Embed and EmbedBatch.
- Vector sync worker (sync DB → Pinecone)
  - `go/internal/services/ai/vector_worker.go` — Syncs Accounts, Vendors, Customers, handles batching and upsert.
- CoA Mapper
  - `go/internal/services/ai/coa_mapper.go` — MapDescriptionToAccount implemented.
- Entity Resolver
  - `go/internal/services/ai/entity_resolver.go` — DB→Pinecone→Fuzzy pipeline implemented.
- QBO client basics
  - `go/qbo/client.go` — OAuth, req/post helpers, Batch and CDC helpers.
- QBO Attachables
  - `go/qbo/attachable.go` — Upload/Create/Update/Download implemented.
- Bills
  - `go/qbo/bill.go` — Create/Update/Query/Delete Bill implemented.

### Partially done / Present as examples

- Roadmap contains orchestration examples (`AIService.PostExpense`, `AIService.AttachReceipt`) as pseudocode in `go/qbo/road_map.md`. The functional building blocks exist, but orchestration must be implemented following module boundaries described above.

### Missing / Deferred (action items — updated to repo architecture)

1. CreatePurchase support (where QBO calls live: `cmd/sync`)
   - The roadmap uses a `Purchase` entity and `CreatePurchase(...)`. Implement a `CreatePurchase` function in the package owned by `cmd/sync` (a thin wrapper around the existing `go/qbo` client or a new `go/qbo/purchase.go` if you prefer keeping client types together).
   - Note: Implementation should remain in `cmd/sync` handlers; do not instantiate protocol agents here.

2. Protocol-side orchestration (under `cmd/protocol`) — AI agents publish requests
   - Implement AI orchestration in `cmd/protocol` agents that:
     - Resolve vendor/account using embedder/Pinecone (or publish a `resolve.request` if resolution is centralized).
     - Publish a JetStream `qbo.create_transaction` message with the resolved refs and transaction payload.
     - Optionally subscribe to a reply subject for success/failure.

3. Sync-side JetStream handlers (under `cmd/sync`) — perform QBO calls, but do NOT instantiate protocol agents
   - `cmd/sync` must subscribe to `qbo.*` subjects and execute the QBO API calls (CreateBill/CreatePurchase, UploadAttachable, Batch). Implement these as handlers that accept JetStream messages, call the QBO client, and publish results/acknowledgements back to the requestor.
   - Important: `cmd/sync` handlers are not AI agents; they are message-driven workers that perform API operations and publish outcomes.

4. Attachable orchestration (split responsibility)
   - Protocol publishes `attachable.request` (with entityRef or later binding intent); `cmd/sync` receives it, performs `UploadAttachable` and links the attachable via `CreateAttachable`/`UpdateAttachable`, and publishes the result.

5. Confidence / human-review path
   - Low-confidence matches should be surfaced by `cmd/protocol` (publish `review.request`) and persisted in the DB; the UI or human workflow can approve. Only after approval should `cmd/protocol` publish the create transaction request.

6. Tests & CI for the message-driven flow
   - Add unit tests for JetStream handlers and protocol publishers. Use a mocked or in-memory NATS JetStream for CI tests where possible.

### Implementation plan (architecturally aligned)

1. Implement `CreatePurchase` under `cmd/sync` (or `go/qbo/purchase.go` with calls invoked by `cmd/sync`) — small
   - Create a thin wrapper that produces the JSON payload expected by QBO and calls the low-level client. Keep the public surface minimal and owned by `cmd/sync`.

2. Implement `cmd/sync` JetStream subscribers for QBO operations — medium
   - Subjects: `qbo.create_transaction`, `qbo.upload_attachable`, `qbo.batch` (example names). Implement idempotency, retries, and rate-limit handling in these handlers.

3. Implement protocol-side orchestration under `cmd/protocol` — medium
   - Agents resolve and publish requests to `qbo.*` subjects. They should not import the `go/qbo` client.

4. Implement attachable duplicate detection and late-binding logic in `cmd/sync` handlers — medium

5. Add tests for JetStream handlers and the AI→QBO message flows — medium

### Quick mapping: Roadmap checklist → repo status (architectural view)

- CoA Vector Mapper: Done (`go/internal/services/ai/coa_mapper.go`) — available for `cmd/protocol` or `cmd/sync` (instantiate where ownership is decided)
- Entity Resolution Service: Done (`go/internal/services/ai/entity_resolver.go`) — instantiate in `cmd/protocol` agents or expose as a `resolve` JetStream subject
- Attachment Service (Attachable): QBO primitives Done (`go/qbo/attachable.go`), orchestration wrapper to be implemented as JetStream handlers in `cmd/sync` — Partial
- Transaction Creator (Purchase): Bill Done (`go/qbo/bill.go`), CreatePurchase Missing in `cmd/sync` scope — Partial

### Final notes

Respect the modular-monolith boundaries:

- `cmd/protocol` = AI agents, publishers of JetStream requests, and human-review orchestration. It may call vector/embedder infra but shouldn't call QBO client primitives directly.  
- `cmd/sync` = QBO API callers and JetStream subscribers/handlers. It performs uploads/creates/updates to QuickBooks in response to messages. It MUST NOT instantiate protocol agents.

If you want, I can implement the first item now: add `CreatePurchase` under the `cmd/sync` ownership model (thin wrapper around `go/qbo`), and then add a JetStream handler stub in `cmd/sync` for `qbo.create_transaction` that calls CreateBill/CreatePurchase. Tell me if you want the `CreatePurchase` code placed in `go/qbo/purchase.go` (client types together) or as a wrapper under `cmd/sync` (explicit module ownership).

