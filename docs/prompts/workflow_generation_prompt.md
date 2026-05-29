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
- `system_prompt` (string, optional - instructions for the agent)
- `workflow_schema` (string, optional - JSON schema for Redux validation)
- `rbac_policy` (array of strings, optional - allowed Redux state paths for the agent, e.g., `["/status", "/mapped_rows"]`)
- `depends_on` (array of step IDs controlling DAG execution)
- `route_condition` (optional `{ step_id: "<dependency>", values: [...] }` that matches the upstream `route` value)
- `suspend_routes` (optional array of route integers that pause the workflow until a resume signal)
- `sub_workflow` (string, optional — the exact `name` of another registered workflow blueprint to embed as a step)

Important: do NOT include `complexity` (it is not part of the current workflow step schema).

Sub-workflow step rules:
- When `sub_workflow` is set, omit `activity_type`, `negotiate`, `system_prompt`, `workflow_schema`, and `rbac_policy` — they are irrelevant.
- The child workflow runs its full DAG as a child instance. The parent step is marked complete only when the child reaches `COMPLETED`.
- Always set a generous `timeout` (e.g. `"300s"`) to accommodate the child's full runtime.
- Downstream steps can declare `depends_on: [<sub_workflow_step_id>]` exactly like any other step.

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

Sub-Workflow Composition
- Use `sub_workflow` when the workflow you are generating must first run a complete, independently-registered pipeline before continuing its own steps.
- The referenced workflow runs as a child instance. Neither YAML file imports the other — coupling is only the blueprint `name` string.
- The child's final proof is forwarded to the parent's `LastProof` and `Variables`, so downstream steps receive it naturally.
- The child workflow can still be triggered standalone via its own `trigger_topic`. Composition does not affect standalone operation.
- Do NOT add `on_complete` hooks or any other coupling inside the child YAML.

Sub-workflow composition example:

```yaml
name: "Bookkeeping Workflow"
version: "1.0"
trigger_topic: "events.accounting.bookkeeping"
steps:
  - id: run_cleanup
    sub_workflow: "CSV Cleaner Pipeline"   # exact blueprint name
    timeout: "300s"
    description: "Run the full CSV cleanup pipeline first. Bookkeeping continues when it completes."

  - id: run_rule_engine
    activity_type: workers.rule_bootstrap
    negotiate: false
    timeout: "300s"
    description: "Bootstrap vendor rules. Runs after cleanup because all rows are now ENRICHED."
    depends_on:
      - run_cleanup

  - id: classify_transaction
    activity_type: agents.accounting.classify_outflow
    negotiate: false
    timeout: "60s"
    depends_on:
      - run_rule_engine
```

Generation Constraints (LLM-Friendly)

- Use `version: "1.0"` unless specified otherwise.
- Step IDs must be short snake_case and unique.
- Keep timeouts realistic (`10s`, `60s`, `120s`, etc.).
- Ensure step order is executable (outputs from earlier steps can feed later steps).
- Avoid comments unless essential.
- Output must be strict YAML only.

One-shot Learning Examples

Example 1 — `tap/workflows/csv_cleaner_pipeline.yml` (flat DAG)

Step sequence:
- `map_columns` (agent, negotiate true)
- `commit_mapped_columns` (worker, direct, depends_on map_columns)
- `run_enrichment` (worker, direct, depends_on commit_mapped_columns)

Legacy note from that file:
- it includes `complexity` keys on some agent steps
- for newly generated workflows, omit `complexity` in final output to match the current schema contract

Example 2 — `tap/workflows/bookkeeping.yml` (sub-workflow composition)

Step sequence:
- `run_cleanup` (sub_workflow: "CSV Cleaner Pipeline") — runs the entire cleanup DAG as a child
- `run_rule_engine` (worker, direct, depends_on run_cleanup)
- `classify_transaction` (agent, negotiate false, depends_on run_rule_engine)
- `classify_inflow` (agent, negotiate false, depends_on run_rule_engine)
- `select_account_type` (agent, negotiate true, depends_on classify_transaction + classify_inflow)
- `select_entity` (agent, negotiate true, depends_on select_account_type)
- `select_account` (agent, negotiate true, depends_on select_entity)

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
    system_prompt: |
      You are the CSV Mapping Agent. Analyze the headers and rows to...
    workflow_schema: |
      { "type": "object", "properties": { "status": { "type": "string" } } }

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
    system_prompt: "You are the Revenue Reconciliation Agent. Match rows to customers..."

  - id: reconcile_expense
    activity_type: agents.accounting.reconcile_expense
    negotiate: true
    timeout: "60s"
    description: "Process negative rows and assign vendor/account signals."
    system_prompt: "You are the Expense Reconciliation Agent. Match rows to vendors..."
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
