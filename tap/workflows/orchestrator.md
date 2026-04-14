# Orchestrator

`tap/workflows/orchestrator.go`

The `Orchestrator` is the central control-plane service for TAP. It owns the full lifecycle of a workflow instance: receiving external triggers, persisting state, dispatching work to Agents and Workers over NATS, correlating proofs on completion, and advancing the state machine to the next step.

---

## Table of Contents

1. [Design Philosophy](#1-design-philosophy)
2. [Key Constants](#2-key-constants)
3. [Struct Layout](#3-struct-layout)
4. [Startup Sequence](#4-startup-sequence)
5. [Blueprint Registry](#5-blueprint-registry)
6. [Trigger Ingestion](#6-trigger-ingestion)
7. [Step Dispatch](#7-step-dispatch)
8. [Proof Correlation — The ConversationID Contract](#8-proof-correlation--the-conversationid-contract)
9. [State Machine](#9-state-machine)
10. [Almanac Discovery](#10-almanac-discovery)
11. [Blueprint Query API](#11-blueprint-query-api)
12. [Status Events](#12-status-events)
13. [Error & Retry Semantics](#13-error--retry-semantics)
14. [Scaling & Horizontal Safety](#14-scaling--horizontal-safety)

---

## 1. Design Philosophy

- **Stateless orchestration, stateful DB.** The Orchestrator holds no per-instance in-memory state. Every decision is driven by rows in `toro_core.workflows`. NATS provides transport; Postgres provides durability.
- **Single inbox.** All replies (PROPOSE bids, INFORM proofs) arrive at one subject — `orchestrator.inbox`. Routing to the correct workflow instance is achieved purely through the FIPA `ConversationID` field.
- **Dynamic discovery.** Agents are found at dispatch time via the Almanac service, not hardcoded. The Orchestrator never imports agent packages.
- **One JetStream consumer for all triggers.** A single durable push consumer (`orchestrator-triggers`) fans out messages from the unified `WORKFLOW_TRIGGERS` JetStream stream, regardless of how many active workflow trigger topics exist.

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

The in-memory `blueprints` slice is a **read-only snapshot** used for subject matching. The Postgres DB is the source of truth. A write to `blueprints` only ever happens inside `SyncBlueprints()`, protected by `blueprintMu`.

---

## 4. Startup Sequence

`Orchestrator.Start(ctx)` proceeds in strict order:

```
1. Subscribe to orchestrator.inbox          → handleIncoming (PROPOSE / INFORM)
2. SyncBlueprints()                         → refresh in-memory snapshot + reconcile JetStream stream subjects
3. startUnifiedTriggerSubscription()        → single QueueSubscribe on WorkflowTriggerDeliverSubject
4. Subscribe to workflow.query.blueprint    → handleBlueprintQuery (API Gateway lookups)
5. Block on ctx.Done()
6. Drain all subscriptions on shutdown
```

If `SyncBlueprints` finds no blueprints, step 3 logs a warning and skips — the Orchestrator starts cleanly but is inactive.

---

## 5. Blueprint Registry

### Loading from YAML

On first boot, `LoadFromDir(ctx, dirPath)` scans a directory for `*.yaml` files, parses each into a `WorkflowDef`, and **upserts** the result into the `workflow_blueprints` Postgres table. The DB is the runtime registry; the YAML files are the bootstrap source.

### Syncing to Memory

`SyncBlueprints(ctx)` reads all rows from the DB, deserialises each `WorkflowDef`, rebuilds the in-memory `[]WorkflowDef` slice, and then **reconciles the WORKFLOW_TRIGGERS JetStream stream** subjects to exactly match the current set of `trigger_topic` values across all blueprints. This allows new workflow definitions to be registered at runtime without restarting the Orchestrator.

### Subject Matching

When a trigger message arrives, the Orchestrator calls `resolveBlueprintForSubject(subject)` which iterates all blueprints and uses `matchNATSSubject(pattern, subject)` — a full NATS wildcard implementation supporting `*` (one token) and `>` (rest of subject). Ties are broken by `triggerPatternScore`, which ranks exact-token specificity higher than wildcards.

---

## 6. Trigger Ingestion

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
    └─ dispatchStep(ctx, steps[0], instanceID, triggerPayload)
```

The trigger envelope is expected to be a FIPA `Envelope{ perf: INFORM, body: TaskDefinition }` where `TaskDefinition.Payload` is a JSON object containing at minimum `entity_id` (a UUID). If `entity_id` is absent the trigger is **rejected** — the instance is not created.

The raw trigger payload (unwrapped inner bytes) is forwarded as-is to the first step.

---

## 7. Step Dispatch

`dispatchStep(ctx, step, instanceID, payload)` constructs a FIPA `Envelope` and publishes it. Behaviour depends on `step.Negotiate`:

### Negotiate = true (FIPA CFP path)

The Orchestrator **broadcasts a Call for Proposals** on the step's public task queue. Any registered agent capable of handling that `activity_type` can respond with a `PROPOSE`.

```
convID  = instanceID + "." + step.ID
subject = NormalizeTaskQueue(step.ActivityType, step.TaskQueue)
         e.g. tasks.accounting.1.map_csv

Envelope {
    perf: CFP
    cid:  <convID>
    body: TaskDefinition{ ID: instanceID, Domain: activity_type, Payload: ... }
}
```

The current implementation **auto-accepts the first PROPOSE** it receives (logged, Ack'd). A real bid-selection strategy is left as a TODO.

### Negotiate = false (Direct dispatch path)

The Orchestrator sends an `ACCEPT_PROPOSAL` directly to a resolved inbox. Resolution priority:

1. **Worker prefix** (`workers.*`): calls `NormalizeTaskQueue` → `worker.inbox.<id>`
2. **`step.TaskQueue` set**: used verbatim as the inbox subject.
3. **Neither**: Almanac lookup via `resolveActorByCapability(activity_type)`.

```
Envelope {
    perf: ACCEPT_PROPOSAL
    cid:  <convID>
    body: TaskDefinition{ ... }
}
```

---

## 8. Proof Correlation — The ConversationID Contract

This is the key mechanism that allows one shared inbox to serve all active workflow instances simultaneously.

### At dispatch time

```go
convID := instanceID + "." + step.ID
// e.g. "550e8400-e29b-41d4-a716-446655440000.map_columns"
```

This compound key is embedded in the FIPA `ConversationID` (`cid`) field of every outbound envelope.

### Agents/Workers echo it back

Every agent and worker **must** copy the `ConversationID` verbatim from the task envelope it received into its INFORM proof envelope. This is the only coupling contract between the Orchestrator and its actors.

### At receipt (handleIncoming)

```go
parts       := strings.Split(env.ConversationID, ".")
instanceID  := parts[0]   // → UUID → DB lookup
stepID      := parts[1]   // → validates against state.CurrentStepID
```

With `instanceID` the Orchestrator fetches `InstanceState` from Postgres, which contains:
- `workflow_def` → which blueprint to use
- `current_step_id` → guard against out-of-order / duplicate proofs

If `stepID != state.CurrentStepID` the message is Ack'd and discarded (stale).

---

## 9. State Machine

### InstanceState (stored as JSONB in toro_core.workflows)

```json
{
  "workflow_def":    "CSV Cleaner Pipeline",
  "current_step_id": "map_columns",
  "variables":       {}
}
```

### Transitions

| Event | Action |
|---|---|
| Trigger received | Insert `workflow` row with `current_step_id = steps[0].ID` |
| INFORM received, next step exists | Update `current_step_id = nextStep.ID`, dispatch next step |
| INFORM received, no next step | Update `current_step_id = "COMPLETED"`, publish `workflow.events.completed` |
| INFORM step ID mismatch | Ack + discard (stale/duplicate) |
| DB / dispatch error | Nak (message redelivered by NATS) |

The **payload passed to each next step is the raw `env.Body`** of the INFORM proof from the previous step. Agents/workers control data forwarding implicitly through what they put in their proof body.

---

## 10. Almanac Discovery

When a step has no explicit `task_queue` and its `activity_type` does not use the `workers.*` prefix, the Orchestrator performs a live discovery call:

```go
o.resolveActorByCapability(ctx, step.ActivityType)
```

This sends a `QUERY_REF` request to `core.SubjectAlmanacQuery` with:

```json
{
  "caller_did":      "did:toro:orchestrator",
  "capability_type": "<activity_type>"
}
```

The Almanac responds with a list of registered agent entries. The Orchestrator takes the first match and uses either its `endpoints[0]` or derives the inbox via `core.BuildAgentInbox(did)`.

Timeout: **5 seconds**. If no agent is found, the step dispatch fails and the message is Nak'd.

---

## 11. Blueprint Query API

The Orchestrator exposes a synchronous blueprint lookup service on:

```
workflow.query.blueprint
```

The API Gateway (or any other service) can send the workflow **name** or **trigger topic** as the message body, and the Orchestrator responds with the full `WorkflowDef` JSON. Used to render the step graph in the UI.

---

## 12. Status Events

On every state transition, `publishStatus` fires an event on:

```
workflow.events.<status>
```

Where `<status>` is one of `started`, `running`, `completed`.

Event payload:
```json
{
  "instance_id":     "<uuid>",
  "entity_id":       "<uuid>",
  "status":          "running",
  "current_step_id": "commit_mapped_columns",
  "assigned_did":    "",
  "blueprint":       { ... full WorkflowDef ... },
  "timestamp":       "2026-04-13T05:00:00Z"
}
```

These are fire-and-forget publishes (errors are logged, not fatal).

---

## 13. Error & Retry Semantics

| Scenario | NATS action | Consequence |
|---|---|---|
| Malformed envelope on inbox | `msg.Term()` | Message is dead-lettered; no redeliver |
| Malformed ConversationID | `msg.Term()` | Same as above |
| DB fetch error | `msg.Nak()` | Redelivered up to `MaxDeliver` times |
| Next step dispatch error | `msg.Nak()` | Redelivered; idempotency depends on actor |
| Stale/duplicate step proof | `msg.Ack()` | Silently discarded |
| Panic in trigger handler | `msg.Nak()` (recover) | Redelivered |
| Unknown performative | `msg.Ack()` | Logged and dropped |

---

## 14. Scaling & Horizontal Safety

- **Inbox consumer** (`orchestrator-inbox-durable`) is a durable queue group `orchestrator-group`. Multiple Orchestrator replicas can run; NATS delivers each message to exactly one.
- **Trigger consumer** (`orchestrator-triggers`) uses `DeliverGroup = "orchestrator-trigger-group"`. Same horizontal safety.
- **Blueprint cache** (`blueprints []WorkflowDef`) is per-process. All replicas must call `SyncBlueprints()` independently. A write to the DB by one replica is not auto-propagated to peers — a restart or periodic re-sync is required.
- **`SequenceID`** on the workflow row is incremented on every `UpdateWorkflowState` call. It is currently used as an optimistic ordering signal (not a CAS lock), so concurrent processing of the same instance ID across replicas would require an advisory lock at the DB level if that becomes a concern.
