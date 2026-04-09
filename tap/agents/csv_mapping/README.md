# CSV Mapping Agent (`tap/agents/csv_mapping`)

## Purpose

The **CSV Mapping Agent** is the first stage in the Toro CSV bank statement processing workflow. When a user uploads a raw bank statement CSV, this agent uses an LLM to analyse the raw column headers and map them to standardised accounting fields (`Date`, `Description`, `Amount`, `Vendor`, `Customer`).

It handles inconsistent bank formats — single-amount columns, split debit/credit columns, varying sign conventions — and produces a structured row mapping that downstream agents (`reconcile_expense`, `reconcile_revenue`) can operate on directly.

---

## Config (`defaults.yaml`)

```yaml
- did: "did:toro:agent:csv_mapping_1"
  name: "CSV Mapping Agent"
  model: "gpt-5.4-mini"
  engine: "internal"
  internal_module: "csv-mapping-agent"
  dependencies:
    database: true
    entity_resolver: false
```

---

## Redux State Engine Integration

This agent performs deterministic state transitions via the Toro `tap/pkg/redux` engine (via `ExecuteGlobalWorkflow`). To prevent hallucinated LLM data corruption, it enforces strict boundaries.

### Direct Usage

- **RBAC Policy**: Actively scopes its mutation authority down to exactly `["/status", "/mapped_rows"]`, denying all other JSON paths.
- **Schema Validation**: Provides a strict JSON schema (`properties: status, mapped_rows`) verified by the Redux Engine at runtime.
- **Optimistic Concurrency**: Tests `baseState` for existing values (via `op: test`) and pre-appends locking patches to prevent run-condition overlaps.
- **Fault-Aware Circuit Breaker**: The internal LLM pipeline captures rejected `DomainFaults` and recursively feeds them to the LLM (up to 3 times) for runtime self-correction. 

### Indirect (Framework) Usage

- **Payload Size & Array Bans**: The engine automatically guarantees patches do not exceed size specifications and do not replace nested arrays without precision.
- **Idempotency**: Execution sequence IDs are tracked safely across DB transactions, preventing redundant `JetStream` redeliveries.
- **Rollup Compression**: Event arrays are cleanly pushed via JetStream down to the daemon's background `RollupWorker`.

---

## Message Flow

```
[User Upload]
     │
     ▼
events.accounting.1.csv_mapping     ← Listens here (JetStream, queue group: csv-mapping-group)
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
proof.accounting.csv_mapping.columns    ← Publishes TAP Envelope (INFORM) with Proof payload
```

---

## Input Payload

The agent expects a `core.TaskDefinition` body containing a `csvMappingTaskPayload`:

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

Published to `proof.accounting.csv_mapping.columns` as a `core.Envelope` (`INFORM`) containing a `core.Proof`:

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
| `CSVMappingAgent` | Main struct embedding `*agent.BaseAgent` + `*agent.Runtime` + `*database.Queries` |
| `LLMColumnMapping` | JSON struct the LLM must return — column indices and sign convention |
| `csvMappingTaskPayload` | The input expected inside the `TaskDefinition.Payload` |
| `RawRow` | Normalised output row (one per CSV data row) |

---

## Poison Pill & Error Handling

- Messages delivered **more than 3 times** are terminated (`msg.Term()`) to avoid infinite retry loops.
- Transient errors → `msg.Nak()` (redelivered by JetStream).
- `"insufficient funds"` errors → message is logged to `stalled_messages` table and terminated.
