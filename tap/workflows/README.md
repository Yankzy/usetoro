# TAP Workflow Orchestrator

This package runs the workflow control-plane for TAP.  
It resolves workflow blueprints, dispatches step work over NATS, and advances workflow state in Postgres.

## 1) What subscribes to what

### Orchestrator subscriptions
- `orchestrator.inbox` (durable queue sub): receives step replies (`PROPOSE`, `INFORM`).
- `orchestrator.triggers.deliver` (queue sub): unified delivery subject for trigger stream consumer.
- `workflow.query.blueprint` (queue sub): serves blueprint lookup requests.

### Agent subscriptions (`tap/pkg/agent/base.go`)
- Private inbox: `core.BuildAgentInbox(<did>)` → `agents.<did>.inbox`.
- Public task queue (optional, assigned by orchestrator): derived/normalized from workflow `activity_type`, `complexity`, and optional `task_queue`.

### Worker subscriptions (`go/internal/workers/*.go`)
- Workers declare concrete subjects via `Subscriptions()`.
- Common pattern: `worker.inbox.<worker_id>` (often produced by workflow `task_queue: <worker_id>` and normalized with `core.BuildWorkerInbox`).

## 2) NATS subject construction (core/topics.go)

Canonical constructors live in `tap/pkg/core/topics.go`:

- `core.BuildTaskSubject(domain, complexity, taskType)`  
  → `tasks.<domain>.<complexity>.<task_type>`
- `core.BuildTaskSubjectFromActivity("agents.accounting.map_csv", 1)`  
  → `tasks.accounting.1.map_csv`
- `core.BuildWorkerInbox("csv-mapping-worker")`  
  → `worker.inbox.csv-mapping-worker`
- `core.BuildAgentInbox("did:toro:agent:abc")`  
  → `agents.did:toro:agent:abc.inbox`
- `core.BuildEventSubject(domain, complexity, taskType)`  
  → `events.<domain>.<complexity>.<task_type>`

Queue normalization is centralized in:
- `core.NormalizeTaskQueue(...)`
- `core.NormalizeTaskQueueWithComplexity(...)`

These are used by workflow step routing (`tap/workflows/workflow_schema.go`) and runtime loading paths (daemon/supervisor/base agent).

## 3) Verbs (performatives) and validation

Protocol verbs are defined in `tap/pkg/core/verbs.go`:
- `CFP`, `PROPOSE`, `ACCEPT_PROPOSAL`, `REJECT_PROPOSAL`
- `INFORM`, `QUERY_REF`
- `REQUEST`, `REFUSE`, `FAILURE`

Validation:
- `core.IsValidPerformative(...)` is enforced when creating envelopes (`core.NewEnvelope`) and on orchestrator/worker ingress guards.

## 4) Envelope + primitives in workflow execution

### Envelope (`tap/pkg/core/envelope.go`)
Every control message is a `core.Envelope`:
- `id`, `ts`, `src`, `dst`, `perf`, `cid`, `body`, `sig`

### Task primitive (`tap/pkg/core/primitives.go`)
Orchestrator dispatches steps as `core.TaskDefinition` in envelope body:
- `ID`: workflow instance id
- `Domain`: step `activity_type`
- `Complexity`: validated complexity (`1|5|10`)
- `Reward` + `Currency`: baseline payment metadata (currently policy-driven in orchestrator; settlement activation-ready)
- `Payload`: prior step payload/proof
- `ExpiresAt`: dispatch expiry derived from step timeout

### Proof primitive
Agents/workers typically return `core.Proof` in `INFORM` envelopes to `orchestrator.inbox`.

## 5) End-to-end routing model

### Trigger phase
1. Producer publishes trigger event (usually `events.*`).
2. JetStream stream `WORKFLOW_TRIGGERS` captures the trigger subjects.
3. Orchestrator consumes via one consumer deliver subject: `orchestrator.triggers.deliver`.
4. It resolves matching blueprint by trigger subject pattern and creates workflow instance state in DB.

### Step dispatch phase
For each step:
1. Resolve queue with `WorkflowStep.ResolveTaskQueue()` (core normalization).
2. Resolve complexity with `WorkflowStep.ResolveComplexity()` (`NormalizeTaskComplexity`).
3. Build `TaskDefinition`.
4. Send:
   - `negotiate: true` → `CFP` published to task queue.
   - `negotiate: false` → `ACCEPT_PROPOSAL` published directly to resolved inbox/queue.

### Dependency graph & payloads
The orchestrator no longer assumes sequential steps—`WorkflowStep.DependsOn` now declares the DAG, `scheduleReadySteps` only fires when each dependency is complete, and optional `route_condition` filters (typically used with `workers.switch`) gate the next steps. `buildStepPayload` merges every dependency proof into `{"dependencies": {"step_id": <proof>, ...}}`, wraps the result with any per-step `config`, and stores it in `state.Variables`. Fan-in/fan-out just works because each downstream step only reads the proofs for the `step_id`s it declared. `suspend_routes` can pause the instance (recording `suspension_reason`), and a `workflow.resume` payload written back into `state.Variables[stepID]` clears the suspension before resuming scheduling.

`sub_workflow` steps spin up child instances (their conversation IDs look like `root/child/step`), and when the child finishes its final proof is merged back into the parent’s `variables` so the parent’s DAG continues without extra wiring.

When a route step (like `ambiguity_gate`) hits a `suspend_routes` value, the orchestrator publishes to `workflow.events.ambiguous` so downstream systems (WebSocket rooms, dashboards) see the exact `proof`, `route`, and `suspension_reason` even though the workflow is paused.

### Completion phase
1. Orchestrator receives incoming envelope on `orchestrator.inbox`.
2. Validates performative.
3. On `INFORM`, parses `cid` (`<instanceID>.<stepID>`), loads workflow state, advances next step or marks completed.

## 6) CSV Cleaner pipeline (current shape)

`tap/workflows/csv_cleaner_pipeline.yaml`:
- Agent step example:
  - `activity_type: agents.accounting.map_csv`
  - `complexity: 1`
  - `task_queue` omitted → derived queue: `tasks.accounting.1.map_csv`
- Worker step example:
  - `activity_type: workers.database.insert_rows`
  - `task_queue: csv-mapping-worker` (worker id)
  - normalized queue: `worker.inbox.csv-mapping-worker`
- Switch gate example:
  - `activity_type: workers.switch`
  - routes on `dependencies.map_columns.is_ambiguous`
  - `suspend_routes: [1]` so ambiguity blocks downstream steps until manual resume

### CSV Cleaner subject map (publisher/subscriber)

| Phase | Subject | Publisher | Subscriber |
|---|---|---|---|
| Trigger | `events.accounting.*.cleanup` | API/ingestion producer | JetStream `WORKFLOW_TRIGGERS` |
| Trigger delivery | `orchestrator.triggers.deliver` | JetStream consumer | Orchestrator |
| Step 1 CFP | `tasks.accounting.1.map_csv` | Orchestrator | CSV Mapping Agent(s) |
| Agent proposal/proof return | `orchestrator.inbox` | Agents/Workers | Orchestrator |
| Ambiguity gate | `worker.inbox.switch` | Orchestrator | `workers.switch` |
| Step 2 direct dispatch | `worker.inbox.csv-mapping-worker` | Orchestrator | CSV Mapping Worker |
| Step 3 direct dispatch | `worker.inbox.enrichment-worker` | Orchestrator | Enrichment Worker |
| Step 4 CFP | `tasks.accounting.1.reconcile_revenue` | Orchestrator | Revenue Agent(s) |
| Step 5 CFP | `tasks.accounting.1.reconcile_expense` | Orchestrator | Expense Agent(s) |

Every step sees a payload shaped like `{"dependencies": {"map_columns": {...}, "ambiguity_gate": {...}}}` thanks to `buildStepPayload`, so `ambiguity_gate` can mark `route 1` as ambiguous, `suspend_routes` halts before `commit_mapped_columns`, and the downstream reconciliation agents only run when `route_condition` equals `0`. This matches the orchestrator’s DAG scheduler without needing agent-side schema enforcement.

## 7) Message shape by stage

- Trigger event: usually `core.Envelope{ perf: INFORM, body: TaskDefinition }`.
- Negotiated step dispatch: `core.Envelope{ perf: CFP, body: TaskDefinition }`.
- Direct step dispatch: `core.Envelope{ perf: ACCEPT_PROPOSAL, body: TaskDefinition }`.
- Step completion: `core.Envelope{ perf: INFORM, body: Proof }`.
- Discovery lookup: request on `core.SubjectAlmanacQuery` with capability payload.

## 8) Who owns sequencing

- Orchestrator owns topology and sequencing.
- Agents/workers do not decide next step subjects.
- Workers are passive executors; manager ack/nak controls retry semantics.
- Workflow durability is DB-backed (`toro_core.workflows`) with NATS delivery for transport, not long-term state.

## 9) Practical checklist for adding a step

1. Choose `activity_type`:
   - Agent: `agents.<domain>.<task>`
   - Worker: `workers.<domain>.<task>`
2. Set `complexity` for agent steps (`1`, `5`, `10`).
3. Set `task_queue`:
   - Agent: optional override (usually omit, let core derive)
   - Worker: worker id (preferred) or full `worker.inbox.<id>`
4. Set `negotiate` and `timeout`.
5. Ensure actor exists:
   - Agent module registered + config present
   - Worker registered via `RegisterFactory`
