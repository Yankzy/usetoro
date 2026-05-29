# CSV Mapping Agent (`tap/agents/csv_mapping`)

## Purpose

The CSV Mapping Agent is the first agent step in the CSV cleaner workflow. It takes raw CSV rows, asks the LLM to infer column mapping, normalizes rows into a canonical shape, and writes the result into workflow state via Redux.

It supports:
- single `amount` column or split `debit`/`credit` columns
- optional `vendor` and `customer` columns
- sign-convention inference (`is_expense_positive`)

## Config (`go/internal/config/defaults.yml`)

```yaml
- name: "CSV Mapping Agent"
  model: "gpt-5.4-mini"
  engine: "internal"
  internal_module: "csv-mapping-agent"
  activity_type: "agents.accounting.map_csv"
  workflow_schema: '{"type":"object","properties":{"status":{"type":"string"},"mapped_rows":{"type":"object"}}}'
  dependencies:
    database: false
    db_queries: true
    entity_resolver: false
```

At runtime, the agent subscribes to:
- private inbox: `agents.<did>.inbox`
- derived task queue for its activity type: `tasks.accounting.1.map_csv`

## Runtime Flow

1. The handler accepts only `CFP` envelopes. Non-`CFP` messages are logged and ignored.
2. It sends a `PROPOSE` reply (`price: 1`, `eta: "10s"`) to `core.BuildAgentInbox(env.SenderDID)`.
3. It immediately executes the task (no separate wait for `ACCEPT_PROPOSAL`).
4. It runs `ExecuteGlobalWorkflow(...)` with Redux RBAC limited to `["/status", "/mapped_rows"]`.
5. After Redux validation succeeds, it publishes an `INFORM` proof to `orchestrator.inbox`.

## Payload Contract

The task body is a `core.TaskDefinition` whose `payload` matches:

```json
{
  "session_id": "<uuid>",
  "realm_id": "<uuid>",
  "rows": [
    ["Date", "Description", "Amount", "Balance"],
    ["2024-01-15", "AMZN DIGITAL", "-9.99", "1234.56"]
  ]
}
```

Compatibility fallback:
- if `session_id` is empty, the agent falls back to `upload_id` from the same payload

Important identity semantics:
- `task.id` = workflow instance UUID (used for Redux trace + proof `task_id`)
- `payload.session_id` = business upload/session context copied into mapped rows

## Redux Mutations

The LLM callback creates RFC6902 patches:
- optional optimistic concurrency test on `/status` when present:
  `{"op":"test","path":"/status","value":<current>}`
- set status:
  `{"op":"add","path":"/status","value":"COLUMNS_MAPPED"}`
- write mapped rows:
  `{"op":"add","path":"/mapped_rows","value":{...}}`

## Row Normalization Rules (`parseRows`)

- starts at row index `1` (skips first row)
- removes `*` artifacts and trims whitespace from extracted fields
- if split amounts are configured, `debit` is preferred; otherwise `credit`
- includes a row only when both `description` and `amount` are non-empty
- output is keyed as `row_<index>` in a map, not an array

## Prompting Behavior (`buildUserPrompt`)

- combines `Cfg.SystemPrompt` + generated user prompt
- includes up to 20 rows with at least 2 non-empty columns
- instructs the LLM to return strict JSON with a confidence score
- `extractJSONToMapping` tolerates markdown-wrapped responses by slicing the first `{...}` block

## Output Proof

The validated result is sent as an `INFORM` envelope to `orchestrator.inbox`.  
Envelope body is a `core.Proof` where:
- `task_id` = workflow instance UUID (`task.id`)
- `type` = `proof.api`
- `data` = validated `/mapped_rows` object

Example `proof` body:

```json
{
  "task_id": "9ec1f778-41c7-4be4-9fd5-5fcf0878892f",
  "type": "proof.api",
  "ts": 1714527600,
  "data": {
    "row_1": {
      "SessionID": "upload_123",
      "RealmID": "realm_abc",
      "Date": "2024-01-15",
      "Description": "AMZN DIGITAL",
      "Amount": "-9.99",
      "Vendor": "",
      "Customer": ""
    }
  },
  "sig": "<ed25519-signature>"
}
```

## Error Handling

- poison pill protection: deliveries `> 3` are terminated with `msg.Term()`
- transient failures return `msg.Nak()` for retry
- `"insufficient funds"` is treated as stalled-paywall: logs into `stalled_messages` then `msg.Term()`
