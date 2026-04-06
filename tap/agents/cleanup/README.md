# Cleanup Agent (`tap/agents/cleanup`)

## Purpose

The **Accounting Cleanup Agent** is the first stage in the Toro CSV bank statement processing pipeline. When a user uploads a raw bank statement CSV, this agent uses an LLM to analyse the raw column headers and map them to standardised accounting fields (`Date`, `Description`, `Amount`, `Vendor`, `Customer`).

It handles inconsistent bank formats — single-amount columns, split debit/credit columns, varying sign conventions — and produces a structured row mapping that downstream agents (`reconcile_expense`, `reconcile_revenue`) can operate on directly.

---

## Config (`defaults.yaml`)

```yaml
- did: "did:toro:agent:cleanup_1"
  name: "Accounting Cleanup Agent"
  model: "gpt-4o-mini"
  engine: "internal"
  internal_module: "cleanup-agent"
  dependencies:
    database: true
    entity_resolver: false
```

---

## Message Flow

```
[User Upload]
     │
     ▼
tasks.accounting.cleanup.>          ← Listens here (JetStream, queue group: cleanup-group)
     │
     ▼
handleCFP()                         ← Parses TAP Envelope, expects Performative: CFP
     │
     ├─► Publishes PROPOSE to sender inbox (price, eta)
     │
     └─► executeTask()
              │
              ▼
         mapRowsUsingLLM()          ← GPT-4o-mini analyses raw CSV rows
              │
              ▼
         parseRows()                ← Converts LLM mapping → []RawRow
              │
              ▼
         ExecuteGlobalWorkflow()    ← RFC6902 patch: sets /status + /mapped_rows on DB workflow
              │
              ▼
proof.accounting.cleanup.columns    ← Publishes TAP Envelope (INFORM) with Proof payload
```

---

## Input Payload

The agent expects a `core.TaskDefinition` body containing a `cleanupTaskPayload`:

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

---

## Output Proof

Published to `proof.accounting.cleanup.columns` as a `core.Envelope` (`INFORM`) containing a `core.Proof`:

```json
{
  "task_id": "<session_id>",
  "type": "proof.api",
  "data": [
    { "SessionID": "...", "RealmID": "...", "Date": "2024-01-15", "Description": "AMZN DIGITAL", "Amount": "-9.99" }
  ]
}
```

---

## Key Types

| Type | Description |
|---|---|
| `CleanupAgent` | Main struct embedding `*agent.BaseAgent` + `*agent.Runtime` + `*database.Queries` |
| `LLMColumnMapping` | JSON struct the LLM must return — column indices and sign convention |
| `cleanupTaskPayload` | The input expected inside the `TaskDefinition.Payload` |
| `RawRow` | Normalised output row (one per CSV data row) |

---

## Poison Pill & Error Handling

- Messages delivered **more than 3 times** are terminated (`msg.Term()`) to avoid infinite retry loops.
- Transient errors → `msg.Nak()` (redelivered by JetStream).
- `"insufficient funds"` errors → message is logged to `stalled_messages` table and terminated.
