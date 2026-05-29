# Formalizing the Workflow Structure: Part 3 (Temporal, FIPA, & Discovery)

Based on your feedback, we are integrating **Temporal.io** core concepts (Workflows, Activities, Task Queues, Event History) alongside our FIPA Agentic-Ecommerce negotiation constraints. We are also ensuring that **Agents and Workers are discoverable and queryable by the frontend**, while strictly adhering to the event-driven dispatch architecture of our Workers.

## 1. Temporal x FIPA Architecture

To allow 3rd-party generic agents to bid on workflow steps while preserving guaranteed durability, the Orchestrator will implement a **Temporal-like Event Sourcing** model hybridized with FIPA messaging over NATS Jetstream.

### The Concepts
- **Workflow:** The macro pipeline definition (e.g., `CSV Cleaner Pipeline`). Managed by the Central Orchestrator service.
- **Activity:** A single task execution handled by an Agent or Worker (e.g., `map_columns`).
- **Task Queue:** The public NATS topic where the Orchestrator advertises the need for an Activity to be performed. Agents poll this queue.
- **Event History:** The append-only log in `toro_core.workflow_history` that tracks state transitions (`WorkflowExecutionStarted`, `ActivityTaskScheduled`, `ActivityTaskCompleted`). 

### FIPA Bidding Flow for 3rd Party Compatibility
1. **Activity Task Scheduled:** A file is uploaded. The Workflow starts. The Orchestrator looks at Step 1 (`map_columns`) which declares a Temporal **Task Queue** of `tasks.accounting.cleanup.mapping`.
2. **CFP Broadcast:** The Orchestrator publishes a `CFP` (Call for Proposal) envelope to that public Task Queue.
3. **Bidding:** Any Agent (internal or 3rd-party) listening to that Task Queue can reply with a `PROPOSE` envelope directed to the sender's private inbox (`orchestrator.inbox`).
4. **Assignment:** The Orchestrator selects the best bid, logs `ActivityTaskStarted` to `workflow_history`, and sends an `ACCEPT` envelope containing the payload to the specific winning agent's private inbox (`agent.inbox.{WINNER_DID}`).
5. **Completion:** The agent computes and publishes the final `Proof` via `INFORM` back to `orchestrator.inbox`.
6. **State Advance:** The Orchestrator logs `ActivityTaskCompleted`, updates the `toro_core.workflows` state, and advances to Step 2 (e.g., publishing to the next generic Task Queue for the DB Worker).

**Note on Workers:** Internal Workers (like DB writters) skip FIPA negotiation. The Orchestrator can push directly to their inbox, or send an auto-accepted task on the queue.

## 2. Reusing `toro_core.workflows` for the Macro State Machine

We will eschew a new table and heavily leverage the existing `toro_core.workflows` and `toro_core.workflow_history`.

- `toro_core.workflows` `state` JSONB will contain the Temporal Engine state map:
  ```json
  {
      "workflow_def": "CSV Cleaner Pipeline",
      "current_step_id": "commit_mapped_columns",
      "workflow_variables": { "session_id": "1234" },
      "pending_activities": ["map_columns_task_uuid"]
  }
  ```
- `toro_core.workflow_history` `content` JSONB will track the Temporal Event History transitions, guaranteeing that if the daemon crashes, the Orchestrator boots up, replays history, and issues timeouts or re-broadcasts stranded CFPs!

## 3. Discoverability (Frontend UI Queries)

To satisfy the requirement of "querying and showing agents and workers registered on the frontend," without violating the stateless `dispatch` architecture of `go/internal/workers` (which prohibits unbounded goroutines inside individual workers):

**Solution: Centralized Infrastructure Heartbeats**
We won't make individual Agents and Workers manage their own registration loops. It would pollute the LLM-generated code.
1. The **Worker Manager** (`workerManager.StartAll(ctx)`) already holds the registry of all loaded workers. The Manager itself will spawn a *single* background goroutine that polls its registry and publishes a batch `almanac.register` payload to NATS.
2. The **Agent Supervisor** (`daemon.go`) already holds the state of all active agents. It will do the exact same: one routine publishing the capabilities and `task_queues` of all its spun-up Agents.
3. **API Endpoint:** We will introduce `GET /v1/system/actors` in `go/internal/api/` which reads from the Almanac state and streams it to the user interface.

*For external 3rd-party ecommerce agents, since they are managed and run inside Firecracker microVMs on our infrastructure, the Firecracker control plane (or an injected sidecar) will publish heartbeats to the Almanac subject on their behalf, maintaining parity securely.*

## 4. Reusable Configuration (Updating YAML)

### Redesigning the YAML format to be Temporal-esque

#### `tap/workflows/csv_cleaner_pipeline.yml`
```yaml
name: CSV Cleaner Pipeline
version: "1.0"
trigger_topic: events.accounting.*.cleanup
steps:
  - id: map_columns
    activity_type: agents.accounting.map_csv
    task_queue: tasks.accounting.cleanup.mapping   # Public topic where CFP is broadcast
    negotiate: true                                # Allow 3rd party bids via PROPOSE
    timeout: "60s"
    
  - id: commit_mapped_columns
    activity_type: workers.database.insert_rows
    task_queue: worker.inbox.csv-mapping-worker    # Direct push to a known internal worker
    negotiate: false
    timeout: "10s"
```

## 5. Updates Required (Proposed Changes)

1. **[MODIFY] `go/internal/api/`**: Add a handler for querying the `Almanac` to serve the frontend registry view.
2. **[MODIFY] `tap/pkg/agent/supervisor.go` and `go/internal/workers/manager.go`**: Implement the centralized `almanac.register` heartbeat loop.
3. **[MODIFY] `tap/workflows/workflow_schema.go`**: Update structs to rename `InputTopic` to Temporal `TaskQueue`, and add `Negotiate` and `ActivityType` fields.
4. **[MODIFY] `tap/workflows/csv_cleaner_pipeline.yml`**: Update to the Temporal/FIPA schema above.
5. **[MODIFY] `tap/workflows/workflow_generation_prompt.md`**: Train the LLM to output Temporal-friendly Task Queues and negotiate blocks.
6. **[MODIFY] `tap/agents/agent_generation_prompt.md`**: Guide LLMs on the FIPA interaction mode (Listen on Task Queue -> Send PROPOSE -> Wait for ACCEPT on Inbox -> Do work -> Send INFORM Proof).

## 6. User Review Required (CTO Checkpoint)

> [!NOTE]
> **Redux vs Orchestrator Overlap in `workflows` Table**
> Because `toro_core.workflows` will now hold both the **Macro Orchestrator State** and any **Micro Agent Redux States**, we need a simple way to delineate them. I recommend the Orchestrator uses a specific `entity_id` (like a system-level ID) or we add a `type` string to the `state` JSONB. Are you comfortable with placing all Temporal state variables inside the existing `state` JSONB field?
