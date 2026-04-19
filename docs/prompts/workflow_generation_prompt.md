TAP Workflow Generation Prompt

Copy the block below and fill in the `[PLACEHOLDERS]` before sending it to an LLM.

---

````
You are an expert systems architect. Generate a valid workflow YAML for the TAP Orchestrator.

Workflow Specification

- Workflow Name: "[WORKFLOW_NAME]"
- Trigger Topic: "[TRIGGER_TOPIC]"
- Description:
  [Describe the workflow purpose in 1-3 sentences.]
- Desired Steps:
  [List the ordered business steps in plain English.]

Current Schema You MUST Follow (`tap/workflows/workflow_schema.go`)

Top-level fields:
- `name` (string)
- `version` (string)
- `description` (string, optional)
- `trigger_topic` (string)
- `steps` (array)

Each step fields:
- `id` (string)
- `activity_type` (string)
- `task_queue` (string, optional)
- `negotiate` (bool)
- `timeout` (string duration, e.g. `"60s"`)
- `description` (string, optional)
- `depends_on` (array of step IDs controlling DAG execution)
- `route_condition` (optional `{ step_id: "<dependency>", values: [...] }` that matches the upstream `route` value)
- `suspend_routes` (optional array of route integers that pause the workflow until a resume signal)
- `sub_workflow` (optional workflow reference for nested compositions)

Important: do NOT include `complexity` (it is not part of the current workflow step schema).

Routing Rules

1. Activity type prefixes
- Agent steps: `activity_type` starts with `agents.`
- Worker steps: `activity_type` starts with `workers.`

2. Queue resolution behavior
- Agents:
  - Prefer omitting `task_queue`.
  - Queue is canonically derived from activity type as `tasks.<domain>.1.<task>`.
- Workers:
  - You may omit `task_queue` and let it derive from activity type as `worker.inbox.<domain>.<task>`.
  - Or set `task_queue` to a worker id (e.g. `csv-mapping-worker`) and it becomes `worker.inbox.csv-mapping-worker`.
  - Full `worker.inbox.*` values are also valid.

3. Negotiate behavior
- `negotiate: true` -> orchestrator dispatches `CFP` to queue.
- `negotiate: false` -> orchestrator dispatches `ACCEPT_PROPOSAL` directly to resolved queue.

4. Completion expectation
 - Step actors should return `INFORM` envelopes to `orchestrator.inbox` with the same conversation id.
 - Orchestrator advances sequencing from those inbox messages.

Dependency graph & payloads
- Express branching and fan-in/fan-out by referencing prior steps with `depends_on`, `route_condition`, and `suspend_routes`.
- The orchestrator merges every dependency proof into `{"dependencies": {"step_id": <proof>, ...}}`, so downstream actors always read the merged JSON from `dependencies.<step_id>`.
- When a switch step suspends (e.g., `route == 1` for ambiguity), the workflow pauses until a `workflow.resume` with a new payload clears the suspension.

Generation Constraints (LLM-Friendly)

- Use `version: "1.0"` unless specified otherwise.
- Step IDs must be short snake_case and unique.
- Keep timeouts realistic (`10s`, `60s`, `120s`, etc.).
- Ensure step order is executable (outputs from earlier steps can feed later steps).
- Avoid comments unless essential.
- Output must be strict YAML only.

One-shot Learning Example (from `tap/workflows/csv_cleaner_pipeline.yaml`)

The current repo example has this sequence:
- `map_columns` (agent, negotiate true)
- `commit_mapped_columns` (worker, direct)
- `run_enrichment` (worker, direct)
- `reconcile_revenue` (agent, negotiate true)
- `reconcile_expense` (agent, negotiate true)

Legacy note from that file:
- it includes `complexity` keys on some agent steps
- for newly generated workflows, omit `complexity` in final output to match the current schema contract

Reference Shape (modernized from that one-shot)

```yaml
name: "CSV Cleaner Pipeline"
version: "1.0"
description: "Maps raw CSV rows, persists them, enriches, then reconciles."
trigger_topic: "events.accounting.*.cleanup"
steps:
  - id: map_columns
    activity_type: agents.accounting.map_csv
    negotiate: true
    timeout: "60s"
    description: "Map raw CSV columns into canonical row objects."

  - id: commit_mapped_columns
    activity_type: workers.database.insert_rows
    task_queue: csv-mapping-worker
    negotiate: false
    timeout: "10s"
    description: "Insert mapped rows into Postgres as PENDING."

  - id: run_enrichment
    activity_type: workers.database.enrich_rows
    negotiate: false
    timeout: "120s"
    description: "Enrich pending rows with deterministic + AI-assisted matching."

  - id: reconcile_revenue
    activity_type: agents.accounting.reconcile_revenue
    negotiate: true
    timeout: "60s"
    description: "Process positive rows and assign customer/account signals."

  - id: reconcile_expense
    activity_type: agents.accounting.reconcile_expense
    negotiate: true
    timeout: "60s"
    description: "Process negative rows and assign vendor/account signals."
```

What to Return

Return only the YAML source.
Do not return Markdown fences or explanations.
````

Placeholder Values

```env
WORKFLOW_NAME=""
TRIGGER_TOPIC=""
```
