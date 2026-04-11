# TAP Workflow Engine

This package defines the **Workflow Orchestrator** — the central nervous system that connects Agents and Workers into durable, replayable pipelines.

---

## Core Concepts

The Workflow Engine is a hybrid of two well-established patterns:

| Concept | Inspiration | Role in TAP |
|--------|-------------|-------------|
| **Workflow** | Temporal.io | A named pipeline definition loaded from YAML. The Orchestrator executes it step by step. |
| **Activity** | Temporal.io | A single unit of work within a Workflow, performed by an Agent or Worker. |
| **Task Queue** | Temporal.io | The public NATS subject where the Orchestrator broadcasts a `CFP` for an Activity. |
| **Event History** | Temporal.io | An append-only log in `toro_core.workflow_history` tracking every state transition for replay and durability. |
| **CFP / PROPOSE / ACCEPT** | FIPA | The negotiation protocol — Orchestrator calls for proposals, Agents bid, winner is dispatched. |
| **Proof** | TAP | The signed output payload emitted by an Agent after completing an Activity. Always sent to `orchestrator.inbox`. |

---

## One Orchestrator. Many Workflow Instances.

This is the most important thing to understand about the design.

```
┌───────────────────────────────────────────────────────┐
│              Orchestrator (singleton)                 │
│                                                       │
│  Workflow blueprints (from Postgres):                 │
│  ├── csv_cleaner_pipeline                             │
│  ├── tax_year_end_workflow                            │
│  └── quarterly_report_workflow                        │
│                                                       │
│  Running Instances (rows in toro_core.workflows):     │
│  ├── Instance uuid-001  [csv_cleaner, step: 2/5]      │
│  ├── Instance uuid-002  [csv_cleaner, step: 1/5]      │
│  ├── Instance uuid-003  [tax_year_end, SUSPENDED]     │
│  └── Instance uuid-504  [csv_cleaner, step: 4/5]      │
└───────────────────────────────────────────────────────┘
```

When 500 users simultaneously upload CSVs, there is still exactly **one Orchestrator process** but **500 `WorkflowInstance` rows** in Postgres — each tracking its own `current_step_id`, `workflow_variables`, and `pending_activities` independently.

| Thing | Count | Where it lives |
|-------|-------|----------------|
| `Orchestrator` (the service) | **1** per deployment | In memory — started once by the Protocol Daemon |
| `WorkflowDef` (the blueprint) | 1 per pipeline | Stored in `toro_core.workflow_blueprints` (JSONB) and cached in memory |
| `WorkflowInstance` (a running execution) | **N** — one per trigger event | A row in `toro_core.workflows` |

---

## Architecture: How a Workflow Runs

```
Upload Trigger (NATS)
        │
        ▼
  ┌─────────────────────┐
  │  Workflow Orchestrator │  ← resolves blueprint from Postgres
  └─────────────────────┘
        │
  [Step 1] map_columns  (negotiate: true)
        │  Publishes CFP → tasks.accounting.cleanup.mapping
        │  Internal & 3rd-party agents bid via PROPOSE
        │  Orchestrator selects winner → sends ACCEPT to agent.inbox.{DID}
        │  Agent proves result → INFORM to orchestrator.inbox
        │  Orchestrator persists state to toro_core.workflows ✓
        │
  [Step 2] commit_mapped_columns  (negotiate: false)
        │  Orchestrator dispatches directly → worker.inbox.csv-mapping-worker
        │  Worker inserts rows into Postgres
        │  Orchestrator advances to next step ✓
        │
  [Step N] ...
        │
        ▼
    COMPLETED
```

**Key invariant:** The Orchestrator owns all routing. Agents and Workers declare **no knowledge** of upstream or downstream topics. Topology is defined here in YAML, not in Go code.

---

## Durability & Fault Tolerance

Long-running workflows (quarterly reports, annual tax runs) are durably suspended in **`toro_core.workflows`**:

```json
{
    "workflow_def": "Tax Year-End Workflow",
    "current_step_id": "await_user_approval",
    "workflow_variables": { "entity_id": "abc-123", "tax_year": "2025" },
    "pending_activities": ["activity-uuid-789"]
}
```

On Orchestrator boot, any workflow row with status `in_progress` where the step has timed out is automatically **re-dispatched**. No message is held alive in NATS across restarts — the DB is the source of truth.

---

## Actor Routing Rules

| `negotiate` | `task_queue` value | Behaviour |
|-------------|-------------------|-----------| 
| `true` | Public topic (e.g. `tasks.accounting.cleanup.mapping`) | Orchestrator broadcasts `CFP`. Any capable Agent (internal or 3rd-party in Firecracker) can bid. Winner receives `ACCEPT` on their private inbox. |
| `false` | Worker inbox (e.g. `worker.inbox.csv-mapping-worker`) | Orchestrator dispatches `ACCEPT` directly to the provided `task_queue`. If empty, it falls back to the internal Agent's private inbox DID lookup. |

---

## YAML Schema Reference

```yaml
name: "Human Readable Pipeline Name"
version: "1.0"
description: "What this pipeline does"
trigger_topic: nats.subject.that.starts.this.workflow
steps:
  - id: unique_step_id
    activity_type: agents.domain.activity_name  # or workers.domain.activity_name
    task_queue: tasks.domain.activity            # public CFP topic or worker.inbox.{id}
    negotiate: true                              # true = FIPA bidding, false = direct dispatch
    timeout: "60s"                               # wall-clock limit before marking step FAILED
    description: "Optional human-readable note"
```

### `activity_type` Naming Convention

| Prefix | Actor Kind | Example |
|--------|-----------|---------| 
| `agents.*` | TAP Agent (LLM-powered) | `agents.accounting.map_csv` |
| `workers.*` | Internal Worker (DB persistence) | `workers.database.insert_rows` |

---

## Files in This Package

| File | Purpose |
|------|---------|
| `workflow_schema.go` | Go structs — `WorkflowDef`, `WorkflowStep` |
| `orchestrator.go` | Central Orchestrator — seeds blueprints into Postgres, syncs trigger stream subjects, advances state machine |
| `csv_cleaner_pipeline.yaml` | Reference pipeline: CSV upload → column mapping → DB insert → reconciliation |
| `workflow_generation_prompt.md` | LLM prompt for generating new pipeline YAMLs |

---

## Adding a New Workflow

1. **Create a new YAML** in this directory following the schema above.
2. On daemon boot, the Orchestrator's `LoadFromDir(ctx, ...)` **upserts** the YAML into Postgres (`toro_core.workflow_blueprints`).
3. Ensure each `activity_type` has a corresponding Agent registered in `defaults.yaml` or a Worker registered via `init()` in `go/internal/workers/`.

### Quick Scaffold (via LLM)

Use `workflow_generation_prompt.md` — fill in the placeholders and send to an LLM. It will output a conformant YAML definition ready to drop in here.

---

## Relationship to `toro_core.workflows` (Database)

The Orchestrator uses the **existing** `toro_core.workflows` table as its state backend:

- **`state` (JSONB)**: Stores the macro pipeline state — current step, variables, pending activity IDs.
- **`workflow_history`**: Append-only event log for replay — mirrors Temporal's Event History concept.
- **`status`**: `open` → `in_progress` → `completed` / `failed`.

The Redux Engine (micro-state per Agent run) also uses this table during individual Agent executions. The two are distinguished by context — Orchestrator rows are created and owned by the Orchestrator service.

---
---

## Trigger Architecture: Single Consumer, Many Subjects

Workflow triggers are published to ordinary NATS subjects like `events.accounting.1.cleanup`.

Instead of creating one subscription per workflow, the Orchestrator maintains:

1. A single JetStream **stream**: `WORKFLOW_TRIGGERS`
2. A single durable **consumer**: `orchestrator-triggers`
3. A single queue subscription on the consumer's **deliver subject**: `orchestrator.triggers.deliver`

At boot (and whenever `SyncBlueprints()` is called), the Orchestrator:

- Reads all blueprints from `toro_core.workflow_blueprints`.
- Collects all `trigger_topic` values.
- Creates/updates the `WORKFLOW_TRIGGERS` stream to include those subjects.

When JetStream delivers a message to `orchestrator.triggers.deliver`, the original trigger subject is available in the `Nats-Subject` header. The Orchestrator uses that subject to resolve which blueprint pattern matched (e.g. `events.accounting.*.cleanup`).

# 📚 Deep Dive Tutorial: Tracing a CSV Through the Entire Pipeline

> By the end of this tutorial you will understand *exactly* how an uploaded CSV file travels through the system, who wakes up, how they are dispatched, and how state survives crashes.

## Table of Contents

1. [The Cast — Agents and Workers in the CSV Pipeline](#1-the-cast--agents-and-workers-in-the-csv-pipeline)
2. [Chapter 1 — Boot-up: How Actors Register Themselves](#chapter-1--boot-up-how-actors-register-themselves)
3. [Chapter 2 — The Orchestrator Wakes Up](#chapter-2--the-orchestrator-wakes-up)
4. [Chapter 3 — A User Uploads a CSV](#chapter-3--a-user-uploads-a-csv)
5. [Chapter 4 — Step 1: The CSV Mapping Agent](#chapter-4--step-1-the-csv-mapping-agent)
6. [Chapter 5 — Step 2: The CSV Mapping Worker](#chapter-5--step-2-the-csv-mapping-worker)
7. [Chapter 6 — Steps 3–5: Enrichment and Reconciliation](#chapter-6--steps-35-enrichment-and-reconciliation)
8. [Chapter 7 — Durability for Long-Running Workflows](#chapter-7--durability-for-long-running-workflows)

---

## 1. The Cast — Agents and Workers in the CSV Pipeline

The `csv_cleaner_pipeline.yaml` defines five steps. Here are the real actors that execute them:

| Step | ID | Actor | Kind | `activity_type` |
|------|----|-------|------|-----------------|
| 1 | `map_columns` | **CSV Mapping Agent** | Agent (LLM) | `agents.accounting.map_csv` |
| 2 | `commit_mapped_columns` | **CSV Mapping Worker** | Worker (DB) | `workers.database.insert_rows` |
| 3 | `run_enrichment` | **Enrichment Worker** | Worker (DB) | `workers.database.enrich_rows` |
| 4 | `reconcile_revenue` | **Revenue Reconciliation Agent** | Agent (LLM) | `agents.accounting.reconcile_revenue` |
| 5 | `reconcile_expense` | **Expense Reconciliation Agent** | Agent (LLM) | `agents.accounting.reconcile_expense` |

Now let's trace what happens from the moment the Go binary starts to the moment the last row is reconciled.

---

## Chapter 1 — Boot-up: How Actors Register Themselves

Understanding boot-up is critical because **no YAML configuration manually wires actors to topics**. Everything hangs off a self-registration pattern — the same one used by Go's `database/sql` drivers.

### 1.1 How Agents Self-Register

Every Agent registers itself with a single `init()` function in its own package. `init()` runs automatically when the binary imports the package — no central list of agents exists.

```go
// tap/agents/csv_mapping/agent.go

package csvmapping

import "github.com/Yankzy/usetoro/tap/agents"

const AgentName = "csv-mapping-agent"

func init() {
    // Stores the factory in a global map before main() ever runs.
    agents.Register(AgentName, NewAgent)
}
```

After all agent packages are imported, the global registry looks like this:

```
agents.Registry = {
    "csv-mapping-agent":        NewAgent  (tap/agents/csv_mapping)
    "reconcile-expense-agent":  NewAgent  (tap/agents/reconcile_expense)
    "reconcile-revenue-agent":  NewAgent  (tap/agents/reconcile_revenue)
    "approval-agent":           NewAgent  (tap/agents/approval)
    "stripe-processor-agent":   NewAgent  (tap/agents/stripe_processor)
}
```

### 1.2 How Workers Self-Register

Workers use the exact same pattern, but call `RegisterFactory` from the workers package:

```go
// go/internal/workers/csv_mapping_worker.go

func init() {
    RegisterFactory(func(deps Dependencies) (Worker, error) {
        return NewCSVMappingWorker(
            deps.Store.Queries,
            deps.EntityResolver,
            deps.CoAMapper,
            deps.Queue,
            deps.Logger,
        )
    })
}
```

Every worker `init()` appends a factory to a global slice. At startup, the `Manager` calls `LoadFromRegistry(deps)`, which iterates that slice and instantiates every registered worker.

### 1.3 The Protocol Daemon Boots Everything

In `daemon.go`, `Run()` is where it all comes together in order:

```go
// 1. Build the shared EventBus (wraps NATS JetStream)
bus := agent.NewNatsAdapter(d.NATS, d.JS)

// 2. Build the Agent Supervisor (manages agent lifecycles)
d.Supervisor = agent.NewSupervisor(d.Logger, bus, mem, d.DBPool, d.EntityResolver)

// 3. Feed every factory from init() registrations into the Supervisor
for name, factory := range agents.GetRegistry() {
    d.Supervisor.RegisterInternalAgent(name, factory)
}

// 4. Build and configure the Workflow Orchestrator (DB-backed blueprints)
d.Orchestrator = workflows.NewOrchestrator(d.Logger, bus, d.NATS, d.JS, d.Supervisor.Queries)
d.Orchestrator.LoadFromDir(ctx, "tap/workflows") // YAML bootstrap: upsert blueprints into Postgres
d.Orchestrator.SyncBlueprints(ctx)               // stream subject reconciliation + in-memory snapshot

// 5. Assign TaskQueues to Agents based on blueprint steps, then start agents
taskQueues := d.Orchestrator.GetTaskQueues()
for i, cfg := range d.currentConfig.Agents {
    if tq, ok := taskQueues[cfg.ActivityType]; ok {
        d.currentConfig.Agents[i].TaskQueue = tq
    }
}
d.Supervisor.LoadAgents(d.currentConfig.Agents)
```

### 1.4 What `LoadAgents()` Does for Each Agent

When the Supervisor processes each entry in `defaults.yaml`, it:

1. Looks up `internal_module: "csv-mapping-agent"` in the factory registry.
2. Calls `NewAgent(env)`, building the NATS message handler closure.
3. Calls `agent.Start()` on the resulting `BaseAgent`.

`BaseAgent.Start()` performs **dual subscription** (Option A):

```go
func (b *BaseAgent) Start() error {
    // ── Subscription 1: Private inbox (always active) ─────────────────────
    // The Orchestrator sends ACCEPT envelopes here for assigned work.
    // Format: "agents.did:toro:agent:csv_mapping_1.inbox"
    inbox := core.BuildAgentInbox(b.Cfg.DID)
    b.Bus.QueueSubscribe(
        inbox,
        safe+"-inbox-group",
        b.Handler,
        nats.Durable(safe+"-inbox"),
        nats.AckExplicit(),
    )

    // ── Subscription 2: Public Task Queue (conditionally active) ─────────
    // Populated at daemon boot based on workflow blueprints.
    if b.Cfg.TaskQueue != "" {
        b.Bus.QueueSubscribe(
            b.Cfg.TaskQueue,        // e.g. "tasks.accounting.cleanup.mapping"
            b.Cfg.QueueGroup,       // derived from DID
            b.Handler,
            nats.Durable(b.Cfg.DurableName),
            nats.AckExplicit(),
        )
    }

    // ── Almanac: announce capabilities ───────────────────────────────────
    b.Bus.Publish("almanac.register", regPayload)
    // payload: {did, endpoints: [inbox], capabilities: [{activity_type: "agents.accounting.map_csv"}]}
}
```

**At this point, boot is complete.** Every Agent is listening on its private inbox (and any assigned public task queues). The Orchestrator is ready to consume triggers via a single JetStream consumer. Nothing is running yet.

---

## Chapter 2 — The Orchestrator Wakes Up

After boot, the full NATS subscription map looks like this:

```
Orchestrator:
  ├── orchestrator.triggers.deliver     (WORKFLOW_TRIGGERS consumer delivery subject)
  └── orchestrator.inbox                (receives PROPOSE bids & INFORM proofs)
  └── workflow.query.blueprint          (blueprint graph lookup)

CSV Mapping Agent (did:toro:agent:csv_mapping_1):
  └── agents.did:toro:agent:csv_mapping_1.inbox

Revenue Reconciliation Agent:
  └── agents.did:toro:agent:reconcile_revenue_1.inbox

Expense Reconciliation Agent:
  └── agents.did:toro:agent:reconcile_expense_1.inbox

CSV Mapping Worker:
  └── worker.inbox.csv-mapping-worker

Enrichment Worker:
  └── worker.inbox.enrichment-worker
```

The Orchestrator will match triggers like `events.accounting.1.cleanup` to the correct workflow because the blueprint registry contains a pattern such as:

```yaml
trigger_topic: events.accounting.*.cleanup
```

The `*` wildcard means it matches any entity variant (entity type 1, 2, etc.). JetStream preserves the original subject in the delivered message headers, so the Orchestrator can resolve the matching blueprint at runtime.

---

## Chapter 3 — A User Uploads a CSV

A user uploads `bank_statement_jan.csv` through the frontend. The `HandleFileIngestion` handler in `go/internal/api/upload_handler.go`:

1. Parses the file into `[][]string` rows.
2. Queries `toro_core.erp_connections` for the user's `realm_id`.
3. Creates a `CleanupSession` row in Postgres (`upload_id` = the session's UUID).
4. Packages a payload with `{session_id, rows, entity_id, realm_id, ...}`.
5. Wraps it in a `CFP` envelope.
6. Publishes to `events.accounting.1.cleanup`.

```go
// upload_handler.go
topic := core.BuildEventSubject(domain, core.ComplexityEntry, taskType)
// → "events.accounting.1.cleanup"

h.NATS.Publish(topic, informBytes)
// HTTP response: 202 Accepted {"upload_id": "b3f1a2c4-...", "status": "TRIAGING"}
```

JetStream routes this message into the `WORKFLOW_TRIGGERS` stream (because its subjects include the workflow trigger topics).

The Orchestrator consumes all triggers via a **single** queue subscription on `orchestrator.triggers.deliver`. When a message arrives there, it:

1. Reads the original subject from the `Nats-Subject` header.
2. Resolves the matching blueprint (e.g. `events.accounting.*.cleanup`).
3. Calls `handleTrigger()` with that blueprint.

```go
func (o *Orchestrator) handleUnifiedTrigger(ctx context.Context, msg *nats.Msg) {
    // msg.Subject == "orchestrator.triggers.deliver"
    subject := msg.Header.Get("Nats-Subject") // e.g. "events.accounting.1.cleanup"
    def := o.resolveBlueprintForSubject(subject)
    _ = o.handleTrigger(ctx, def, msg)
}
```

`handleTrigger()` executes:

1. **Envelope Parsing:** It first unmarshals the FIPA Envelope and inner TaskDefinition.
2. **Identity Extraction:** It extracts the `entity_id` from the deep JSON payload. This is critical for database constraints.
3. **DB Persistence:** It creates a record in `toro_core.workflows` with the extracted `entity_id`.

```go
func (o *Orchestrator) handleTrigger(ctx context.Context, def WorkflowDef, msg *nats.Msg) error {
    instanceID := uuid.New()
    
    // Extract entity_id from msg.Data (FIPA Envelope -> TaskDef -> Body)
    entityID := extractEntityID(msg.Data) 

    // INSERT into toro_core.workflows (id, entity_id, state)
    o.queries.CreateOrGetWorkflow(ctx, arg)

    // Dispatch Step 1
    return o.dispatchStep(ctx, def, def.Steps[0], instanceID.String(), msg.Data)
}
```

---

## Chapter 4 — Step 1: The CSV Mapping Agent

**Step definition in `csv_cleaner_pipeline.yaml`:**
```yaml
- id: map_columns
  activity_type: agents.accounting.map_csv
  task_queue: tasks.accounting.cleanup.mapping
  negotiate: true
  timeout: "60s"
```

`negotiate: true` → FIPA bidding path. `dispatchStep()` broadcasts a **CFP** (Call for Proposal):

```go
// orchestrator.go — dispatchStep()
taskDef := core.TaskDefinition{ID: instanceID, Domain: "agents.accounting.map_csv", Payload: msg.Data}
cfp, _ := core.NewEnvelope(uuid.New().String(), "did:toro:orchestrator", "", convID, core.CFP, taskDef)
o.bus.Publish("tasks.accounting.cleanup.mapping", cfpBytes)
```

**Who is listening on `tasks.accounting.cleanup.mapping`?**

The CSV Mapping Agent! (And potentially any 3rd-party agent running in a Firecracker microVM that has subscribed to the same topic to bid on accounting work.)

The agent's `Handler` closure fires. `handleCFP()` executes:

```go
func (a *CSVMappingAgent) handleCFP(msg *nats.Msg) error {
    var env core.Envelope
    json.Unmarshal(msg.Data, &env)
    // env.Performative == core.CFP

    // ── FIPA Step 1: Send a PROPOSE bid to the Orchestrator ──────────────
    proposal := map[string]interface{}{"price": 1, "eta": "10s"}
    replyEnv, _ := core.NewEnvelope(uuid.New().String(), a.Cfg.DID,
        env.SenderDID, env.ConversationID, core.PROPOSE, proposal)
    a.Bus.Publish(core.BuildAgentInbox(env.SenderDID), replyBytes)
    // → publishes to "orchestrator.inbox"

    // ── Auto-execute (internal agents skip waiting for ACCEPT) ────────────
    // Full FIPA round-trip (wait for Orchestrator ACCEPT) is a TODO
    // for when Firecracker 3rd-party agents compete and bid selection matters.
    return a.executeTask(env)
}
```

`executeTask()` runs the full **Redux workflow**:

```
payload.Rows ([][]string from the CFP) →

llmCallback():
  → builds prompt: "Analyze the following sample rows..."
  → calls a.rt.ExecWithPaging(ctx, fullPrompt)  ← calls GPT/Gemini
  → receives: {"date_col_idx": 0, "description_col_idx": 2, "amount_col_idx": 3, ...}
  → builds JSON Patches:
      [{"op":"add","path":"/status","value":"COLUMNS_MAPPED"},
       {"op":"add","path":"/mapped_rows","value":{...all parsed rows by row_id...}}]

Redux Engine validates the patches:
  ✓ Payload size bounds check
  ✓ RBAC: only /status and /mapped_rows allowed for this DID
  ✓ Schema: state matches workflow_schema JSON Schema in defaults.yaml
  ✓ No arrays in patch values (banned for safety)

onComplete() fires with the validated final state:
  → extracts mapped_rows from state JSON
  → builds: core.Proof{TaskID: sessionID, Type: "proof.api", Data: mapped_rows}
  → wraps Proof in INFORM envelope
  → publishes to "orchestrator.inbox"   ← Proof returned to Orchestrator
```

The Orchestrator's `handleIncoming()` receives the `INFORM`:

```go
case core.INFORM:
    // Step 1 (map_columns) is DONE.
    // TODO: extract instanceID from ConversationID,
    //       UPDATE toro_core.workflows SET current_step_id = 'commit_mapped_columns',
    //       call dispatchStep() for Step 2 with the Proof as payload.
```

---

## Chapter 5 — Step 2: The CSV Mapping Worker

**Step definition:**
```yaml
- id: commit_mapped_columns
  activity_type: workers.database.insert_rows
  task_queue: worker.inbox.csv-mapping-worker
  negotiate: false
  timeout: "10s"
```

`negotiate: false` → direct dispatch. No CFP, no bidding. The Orchestrator looks up the target directly and sends an `ACCEPT_PROPOSAL` envelope straight to the worker's inbox:

```go
// orchestrator.go — dispatchStep(), negotiate=false path
// Since task_queue is provided, it dispatches directly to that subject.
inbox := "worker.inbox.csv-mapping-worker"
o.bus.Publish(inbox, acceptEnvelope)
```

The Worker Manager's dispatch layer fires `CSVMappingWorker.Handle()`:

```go
func (e *CSVMappingWorker) Handle(ctx context.Context, msg *nats.Msg) error {
    // 1. Parse the ACCEPT_PROPOSAL envelope from the Orchestrator
    var env core.Envelope
    json.Unmarshal(msg.Data, &env)
    // env.Performative == core.ACCEPT_PROPOSAL

    // 2. Extract the Proof payload
    var proof core.Proof
    json.Unmarshal(env.Body, &proof)

    // 3. Extract mapped rows from proof.Data
    rows, _ := ExtractRows(proof.Data)  // []map[string]interface{}

    // 4. Mark session as PROCESSING
    e.db.UpdateCleanupSessionStatus(ctx, database.UpdateCleanupSessionStatusParams{
        ID: pgSessionID, Status: "PROCESSING",
    })

    // 5. Insert every mapped row into the DB as PENDING
    for _, r := range rows {
        e.db.InsertCleanupRow(ctx, database.InsertCleanupRowParams{
            SessionID:      pgSessionID,
            RawDescription: pgtype.Text{String: r["Description"].(string), Valid: true},
            RawAmount:      r["Amount"].(string),
            Status:         "PENDING",
            // ... vendor, customer, date, realm_id ...
        })
    }

    // Workers are PASSIVE. They return nil.
    // The Manager calls msg.Ack() automatically.
    // The Orchestrator advances the pipeline — NOT the worker.
    return nil
}
```

> **Workers are passive.** They never publish to the next pipeline topic. The Orchestrator detects completion (worker returned nil → ack'd) and dispatches Step 3.

---

## Chapter 6 — Steps 3–5: Enrichment and Reconciliation

The same orchestration pattern repeats:

**Step 3 — `run_enrichment`** (`negotiate: false`)

```
Orchestrator → ACCEPT_PROPOSAL → worker.inbox.enrichment-worker

EnrichmentWorker.Handle():
  → fetches all PENDING rows for this session
  → for each row: calls EntityResolver AI service to normalize vendor/customer strings
  → updates row status to ENRICHED with PredictedVendorID, PredictedCustomerID
```

**Step 4 — `reconcile_revenue`** (`negotiate: true`)

```
Orchestrator → CFP → tasks.accounting.cleanup.reconcile.revenue

RevenueReconciliationAgent.handleEnrichmentProof():
  → reads ENRICHED rows from DB where amount > 0  (positive = money in = revenue)
  → for each: entityResolver.ResolveCustomer(rowRealm, entityInput)
              entityResolver.ResolveAccount(rowRealm, "money_in", aiInput, customerName)
  → updates rows: PredictedCustomerID, PredictedAccountID, ConfidenceScore, AiReasoning
  → publishes INFORM Proof → orchestrator.inbox
```

**Step 5 — `reconcile_expense`** (`negotiate: true`)

```
Orchestrator → CFP → tasks.accounting.cleanup.reconcile.expense

ExpenseReconciliationAgent.handleEnrichmentProof():
  → same as revenue but filters amount ≤ 0  (negative = money out = expense)
  → resolves Vendors instead of Customers
  → resolves Account with direction "money_out"
  → publishes INFORM Proof → orchestrator.inbox
```

When both proofs arrive at `orchestrator.inbox`, the Orchestrator marks the `WorkflowInstance` as `completed`.

---

## Chapter 7 — Durability for Long-Running Workflows

### Short workflows (~30 seconds): NATS JetStream handles it

NATS JetStream durable consumers provide automatic replay. If the daemon crashes mid-step, the unacknowledged message is redelivered after the consumer's `ack_wait` period. The worker/agent processes it again — all handlers are idempotent (`ON CONFLICT DO NOTHING` in DB queries).

### Long workflows (days, months): Postgres is the source of truth

Consider a `tax_year_end_workflow` where a CPA must manually review and approve before the next step can proceed. This workflow might suspend for **3 weeks**.

```json
// toro_core.workflows row:
{
    "workflow_def": "Tax Year-End Workflow",
    "current_step_id": "await_cpa_review",
    "workflow_variables": {
        "entity_id": "abc-123",
        "tax_year": "2025",
        "submitted_at": "2026-01-15T09:00:00Z"
    },
    "pending_activities": ["activity-b3f1a2c4"]
}
// status: "suspended"
```

No NATS messages are held alive. NATS `max_age: 168h` (7 days) would expire them anyway. The workflow "sleeps" in Postgres indefinitely.

Three weeks later, the CPA clicks "Approve" in the frontend:
1. API receives the approval event.
2. Orchestrator queries `toro_core.workflows` for the suspended instance.
3. Orchestrator calls `dispatchStep()` for the next step directly from the persisted `current_step_id`.
4. Pipeline resumes exactly where it left off.

### Crash Recovery on Boot

```go
func (o *Orchestrator) Start(ctx context.Context) error {
    // TODO: SELECT * FROM toro_core.workflows
    //       WHERE status = 'in_progress'
    //       AND updated_at < NOW() - INTERVAL '5 minutes'
    //
    // For each stalled row: re-dispatch current_step_id with its stored payload.
    // This is the Temporal "replay" equivalent — we don't replay code,
    // we simply re-emit the last known dispatch event.
    ...
}
```

This guarantees **at-least-once delivery** for every step. Because all handlers are idempotent, re-dispatching a completed step is safe — the DB insert will be a no-op.
