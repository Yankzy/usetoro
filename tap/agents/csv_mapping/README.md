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

## Core Concepts

The CSV Mapping Agent follows the standard **Toro Agent Pattern**, centering around three core components:

### 1. The `Handler` (JetStream Inbox)
The agent listens on a dedicated JetStream subject (via `BaseAgent.Sub`). The handler includes mandatory **Poison Pill** protection: if a message fails more than 3 times, it is terminated to prevent infinite retry loops.

### 2. The `llmCallback` (Reasoning & Correction)
This closure is passed to the Redux engine. It is **retry-aware**:
- It receives a list of `redux.DomainFault`s from previous failed attempts.
- It uses `agent.Runtime` (via `ExecWithPaging`) to prompt the LLM.
- It returns a set of JSON Patches (RFC 6902) to be applied to the workflow state.
- **Fail-Safe**: If Redux rejects the patches (e.g., due to RBAC or Schema violations), this callback is re-invoked with the errors, allowing the LLM to self-correct.

### 3. The `onComplete` Hook (Side Effects)
This closure is called **only after** Redux has successfully validated and persisted the state transition.
- **Standard Purpose**: It is the designated place for publishing "informed" results or **Proofs** to JetStream.
- **Guarantee**: By the time `onComplete` runs, the internal database is updated, and the transition is immutable.

---

## Redux State Engine Integration

This agent performs deterministic state transitions via the Toro `tap/pkg/redux` engine using the `ExecuteGlobalWorkflow` lifecycle.

### The 5-Phase Execution Lifecycle

1.  **Hydrate**: Fetches the current workflow state and sequence ID from the database.
2.  **Boot**: Initializes a fresh Redux Store with the agent's specific **RBAC** and **Schema** policies.
3.  **Reason**: Enters the `llmCallback` loop (up to 3 retries) until valid patches are generated.
4.  **Trace**: Publishes the validated event to the `workflow.trace.<id>` subject for observability.
5.  **Finalize**: Invokes `onComplete` to trigger downstream side effects (e.g., publishing a Proof).

### Practical Safeguards

- **RBAC Policy**: Actively scopes mutation authority down to exactly `["/status", "/mapped_rows"]`.
- **Schema Validation**: Ensures the resulting state matches expected JSON structures.
- **Optimistic Concurrency**: Uses `op: test` patches to prevent race conditions during heavy state updates.
- **Idempotency**: Sequence IDs are tracked to prevent double-processing on JetStream redeliveries.

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
