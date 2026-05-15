# TAP Workflow Orchestrator

`tap/workflows/orchestrator.go`

The `Orchestrator` is the central control-plane service for TAP. It owns the full lifecycle of a
workflow instance: receiving external triggers, persisting state, dispatching work to Agents and
Workers over NATS, correlating proofs on completion, and advancing the state machine to the next
step.

---

## Table of Contents

1. [Design Philosophy](#1-design-philosophy)
2. [Key Constants](#2-key-constants)
3. [Struct Layout](#3-struct-layout)
4. [Startup Sequence](#4-startup-sequence)
5. [Blueprint Registry](#5-blueprint-registry)
6. [NATS Subject Construction](#6-nats-subject-construction)
7. [Protocol Verbs](#7-protocol-verbs)
8. [Envelope & Primitives](#8-envelope--primitives)
9. [Trigger Ingestion](#9-trigger-ingestion)
10. [Step Dispatch](#10-step-dispatch)
    - [Negotiate = true (CFP)](#negotiate--true-fipa-cfp-path)
    - [Negotiate = false (Direct)](#negotiate--false-direct-dispatch-path)
    - [Switch Routing & Suspension](#switch-routing--suspension)
    - [Sub-Workflows](#sub-workflows)
11. [Proof Correlation — The ConversationID Contract](#11-proof-correlation--the-conversationid-contract)
12. [State Machine](#12-state-machine)
13. [Almanac Discovery](#13-almanac-discovery)
14. [Blueprint Query API](#14-blueprint-query-api)
15. [Status Events](#15-status-events)
16. [Error & Retry Semantics](#16-error--retry-semantics)
17. [Scaling & Horizontal Safety](#17-scaling--horizontal-safety)
18. [CSV Cleaner Pipeline — Reference Shape](#18-csv-cleaner-pipeline--reference-shape)
19. [Practical Checklist for Adding a Step](#19-practical-checklist-for-adding-a-step)
20. [Who Owns Sequencing](#20-who-owns-sequencing)

---

## 1. Design Philosophy

- **Stateless orchestration, stateful DB.** The Orchestrator holds no per-instance in-memory
  state. Every decision is driven by rows in `toro_core.workflows`. NATS provides transport;
  Postgres provides durability.
- **Single inbox.** All replies (PROPOSE bids, INFORM proofs) arrive at one subject —
  `orchestrator.inbox`. Routing to the correct workflow instance is achieved purely through the
  FIPA `ConversationID` field.
- **Dynamic discovery.** Agents are found at dispatch time via the Almanac service, not hardcoded.
  The Orchestrator never imports agent packages.
- **One JetStream consumer for all triggers.** A single durable push consumer
  (`orchestrator-triggers`) fans out messages from the unified `WORKFLOW_TRIGGERS` JetStream
  stream, regardless of how many active workflow trigger topics exist.
- **Composable DAGs.** Workflows are pure YAML — steps declare `depends_on`, `route_condition`,
  and `sub_workflow` keys. No workflow file imports another; composition is by name reference only.

---

## 2. Key Constants

| Constant | Value | Purpose |
|---|---|---|
| `OrchestratorInbox` | `orchestrator.inbox` | Single subject where all agents/workers reply |
| `OrchestratorDID` | `did:toro:orchestrator` | Canonical FIPA sender identity |
| `WorkflowTriggerStream` | `WORKFLOW_TRIGGERS` | JetStream stream collecting all trigger events |
| `WorkflowTriggerConsumer` | `orchestrator-triggers` | Durable push consumer name |
| `WorkflowTriggerDeliverSubject` | `orchestrator.triggers.deliver` | Subject JetStream pushes trigger messages to |
| `WorkflowTriggerDeliverGroup` | `orchestrator-trigger-group` | Queue group for horizontal scaling |

---

## 3. Struct Layout

```go
type Orchestrator struct {
    logger  *slog.Logger
    queries *database.Queries    // Postgres access layer (sqlc-generated)
    bus     core.EventBus        // NATS publish/subscribe abstraction

    nc  *nats.Conn               // Raw NATS connection (for QueueSubscribe)
    js  nats.JetStreamContext    // JetStream context (for stream management)

    blueprintMu sync.RWMutex    // Guards the in-memory blueprint snapshot
    blueprints  []WorkflowDef   // Cached from DB; refreshed via SyncBlueprints()

    subs []*nats.Subscription   // Tracked subscriptions, drained on shutdown
}
```

The in-memory `blueprints` slice is a **read-only snapshot** used for subject matching. The
Postgres DB is the source of truth. A write to `blueprints` only ever happens inside
`SyncBlueprints()`, protected by `blueprintMu`.

---

## 4. Startup Sequence

`Orchestrator.Start(ctx)` proceeds in strict order:

```
1. Subscribe to orchestrator.inbox          → handleIncoming (PROPOSE / INFORM)
2. SyncBlueprints()                         → refresh in-memory snapshot + reconcile JetStream stream subjects
3. startUnifiedTriggerSubscription()        → single QueueSubscribe on WorkflowTriggerDeliverSubject
4. startResumeSubscription()                → handles workflow.resume messages
5. Subscribe to workflow.query.blueprint    → handleBlueprintQuery (API Gateway lookups)
6. Block on ctx.Done()
7. Drain all subscriptions on shutdown
```

If `SyncBlueprints` finds no blueprints, step 3 logs a warning and skips — the Orchestrator
starts cleanly but is inactive.

---

## 5. Blueprint Registry

### Loading from YAML

On first boot, `LoadFromDir(ctx, dirPath)` scans a directory for `*.yaml` files, parses each
into a `WorkflowDef`, and **upserts** the result into the `workflow_blueprints` Postgres table.
The DB is the runtime registry; the YAML files are the bootstrap source.

Hot-reload is supported via `WatchWorkflows(ctx, dirPath)` which uses `fsnotify` to detect writes
and calls `UpsertWorkflowFromFile` + `SyncBlueprints` automatically.

### Syncing to Memory

`SyncBlueprints(ctx)` reads all rows from the DB, deserialises each `WorkflowDef`, rebuilds the
in-memory `[]WorkflowDef` slice, and then **reconciles the WORKFLOW_TRIGGERS JetStream stream**
subjects to exactly match the current set of `trigger_topic` values across all blueprints. This
allows new workflow definitions to be registered at runtime without restarting the Orchestrator.

### Subject Matching

When a trigger message arrives, the Orchestrator calls `resolveBlueprintForSubject(subject)` which
iterates all blueprints and uses `matchNATSSubject(pattern, subject)` — a full NATS wildcard
implementation supporting `*` (one token) and `>` (rest of subject). Ties are broken by
`triggerPatternScore`, which ranks exact-token specificity higher than wildcards.

---

## 6. NATS Subject Construction

Canonical constructors live in `tap/pkg/core/topics.go`:

| Constructor | Output |
|---|---|
| `core.BuildTaskSubjectFromActivity("agents.accounting.map_csv", 1)` | `tasks.accounting.1.map_csv` |
| `core.BuildWorkerInbox("csv-mapping-worker")` | `worker.inbox.csv-mapping-worker` |
| `core.BuildAgentInbox("did:toro:agent:abc")` | `agents.did:toro:agent:abc.inbox` |
| `core.BuildEventSubject(domain, complexity, taskType)` | `events.<domain>.<complexity>.<task_type>` |

Queue normalization is centralized in `core.NormalizeTaskQueue(...)` and
`core.NormalizeTaskQueueWithComplexity(...)`. These are used by workflow step routing and runtime
loading paths.

### Subscriptions by role

**Orchestrator:**
- `orchestrator.inbox` (durable queue sub): receives step replies (`PROPOSE`, `INFORM`).
- `orchestrator.triggers.deliver` (queue sub): unified delivery subject for trigger stream consumer.
- `workflow.query.blueprint` (queue sub): serves blueprint lookup requests.

**Agents** (`tap/pkg/agent/base.go`):
- Private inbox: `core.BuildAgentInbox(<did>)` → `agents.<did>.inbox`
- Public task queue (optional): derived/normalized from workflow `activity_type`, `complexity`, and optional `task_queue`.

**Workers** (`go/internal/workers/*.go`):
- Workers declare concrete subjects via `Subscriptions()`.
- Common pattern: `worker.inbox.<worker_id>`.

---

## 7. Protocol Verbs

Protocol verbs are defined in `tap/pkg/core/verbs.go`:
- `CFP`, `PROPOSE`, `ACCEPT_PROPOSAL`, `REJECT_PROPOSAL`
- `INFORM`, `QUERY_REF`
- `REQUEST`, `DELEGATE`, `REFUSE`, `FAILURE`

`core.IsValidPerformative(...)` is enforced when creating envelopes (`core.NewEnvelope`) and on
orchestrator/worker ingress guards.

---

## 8. Envelope & Primitives

### Envelope (`tap/pkg/core/envelope.go`)

Every control message is a `core.Envelope`:
- `id`, `ts`, `src`, `dst`, `perf`, `cid`, `body`, `sig`

### Task primitive (`tap/pkg/core/primitives.go`)

Orchestrator dispatches steps as `core.TaskDefinition` in envelope body:
- `ID`: workflow instance id
- `Domain`: step `activity_type`
- `Complexity`: validated complexity (`1|5|10`)
- `Reward` + `Currency`: baseline payment metadata
- `Payload`: prior step payload/proof
- `ExpiresAt`: dispatch expiry derived from step timeout

### Proof primitive

Agents/workers return `core.Proof` in `INFORM` envelopes to `orchestrator.inbox`.

**Message shape by stage:**

| Stage | Envelope performative |
|---|---|
| Trigger event | `INFORM`, body: `TaskDefinition` |
| Negotiated step dispatch | `CFP`, body: `TaskDefinition` |
| Direct step dispatch | `ACCEPT_PROPOSAL`, body: `TaskDefinition` |
| Step completion | `INFORM`, body: `Proof` |
| Discovery lookup | request on `core.SubjectAlmanacQuery` with capability payload |

---

## 9. Trigger Ingestion

```
External Producer
    │
    ▼  publish to e.g. events.accounting.1.cleanup
JetStream stream: WORKFLOW_TRIGGERS
    │  (push consumer: orchestrator-triggers)
    ▼  deliver to: orchestrator.triggers.deliver
Orchestrator (handleUnifiedTrigger)
    │
    ├─ resolve blueprint by trigger_topic pattern
    ├─ extract entity_id from inner payload
    ├─ create workflow instance row in toro_core.workflows
    └─ scheduleReadySteps → dispatch entry steps
```

The trigger envelope is expected to be a FIPA `Envelope{ perf: INFORM, body: TaskDefinition }`
where `TaskDefinition.Payload` is a JSON object containing at minimum `entity_id` (a UUID). If
`entity_id` is absent the trigger is **rejected** — the instance is not created.

The raw trigger payload (unwrapped inner bytes) is forwarded as-is to the first step.

---

## 10. Step Dispatch

`scheduleReadySteps` is the core DAG engine. It calls `readySteps` to find all steps whose
`depends_on` entries are all in `CompletedSteps` and whose `route_condition` (if any) is
satisfied. For each ready step it calls `buildStepPayload`, then routes to one of three paths:

```go
if step.SubWorkflow != "" {
    o.spawnSubWorkflow(...)   // compose a child workflow
} else {
    o.dispatchStep(...)       // normal agent/worker dispatch
}
state.ActiveSteps[step.ID] = true
```

### Negotiate = true (FIPA CFP path)

The Orchestrator **broadcasts a Call for Proposals** on the step's public task queue. Any
registered agent capable of handling that `activity_type` can respond with a `PROPOSE`. The
current implementation auto-accepts the first proposal.

```
convID  = buildConversationID(instancePath, step.ID)
subject = NormalizeTaskQueue(step.ActivityType, step.TaskQueue)

Envelope { perf: CFP, cid: <convID>, body: TaskDefinition }
```

### Negotiate = false (Direct dispatch path)

Sends an `ACCEPT_PROPOSAL` directly to a resolved inbox. Resolution priority:

1. **Worker prefix** (`workers.*`): calls `NormalizeTaskQueue` → `worker.inbox.<id>`
2. **`step.TaskQueue` set**: used verbatim as the inbox subject.
3. **Neither**: Almanac lookup via `resolveActorByCapability(activity_type)`.

### 10.3 Branching, Routing & Suspension

Toro supports dynamic DAG branching and conditional execution. While the `workers.switch` worker is a specialized tool for calculating routes (especially for arrays), the Orchestrator itself natively evaluates routing logic based on a `route` integer present in any step's output (Agent or Worker).

#### The `route` Field Protocol
The Orchestrator extracts the route value from the step's "proof" (output JSON) using the following priority:
1.  **Root level lookup:** `{"route": 1}`
2.  **Protocol-wrapped lookup:** `{"data": {"route": 1}}` (standard for FIPA `INFORM` bodies).
3.  **Array first-item:** `[{"route": 1}, ...]` (Common when routing arrays of data).

#### `route_condition` (Branching)
Use `route_condition` to gate the execution of a step based on the route value returned by its parent.

```yaml
  - id: process_payment
    activity_type: workers.payment_processor
    depends_on:
      - validate_order
    route_condition:
      step_id: validate_order   # optional, defaults to first entry in depends_on
      values: [0]                # only runs if validate_order returned route 0
```

#### `suspend_routes` (Suspension & Ambiguity)
If a step returns a route matching one of its `suspend_routes`, the Orchestrator halts execution and moves the workflow into a **suspended** state. This is primarily used for Human-in-the-Loop (HITL) or handling ambiguity that requires external resolution.

```yaml
  - id: map_columns
    activity_type: agents.accounting.map_csv
    suspend_routes: [1]                 # e.g., route 1 signifies "Ambiguity detected"
    suspension_reason_path: "reason"    # Optional JSON path to extract the reason
```

When a workflow is suspended:
1.  The instance state in Postgres is updated with `Suspended = true`, `SuspensionStep`, and `SuspensionReason`.
2.  A `workflow.events.suspended` event is published to NATS, carrying the current proof, route, and reason.
3.  The Orchestrator stops scheduling new steps until a `workflow.resume` message is received (via the `workflow.resume` NATS subject).

Once resumed, the Orchestrator clears the suspension flags and calls `scheduleReadySteps` to continue the DAG.


### Sub-Workflows

A step with `sub_workflow` set causes the orchestrator to **compose** a fully independent child
workflow instead of dispatching to an agent or worker. This is the mechanism for reusing complete
workflows inside a larger DAG without coupling the two YAML files together.

#### YAML declaration

```yaml
# bookkeeping.yaml
steps:
  - id: run_cleanup
    sub_workflow: "CSV Cleaner Pipeline"   # exact blueprint name in DB
    timeout: "300s"
    description: "Run the CSV cleanup pipeline first. Bookkeeping continues when it completes."

  - id: run_rule_engine
    activity_type: workers.rule_bootstrap
    depends_on:
      - run_cleanup                        # waits for the sub-workflow to finish
```

`csv_cleaner_pipeline.yaml` is **entirely unchanged** and remains independently triggerable via
its own `trigger_topic`.

#### How it works — code trace

**Phase 1 — `scheduleReadySteps` detects the sub-workflow step** (`orchestrator.go:1534`)

```go
if step.SubWorkflow != "" {
    err = o.spawnSubWorkflow(ctx, step, state, entityID, payload)
}
state.ActiveSteps["run_cleanup"] = true   // parent waits here
```

**Phase 2 — `spawnSubWorkflow` creates a child instance** (`orchestrator.go:917`)

```go
childDef, _ := o.resolveBlueprintByName(ctx, step.SubWorkflow)
//   loads "CSV Cleaner Pipeline" blueprint from DB/cache

childInstanceID := uuid.New()
childPath := append(parentState.InstancePath..., childInstanceID.String())
//   e.g. ["bk-uuid-1234", "csv-uuid-5678"]

childState := InstanceState{
    WorkflowDef:  "CSV Cleaner Pipeline",
    InstancePath: childPath,       // parent UUID at index [0]
    ParentStepID: step.ID,        // = "run_cleanup" ← breadcrumb back to parent
}
// Persisted to DB, then scheduleReadySteps fires on the child def
```

The parent (Bookkeeping) instance sits idle with `run_cleanup` in `ActiveSteps`. The child runs
its own full DAG — `map_columns → commit_mapped_columns → run_enrichment` — entirely via normal
NATS dispatch. The parent is not involved.

**Phase 3 — Child's last step completes** (`orchestrator.go:1291`)

```go
if len(state.CompletedSteps) == len(wfDef.Steps) {
    // CSV Cleaner: 3/3 steps done
    o.publishStatus(..., "completed", ...)

    if state.ParentStepID != "" && len(state.InstancePath) > 1 {
        parentPath := state.InstancePath[:len-1]   // = ["bk-uuid-1234"]
        o.completeParentStep(ctx, parentPath, "run_cleanup", proofCopy)
    }
}
```

**Phase 4 — `completeParentStep` resumes the bookkeeping DAG** (`orchestrator.go:1336`)

```go
// Loads bookkeeping instance from DB using bk-uuid-1234
// Calls handleStepCompletion(bookkeeping, stepID="run_cleanup", proof)
//   → delete(ActiveSteps, "run_cleanup")
//   → CompletedSteps["run_cleanup"] = true
//   → scheduleReadySteps fires
//       run_rule_engine: depends_on=[run_cleanup] ✓ → dispatched
```

#### Full execution trace

```
events.accounting.bookkeeping published
  │
  ▼ handleTrigger (Bookkeeping Workflow)
  │   scheduleReadySteps → run_cleanup ready (no deps)
  │   step.SubWorkflow = "CSV Cleaner Pipeline"
  │   → spawnSubWorkflow()
  │       childState.ParentStepID = "run_cleanup"
  │       childPath = ["bk-uuid", "csv-uuid"]
  │       scheduleReadySteps(childDef) → dispatches map_columns
  │   Bookkeeping: ActiveSteps = {"run_cleanup": true}  ← waiting
  │
  ▼ [CSV Cleaner runs independently via NATS]
  │   map_columns      → INFORM → handleStepCompletion(child)
  │   commit_mapped_columns → INFORM → handleStepCompletion(child)
  │   run_enrichment   → INFORM → handleStepCompletion(child)
  │       3/3 complete → COMPLETED
  │       ParentStepID = "run_cleanup" → completeParentStep()
  │
  ▼ completeParentStep → handleStepCompletion(Bookkeeping, "run_cleanup")
      CompletedSteps["run_cleanup"] = true
      scheduleReadySteps → run_rule_engine now satisfies depends_on ✓
      → dispatched → bookkeeping continues normally
```

#### Properties

| Property | Value |
|---|---|
| `csv_cleaner_pipeline.yaml` modified? | **No** — zero changes |
| Can CSV Cleaner still run standalone? | **Yes** — still has its own `trigger_topic` |
| Coupling between the two YAMLs | Only: the string `"CSV Cleaner Pipeline"` (blueprint name in DB) |
| Concurrency safe? | Yes — child and parent are separate DB rows with separate `SequenceID` |
| Nested sub-workflows? | Supported — `InstancePath` is a slice, `completeParentStep` walks it |

### Dynamic Delegation & Pagination

A step does not have to explicitly declare a `SubWorkflow` to run one. An Agent or Worker can dynamically spawn an ephemeral sub-workflow at runtime using the `DELEGATE` performative.

When the Orchestrator receives a `DELEGATE` envelope, it dynamically generates an ephemeral `WorkflowDef` based on the steps provided by the agent. It enforces two safety limits:
1. **`MaxDynamicDelegationSteps = 50`**: An agent cannot request > 50 steps at once.
2. **`MaxDelegationDepth`**: A static primitive on the `WorkflowDef` (e.g., `max_delegation_depth: 3`). The Orchestrator rejects delegations if `len(InstancePath)` exceeds this limit.

#### The Generic Delegation Worker (`workers.delegator`)

To utilize Dynamic Delegation natively in YAML without writing Go code, you can use the generic delegation worker. It reads an array from the JSON payload (populated via `workflow_schema`), chunks it based on `batch_size`, and issues a `DELEGATE` to fan out execution.

```yaml
  - id: fan_out_transactions
    activity_type: workers.delegator
    workflow_schema: |
      {
        "type": "object",
        "properties": {
          "unclassified_transactions": { "type": "array" }
        }
      }
    config:
      batch_size: 50
      target_sub_workflow: "categorize_batch"
```

If the incoming array exceeds 2,500 items (50 steps * 50 batch size), the `delegator` worker recursively adds a final pagination step to chunk the remainder once the first batch finishes, preventing DAG bloat and enabling infinite horizontal scale.

So now, There are actually two distinct ways you can trigger dynamic sub-workflows now.

1. The AI/Custom Agent Way (No workers.delegator needed)
Because the Orchestrator now natively understands the DELEGATE performative, any agent can decide to spawn a sub-workflow.

For example, imagine you write a custom Go agent called a "Research Agent" that uses an LLM. The LLM decides a problem is too complex and tells your Go code to break it into 3 parts. Your Go agent can programmatically construct a DELEGATE envelope with those 3 steps and send it to the Orchestrator. In this scenario, the YAML workflow just calls your agents.research. The agent itself decides to delegate on the fly. workers.delegator is completely uninvolved here.

2. The Static Array Way (Using workers.delegator)
Sometimes you don't have an intelligent AI agent deciding what to do. You just have a massive array of 2,000 transactions, and you know you want to fan them out into parallel categorization steps.

You shouldn't have to write custom Go code or invoke an LLM just to iterate over an array.

This is where workers.delegator comes in. It is a "dumb", generic worker we built for convenience. You explicitly put it in your YAML, and it acts as a bridge: it takes an array from the payload, chunks it up, and automatically fires the DELEGATE envelope for you.

#### The Unified Generic Batch Agent (`agents.accounting.batch_categorization`)

When the Orchestrator runs a dynamic sub-workflow for chunked arrays (like the `Categorize Batch Sub-Workflow`), the sub-workflow's steps often require LLM processing on the array chunk.

Instead of writing custom Go code for each specific type of batch categorization (e.g., *Macro Classification*, *Entity Selection*, *Account Type Selection*), Toro utilizes a single, powerful **Generic Batch Agent** (`tap/agents/generic_batch_agent`).

This unified architecture relies heavily on Redux JSON Patch functionality:
1. **Dynamic Prompting**: The agent does not hardcode any logic. It reads the `SystemPrompt` straight from the Orchestrator's execution context (defined entirely in your YAML workflow).
2. **Auto-Array Detection**: It scans the `batch_payload` config injected by the `delegator` worker to find the array of transactions.
3. **Agnostic JSON Patching**: The LLM is instructed to return an array of JSON objects (each containing an `id`). The Generic Batch Agent iterates over these results and dynamically generates index-based JSON patches mapping to the Redux state (e.g., `{"op": "add", "path": "/unclassified_transactions/0/macro_class", "value": "EXPENSE"}`). 

Because the agent maps *any* keys returned by the LLM into the Redux array, you can define entirely new batch categorization agents by simply creating new steps in YAML with different `system_prompt`s, without ever compiling new Go code.

---

## 11. Proof Correlation — The ConversationID Contract

This is the key mechanism that allows one shared inbox to serve all active workflow instances
simultaneously.

### At dispatch time

```go
convID := buildConversationID(instancePath, step.ID)
// Flat:   "550e8400-e29b-41d4-a716-446655440000/map_columns"
// Nested: "bk-uuid/csv-uuid/run_enrichment"
```

`buildConversationID` joins the stored `instance_path` with `/` separators, so nested workflows
automatically produce conversation IDs that encode the full ancestry.

### Agents/Workers echo it back

Every agent and worker **must** copy the `ConversationID` verbatim from the task envelope into its
INFORM proof envelope. This is the only coupling contract between the Orchestrator and its actors.

### At receipt (`handleIncoming`)

```go
parts        := strings.Split(env.ConversationID, "/")
instancePath := parts[:len(parts)-1]
stepID       := parts[len(parts)-1]
instanceID   := instancePath[len(instancePath)-1]  // UUID → DB lookup
```

With `instanceID` the Orchestrator fetches `InstanceState` from Postgres and advances the DAG.

---

## 12. State Machine

### InstanceState (stored as JSONB in `toro_core.workflows`)

```json
{
  "workflow_def":      "CSV Cleaner Pipeline",
  "current_step_id":  "map_columns",
  "instance_path":    ["550e8400-e29b-41d4-a716-446655440000"],
  "active_steps":     {"map_columns": true},
  "completed_steps":  {},
  "variables":        {},
  "last_proof":       null,
  "parent_step_id":   "",
  "suspended":        false,
  "suspension_step":  "",
  "suspension_route": 0,
  "suspension_reason": ""
}
```

For a sub-workflow child the `parent_step_id` is set and `instance_path` has two UUIDs
(`[parent-uuid, child-uuid]`).

### Transitions

| Event | Action |
|---|---|
| Trigger received | Insert `workflow` row, schedule entry steps |
| INFORM received, more steps remain | Mark step complete, schedule next ready steps |
| INFORM received, all steps complete | Set `current_step_id = "COMPLETED"`, publish `workflow.events.completed`, notify parent if sub-workflow |
| INFORM step not in `ActiveSteps` | Ack + discard (stale/duplicate) |
| Suspend route hit | Set `Suspended = true`, publish `workflow.events.suspended` + `workflow.events.ambiguous` |
| `workflow.resume` received | Clear suspension, re-run `scheduleReadySteps` |
| DB / dispatch error | Nak (redelivered by NATS up to `MaxDeliver`) |

`buildStepPayload` merges proofs from all `DependsOn` entries into `{"dependencies": {"step_id": <proof>}}`,
wraps with any per-step `config`, and stores under `state.Variables[stepID]`. Fan-in/fan-out
works naturally — each downstream step reads only its declared dependency step IDs.

---

## 13. Almanac Discovery

When a step has no explicit `task_queue` and its `activity_type` does not use the `workers.*`
prefix, the Orchestrator performs a live discovery call:

```go
o.resolveActorByCapability(ctx, step.ActivityType)
```

Sends a `QUERY_REF` request to `core.SubjectAlmanacQuery`:

```json
{ "caller_did": "did:toro:orchestrator", "capability_type": "<activity_type>" }
```

The Almanac responds with matching agent entries. The Orchestrator takes the first match.
Timeout: **5 seconds**. If no agent is found, step dispatch fails and the message is Nak'd.

---

## 14. Blueprint Query API

The Orchestrator exposes a synchronous blueprint lookup on:

```
workflow.query.blueprint
```

Send the workflow **name** or **trigger topic** as the message body; the Orchestrator responds
with the full `WorkflowDef` JSON. Used to render the step graph in the UI.

---

## 15. Status Events

On every state transition, `publishStatus` fires:

```
workflow.events.<status>
```

Where `<status>` ∈ `started`, `running`, `completed`, `suspended`.

When a switch step hits a `suspend_routes` value, `workflow.events.ambiguous` is also published
with `proof`, `route`, and `suspension_reason` for WebSocket consumers.

Event payload:

```json
{
  "instance_id":     "<uuid>",
  "entity_id":       "<uuid>",
  "status":          "running",
  "current_step_id": "commit_mapped_columns",
  "assigned_did":    "",
  "blueprint":       { "...full WorkflowDef..." },
  "active_steps":    ["commit_mapped_columns"],
  "timestamp":       "2026-05-08T22:00:00Z"
}
```

These are fire-and-forget publishes (errors logged, not fatal).

---

## 16. Error & Retry Semantics

| Scenario | NATS action | Consequence |
|---|---|---|
| Malformed envelope on inbox | `msg.Term()` | Dead-lettered; no redeliver |
| Malformed ConversationID | `msg.Term()` | Same |
| DB fetch error | `msg.Nak()` | Redelivered up to `MaxDeliver` times |
| Next step dispatch error | `msg.Nak()` | Redelivered; idempotency depends on actor |
| Stale/duplicate step proof | `msg.Ack()` | Silently discarded |
| Panic in trigger handler | `msg.Nak()` (recover) | Redelivered |
| Unknown performative | `msg.Ack()` | Logged and dropped |
| FAILURE performative received | Suspend instance, `msg.Ack()` | `workflow.events.suspended` emitted |

DLQ subjects: `workflow.dlq.trigger`, `workflow.dlq.inbox`.

---

## 17. Scaling & Horizontal Safety

- **Inbox consumer** (`orchestrator-inbox-durable`) is a durable queue group `orchestrator-group`.
  Multiple Orchestrator replicas can run; NATS delivers each message to exactly one.
- **Trigger consumer** (`orchestrator-triggers`) uses `DeliverGroup = "orchestrator-trigger-group"`.
  Same horizontal safety.
- **Blueprint cache** (`blueprints []WorkflowDef`) is per-process. All replicas call
  `SyncBlueprints()` independently. A write to the DB by one replica is not auto-propagated to
  peers — a restart or periodic re-sync is required.
- **`SequenceID`** on the workflow row is incremented on every `UpdateWorkflowState` call.
  Currently used as an optimistic ordering signal (not a CAS lock). Concurrent processing of the
  same instance across replicas would require a DB advisory lock if that becomes a concern.

---

## 18. CSV Cleaner Pipeline — Reference Shape

`tap/workflows/csv_cleaner_pipeline.yaml`:

| Phase | Subject | Publisher | Subscriber |
|---|---|---|---|
| Trigger | `events.accounting.*.cleanup` | API/ingestion producer | JetStream `WORKFLOW_TRIGGERS` |
| Trigger delivery | `orchestrator.triggers.deliver` | JetStream consumer | Orchestrator |
| Step 1 CFP | `tasks.accounting.1.map_csv` | Orchestrator | CSV Mapping Agent(s) |
| Agent/worker reply | `orchestrator.inbox` | Agents/Workers | Orchestrator |
| Step 2 direct | `worker.inbox.csv-mapping-worker` | Orchestrator | CSV Mapping Worker |
| Step 3 direct | `worker.inbox.enrichment-worker` | Orchestrator | Enrichment Worker |

The `suspend_routes: [1]` on `map_columns` means ambiguous polarity halts the pipeline and emits
`workflow.events.ambiguous` before `commit_mapped_columns` runs. The downstream commit step only
executes when `route_condition` equals `0`.

When composed inside `bookkeeping.yaml` as a `sub_workflow`, the pipeline runs identically —
there is no difference from its perspective whether it was triggered by a NATS event or spawned by
a parent orchestrator.

---

## 19. Practical Checklist for Adding a Step

1. Choose `activity_type`:
   - Agent: `agents.<domain>.<task>`
   - Worker: `workers.<domain>.<task>`
   - Composed pipeline: `sub_workflow: "<Blueprint Name>"`
2. Set `complexity` for agent steps (`1`, `5`, `10`).
3. Set `task_queue`:
   - Agent: usually omit (core derives from `activity_type` + `complexity`)
   - Worker: worker id (e.g. `csv-mapping-worker`)
4. Set `negotiate` (`true` for agents via CFP, `false` for workers/direct).
5. Set `timeout`.
6. Declare `depends_on` for all upstream steps the step requires.
7. Optionally set `route_condition` if this step only runs on a specific switch route.
8. Ensure actor exists:
   - Agent module registered + config present in Almanac
   - Worker registered via `RegisterFactory`

---

## 20. Who Owns Sequencing

- **Orchestrator** owns topology and sequencing entirely.
- **Agents and workers** do not decide the next step subject — they only echo the `ConversationID`
  back in their proof.
- **Workers** are passive executors; NATS ack/nak controls retry semantics.
- **Workflow durability** is DB-backed (`toro_core.workflows`) with NATS as transport, not
  long-term state store.
- **Sub-workflow composition** happens at the orchestrator level — child workflows are fully
  autonomous instances whose completion automatically signals the parent step as done.
