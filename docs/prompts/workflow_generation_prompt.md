TAP Workflow Generation Prompt

Copy the block below and fill in the `[PLACEHOLDERS]` before sending it to an LLM.

---

````
You are an expert system architect. Your task is to generate a declarative Workflow Definition YAML file representing a NATS-based pipeline, orchestrating Agents and Workers.

Workflow Specification

- Pipeline Name: "[PIPELINE_NAME]"
- Trigger Topic: "[TRIGGER_TOPIC]"
- Description:
  [Describe what the pipeline does. e.g., "Fetches external HR data, normalizes it, and syncs to a 3rd party."]
- Steps:
  [List the actors involved and their actions. e.g., "1. Normalization Agent reads trigger and proves normalized struct. 2. DB Worker inserts rows. 3. Sync Agent hits external API."]

---

Framework Contracts You MUST Follow

1. Architecture Boundaries

- **Agents** (`activity_type: agents.*`): Perform intelligence/computation via LLM. They DO NOT write to databases.
- **Workers** (`activity_type: workers.*`): Perform extra work before or after agent and make database mutations. They DO NOT make LLM calls.

2. Routing Model (Temporal-FIPA Hybrid)

The Workflow Orchestrator owns all step routing. Agents and Workers do NOT hardcode upstream or downstream topics.

- `complexity` is required for agent steps and must be one of: `1` (entry), `5` (junior), `10` (senior).
- `task_queue` routing:
  - Agents: optional. If omitted, orchestrator derives route from `activity_type` + `complexity` using `core.BuildTaskSubject(...)` (via `core.BuildTaskSubjectFromActivity`).
  - Workers: required as worker id (e.g. `csv-mapping-worker`) and orchestrator resolves it via `core.BuildWorkerInbox(worker_id)`.
- Message verbs are protocolized:
  - `negotiate: true` path uses `CFP` → `PROPOSE` → `ACCEPT_PROPOSAL`.
  - Step completion is returned as `INFORM` to `orchestrator.inbox`.
- Payment readiness:
  - `complexity` determines baseline reward policy in dispatched `TaskDefinition` (`reward`, `currency`, `expires_at` are filled by orchestrator).
- `negotiate: true` → the Orchestrator uses FIPA bidding. The winning agent receives the payload via their private inbox.
- `negotiate: false` → the Orchestrator dispatches directly to the provided `task_queue`.
- `timeout` → a wall-clock limit after which the Orchestrator marks the step FAILED and retries or compensates.

3. YAML Structure

Your output MUST be a strict YAML document conforming to this schema:

```yaml
name: "Name of the Workflow"
version: "1.0"
description: "High level description"
trigger_topic: "nats.topic.that.starts.it"
steps:
  - id: step_id
    activity_type: agents.accounting.map_csv     # use underscores for activity names
    complexity: 1                                # 1|5|10
    # task_queue optional for agents (derived if omitted)
    negotiate: true                              # true = FIPA bidding; false = direct dispatch
    timeout: "60s"                               # timeout
    description: "What this step does"
```

What to Return

Return only the YAML source. Do not return any Markdown wrapping or explanations.
````
