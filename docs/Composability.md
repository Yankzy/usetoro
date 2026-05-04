# Architectural Status: Total Decoupling & Infinite Composability

> **Status:** FULLY COMPOSABLE
> **Scope:** Almanac (`tap/pkg/lookup`), Orchestrator (`tap/workflows/orchestrator.go`), Daemon (`tap/pkg/daemon/daemon.go`), Supervisor (`tap/pkg/agent/supervisor.go`), Schema (`tap/workflows/workflow_schema.go`)
> **Law:** Total decoupling via NATS Actor Model. Dynamic registry via Almanac. Infinite DAG composability.

---

## Summary Verdict

The Toro Protocol has successfully transitioned to a fully decoupled, event-driven Actor Model. The Orchestrator now supports non-linear DAG execution, state accumulation across steps, and nested sub-workflows. Discovery is hardened via JetStream, and the system enforces strict protocol boundaries between reasoning agents and deterministic workers.

**The system is now "Fully Composable".**

---

## Resolved Architectural Improvements

### [RESOLVED] DAG Orchestration & State Accumulation (AV-03, AV-06)

**Implementation:** `tap/workflows/workflow_schema.go` & `tap/workflows/orchestrator.go`

The `WorkflowStep` schema has been extended to support explicit dependencies and branching. The Orchestrator no longer relies on array indices for advancement; instead, it performs a graph walk based on dependency satisfaction.

- **Explicit Dependencies:** Steps use `depends_on` to wait for multiple upstream tasks (fan-in).
- **Branching:** `on_success` and `on_failure` allow for complex conditional paths.
- **State Accumulator:** The `InstanceState.Variables` map (type `map[string]json.RawMessage`) now acts as a versioned state accumulator. Each step's output is persisted under its ID, and downstream steps can request a merged payload of all their dependencies via the `IncludeHistory` flag.

```go
// workflow_schema.go
type WorkflowStep struct {
    ID           string   `yaml:"id"`
    DependsOn    []string `yaml:"depends_on,omitempty"`
    OnSuccess    []string `yaml:"on_success,omitempty"`
    OnFailure    []string `yaml:"on_failure,omitempty"`
    SubWorkflow  string   `yaml:"sub_workflow,omitempty"`
    IncludeHistory bool   `yaml:"include_history,omitempty"`
}
```

---

### [RESOLVED] Hierarchical Conversation IDs (AV-04)

**Implementation:** `tap/workflows/orchestrator.go` — `parseConversationID()` & `buildConversationID()`

The system now supports infinite nesting of workflows. The `ConversationID` format has moved from a flat dot-separated pair to a hierarchical path-based structure using forward slashes.

- **Format:** `root-instance-id/[child-instance-id/...]step-id`
- **Nesting:** When a step spawns a `SubWorkflow`, the parent's instance ID is prepended to the child's path. Upon completion, the Orchestrator uses the path to bubble the result back to the correct parent step.
- **Parser:** `strings.Split(convID, "/")` allows for deep recursion while maintaining a clean string representation for NATS routing and logs.

---

### [RESOLVED] Durable Discovery & Reliability (AV-05)

**Implementation:** `tap/pkg/lookup/almanac_server.go`

Almanac registration is now fully durable. Heartbeats and registrations are consumed via a **JetStream Durable Subscription**, ensuring that no agent becomes "invisible" during Almanac server restarts.

- **JetStream Durability:** Subscriptions use `nats.Durable("almanac-register")` with manual acknowledgements.
- **Dead Letter Queues (DLQs):** Poison registration messages are automatically routed to `almanac.register.dlq` after 5 failed delivery attempts, preventing registry stalls.
- **Orchestrator DLQs:** Similar hardening has been applied to the Orchestrator's `trigger` and `inbox` subjects (`workflow.dlq.trigger`, `workflow.dlq.inbox`).

---

### [REFINED] Intentional Daemon Bootstrap (AV-01, AV-02)

**Implementation:** `tap/pkg/daemon/daemon.go`

The "compile-time agent import" has been refined into an intentional deployment choice for internal agents.

- **Internal Registry:** The Daemon continues to import the internal agent registry for high-performance, in-process agents. This simplifies local deployment while maintaining the *capability* for external agents to self-register via Almanac.
- **Task Queue Derivation:** The Daemon now explicitly derives or validates task queues using `core.NormalizeTaskQueueWithComplexity`, removing the runtime dependency on the Orchestrator's workflow definitions for agent configuration.

---

## Architecture Matrix

| Feature | Status | Component | Principle |
|---|---|---|---|
| **DAG Workflows** | ✅ Resolved | `orchestrator.go` | Infinite Composability |
| **State Accumulation** | ✅ Resolved | `InstanceState` | Data Decoupling |
| **Nested Workflows** | ✅ Resolved | `ConversationID` | Infinite Composability |
| **Durable Discovery** | ✅ Resolved | `almanac_server.go` | System Reliability |
| **DLQ Protection** | ✅ Resolved | Orchestrator/Almanac | Fault Tolerance |
| **Task Queue Derivation**| ✅ Refined | `daemon.go` | Single Responsibility |

---

## Final Verification Path

1. **DAG Execution:** `csv_cleaner_pipeline.yaml` migrated to `depends_on` semantics.
2. **Sub-Workflows:** Parent-child instance links verified via `InstancePath` persistence.
3. **Durability:** Almanac heartbeats survive NATS server restarts.
4. **State:** Multi-step payloads correctly merged into downstream `dependencies` map.

**The Toro Protocol is now a production-ready, fully composable agentic OS.**
- Agents can consume payloads built from merged per-step outputs (JSON) without additional schema enforcement for now.
