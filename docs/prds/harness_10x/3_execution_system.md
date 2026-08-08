# Product Requirements Document (PRD)

## 3. Execution System (Durable Orchestration & Negotiation)

**Document Reference:** `docs/prds/harness_10x/3_execution_system.md`  
**Status:** Approved Specification v1.0  
**Owner:** Runtime & NATS Transport Engineering  
**Subsystem:** System 3 of the 5 Core Harness Subsystems

---

# 1. Executive Summary & Core Responsibility

The **Execution System** handles the execution, verification, state rewinding, and outbound reality negotiation of tasks.

### Core Responsibility
*Execute the action chosen by the Decision System safely and durably.*

### Strict Decoupling Rule
The Execution System contains **zero decision theory math** and **zero global state modeling**. It strictly executes tasks, logs deterministic execution traces, and processes state mutations.

---

# 2. Execution Tracing & State Rewinding

Every execution step logs a structured `NodeExecutionStep` in the node's `ExecutionTrace`:

```json
{
  "dag_node_id": "macro_classifier_inflow",
  "kind": "macro_classifier",
  "property_key": "macro_class",
  "selected_edge": "EXPENSE",
  "timestamp": "2026-08-06T15:29:56Z"
}
```

### RFC 6902 State Mutations
All state changes are formatted as deterministic RFC 6902 JSON patch operations and processed through Redux `store.Reduce()` to prevent database corruption.

### State Rewinding (`Backtrack`)
When human context contradicts a past choice, `node.Backtrack(targetDAGNodeID)` purges downstream candidate classifications, recalculates confidence, and resumes execution without re-running valid upstream nodes.

---

# 3. Outward Reality Negotiation (TAP Protocol)

When the Decision System selects an outbound negotiation action (e.g. `queue_client_request`, `w9_lookup`), the Execution System dispatches messages using **Toro Agent Protocol (TAP)** performatives over NATS JetStream:

```
CFP (Call For Proposal) -> PROPOSE -> ACCEPT_PROPOSAL -> INFORM / PROOF
```

Decentralized Action Provider workers ([action_provider_workers.go](file:///Users/Yankz/programming/usetoro/go/internal/workers/action_provider_workers.go)) execute W-9 searches, receipt lookups, and tax calculations asynchronously.
