# Architectural Vulnerability Audit: Decoupling & Composability

> **Auditor:** Principal Distributed Systems Architect
> **Scope:** Almanac (`tap/pkg/lookup`), Orchestrator (`tap/workflows/orchestrator.go`), Daemon (`tap/pkg/daemon/daemon.go`), Supervisor (`tap/pkg/agent/supervisor.go`), Schema (`tap/workflows/workflow_schema.go`), Config (`go/internal/config/defaults.yaml`)
> **Law:** Total decoupling via NATS Actor Model. Dynamic registry via Almanac. Infinite DAG composability.

---

## Summary Verdict

The system has made solid progress toward a decoupled Actor Model. The Orchestrator's pivot to Almanac-based runtime discovery is correct. **Four real composability gaps remain, plus two coupling trade-offs** to watch. The main blockers are the linear `WorkflowStep` schema (no DAGs), lack of nested conversation IDs, lossy Almanac registration, and missing state accumulation across steps. The Daemon's agent import and task-queue enrichment are intentional for in-process agents today and should only be changed if you move to a plugin or external-agent model.

---

## Findings (gaps and trade-offs)

---

### [AV-01] Daemon Bootstrap: Static Compile-Time Agent Import (Trade-off, not bug)

**File/Function:** `tap/pkg/daemon/daemon.go` — `Run()`, lines 19, 98–100

**Current design:**

```go
// daemon.go
import (
    "github.com/Yankzy/usetoro/tap/agents" // ← HARD IMPORT
)

// Run()
for name, factory := range agents.GetRegistry() {
    d.Supervisor.RegisterInternalAgent(name, factory)
}
```

The Daemon imports the in-process agent registry and registers compiled-in agents at boot. This is an expected pattern for internal Go agents that ship inside the binary. It does mean adding a new internal agent requires a rebuild, but that matches the current deployment model.

**When to change:**
If/when you run agents as separate binaries or plugins, remove the direct import and let agents self-register via Almanac (JetStream) heartbeats. Until then, keep the registry import; it keeps Supervisor plumbing simple for internal agents.

```go
// Future external/plug-in agents:
// daemon.go — drop the registry import
// agents publish to almanac.register on startup instead
// go/cmd/protocol/main.go keeps blank imports for any built-in agents you still ship
```

---

### [AV-02] Daemon: Orchestrator Leaks Task Queue Topology into Agent Configuration (Trade-off)

**File/Function:** `tap/pkg/daemon/daemon.go` — `Run()` lines 116–121, `loadConfig()` lines 310–316

**Current behavior:**

```go
// Run()
taskQueues := d.Orchestrator.GetTaskQueues()
for i, cfg := range d.currentConfig.Agents {
    if tq, ok := taskQueues[cfg.ActivityType]; ok {
        d.currentConfig.Agents[i].TaskQueue = tq
    }
}
```

The Daemon pulls `TaskQueue` from loaded workflow blueprints via `GetTaskQueues()` and overwrites agent configs on each load/hot-reload. This hides the source of truth inside workflows but keeps task queues single-sourced and avoids duplication in `defaults.yaml`.

**Risk/Trade-off:** Config mutation is non-obvious and couples agents to workflow topology, but it is functionally consistent today. If you want clearer boundaries, duplicate task queues explicitly in agent config and remove the mutation loop; otherwise document this enrichment so operators understand where the value comes from.

---

### [AV-03] WorkflowStep Schema: No Branch/Condition Field — DAG is Structurally Impossible (Confirmed)

**File/Function:** `tap/workflows/workflow_schema.go` — `WorkflowStep` struct

**The Violation:**

```go
type WorkflowStep struct {
    ID           string `yaml:"id" json:"id"`
    ActivityType string `yaml:"activity_type" json:"activity_type"`
    TaskQueue    string `yaml:"task_queue" json:"task_queue"`
    Negotiate    bool   `yaml:"negotiate" json:"negotiate"`
    Timeout      string `yaml:"timeout" json:"timeout"`
    Description  string `yaml:"description,omitempty" json:"description,omitempty"`
    // ← NO: DependsOn, Condition, OnSuccess, OnFailure, BranchType
}
```

The schema has **no concept of dependencies, branching, or conditional next-steps**. The Orchestrator's entire step-advancement logic is a hardcoded linear traversal:

```go
// orchestrator.go handleIncoming()
for i, s := range wfDef.Steps {
    if s.ID == stepID {
        if i+1 < len(wfDef.Steps) {  // ← FATAL: Pure array index = linear pipeline only
            nextStep = &wfDef.Steps[i+1]
        }
        break
    }
}
```

`Steps[i+1]` is not a DAG. It is a linked list with ordinal coupling. There is **zero mechanism** for:
- Parallel fan-out (two steps starting simultaneously after one completes)
- Conditional branching (if step A succeeds → B, if fails → C)
- Nesting a sub-workflow as a step (infinite composability)
- Fan-in (wait for multiple parallel steps to complete before proceeding)

**The Fix:**

Redesign `WorkflowStep` to carry an explicit, declarative next-step graph:

```go
// workflow_schema.go

type WorkflowStep struct {
    ID           string `yaml:"id" json:"id"`
    ActivityType string `yaml:"activity_type" json:"activity_type"`
    TaskQueue    string `yaml:"task_queue" json:"task_queue"`
    Negotiate    bool   `yaml:"negotiate" json:"negotiate"`
    Timeout      string `yaml:"timeout" json:"timeout"`
    Description  string `yaml:"description,omitempty" json:"description,omitempty"`

    // DAG fields — enable branching and composition
    DependsOn []string   `yaml:"depends_on,omitempty" json:"depends_on,omitempty"` // IDs of steps that must complete first
    OnSuccess []string   `yaml:"on_success,omitempty" json:"on_success,omitempty"` // Step IDs to dispatch on INFORM (success)
    OnFailure []string   `yaml:"on_failure,omitempty" json:"on_failure,omitempty"` // Step IDs to dispatch on FAILURE

    // Composability: trigger a nested workflow
    SubWorkflow string `yaml:"sub_workflow,omitempty" json:"sub_workflow,omitempty"` // Workflow name to spawn as a child
}
```

Then the Orchestrator's advance logic becomes a graph walk, not an array index:

```go
// orchestrator.go — replace the i+1 loop with:
func (o *Orchestrator) nextSteps(def WorkflowDef, completedStepID string) []WorkflowStep {
    var next []WorkflowStep
    for _, s := range def.Steps {
        for _, dep := range s.DependsOn {
            if dep == completedStepID {
                // check all of s.DependsOn are in completed set before appending
                next = append(next, s)
                break
            }
        }
    }
    return next
}
```

For `SubWorkflow`, the dispatch logic fires a trigger event on the child workflow's `trigger_topic`, embedding the parent `instance_id` for continuation.

---

### [AV-04] Orchestrator: `handleIncoming` — ConversationID Parser is Flat, Blocks Nested Workflows (Confirmed once nesting is added)

**File/Function:** `tap/workflows/orchestrator.go` — `handleIncoming()`, lines 764–773

**The Violation:**

```go
parts := strings.Split(env.ConversationID, ".")
if len(parts) < 2 {
    o.logger.Error("Orchestrator: malformed conversation ID", "id", env.ConversationID)
    msg.Term()
    return
}
instanceIDStr := parts[0]
stepID := parts[1]
```

The `ConversationID` format is `instanceID.stepID`. UUID v4 contains **hyphens, not dots**, so this split is correct for a UUID. However, `instanceID` is itself a UUID (`parts[0]`), and `stepID` is `parts[1]`. But UUIDs don't contain dots, so `parts[0]` is correctly the UUID. The real violation is subtler: **this schema assumes a flat, single-level instance ID**. In a nested workflow model (a sub-workflow spawned by step B of parent workflow A), the conversation ID has no structure for expressing the call stack:

```
// Current (flat):
"conv_id": "e4f1c2a3-...<uuid>...b9.map_columns"

// What nested workflows require:
"conv_id": "parent-uuid.child-uuid.map_columns"
//          └── parent   └── child  └── step
```

When the sub-workflow's INFORM arrives, the Orchestrator cannot determine which parent instance to advance without a hierarchical ID structure.

**The Fix:**

Adopt a structured conversation ID that carries the full execution context:

```go
// New ConversationID format:
// "<root-instance-id>/<child-instance-id>/<step-id>"
// For top-level: "<instance-id>/<step-id>"

convID := fmt.Sprintf("%s/%s", instanceID, step.ID)
// Nested: fmt.Sprintf("%s/%s/%s", parentInstanceID, childInstanceID, step.ID)
```

The parser becomes:

```go
parts := strings.SplitN(env.ConversationID, "/", 3)
// parts[0] = root instance ID
// parts[1] = current instance ID (same as root for top-level)
// parts[2] = step ID
```

This allows the Orchestrator to resume the correct parent when a sub-workflow completes.

---

## HIGH Vulnerabilities

---

### [AV-05] Almanac: Registration Subject is Fire-and-Forget with No NATS JetStream Durability (Confirmed)

**File/Function:** `tap/pkg/lookup/almanac_server.go` — `Start()` line 38; `almanac_helpers.go` — `RegisterAlmanac()` line 29

**The Violation:**

```go
// almanac_server.go
_, err := r.nc.Subscribe("almanac.register", ...)

// almanac_helpers.go
err = nc.Publish("almanac.register", reqBytes)
```

Both the registration publisher (`Publish`) and the listener (`Subscribe`) use **core NATS** (not JetStream). This is at-most-once delivery. If the Almanac server is temporarily unavailable during a registration heartbeat — e.g., during a rolling restart — the registration message is **silently dropped**. The agent becomes invisible to the Orchestrator until its next heartbeat fires.

Critically, `defaults.yaml` *does* declare a JetStream `ALMANAC` stream covering `almanac.register` and `almanac.query`. But the code doesn't use it, negating the durability guarantee.

**The Fix:**

Use JetStream publish for registrations so no heartbeat is lost during server restarts:

```go
// almanac_helpers.go
func RegisterAlmanac(js nats.JetStreamContext, kp *identity.KeyPair, did, capability string) {
    // ...
    _, err = js.Publish("almanac.register", reqBytes) // ← JetStream, not nc.Publish
}

// almanac_server.go — consume via durable JetStream subscription:
_, err := js.Subscribe("almanac.register", func(msg *nats.Msg) {
    // ...
    msg.Ack()
}, nats.Durable("almanac-register-consumer"), nats.ManualAck())
```

---

### [AV-06] InstanceState: Opaque `Variables` Map Prevents Cross-Step Payload Contracts (Confirmed)

**File/Function:** `tap/workflows/orchestrator.go` — `InstanceState` struct, lines 63–67; `handleIncoming()` line 846

**The Violation:**

```go
type InstanceState struct {
    WorkflowDef   string                 `json:"workflow_def"`
    CurrentStepID string                 `json:"current_step_id"`
    Variables     map[string]interface{} `json:"variables"` // ← Opaque blob
}
```

The `Variables` field is a `map[string]interface{}` that is **never written to or read from** in the current step-advancement logic. When the Orchestrator dispatches the next step, it passes `env.Body` (the raw FIPA INFORM body from the previous step) directly as the payload:

```go
// handleIncoming() line 846
if err := o.dispatchStep(ctx, *nextStep, instanceIDStr, env.Body); err != nil {
```

This has two problems:
1. **No accumulation**: The output of step A cannot be merged with step C's inputs when step B runs in parallel. There's no shared state accumulation layer.
2. **No schema contract**: Any step can pass any opaque bytes to the next step. In a DAG with fan-in (multiple steps feeding one step), there is no mechanism to merge outputs before dispatch.

**The Fix:**

Promote `Variables` to the role of a **typed, versioned state accumulator** that steps read from and write to:

```go
type InstanceState struct {
    WorkflowDef      string                 `json:"workflow_def"`
    CurrentStepID    string                 `json:"current_step_id"`
    CompletedSteps   map[string]bool        `json:"completed_steps"`   // For fan-in tracking
    Variables        map[string]interface{} `json:"variables"`          // Accumulated outputs keyed by step ID
    LastProof        json.RawMessage        `json:"last_proof"`         // Last step's raw INFORM body
}
```

Steps write their proof output into `Variables[step.ID]`, and the Orchestrator merges all dependency outputs when all `DependsOn` steps are complete before dispatching the fan-in step.

---

## Architecture Matrix

| Violation | Severity | Component | Broken Principle |
|---|---|---|---|
| **AV-01** Static compile-time agent import in Daemon | ⚪ Trade-off | `daemon.go` | Deployment Model Choice |
| **AV-02** Orchestrator leaks task queue into agentconfig | 🟡 Medium (trade-off) | `daemon.go` | Single Responsibility / Decoupling |
| **AV-03** `WorkflowStep` has no DAG fields (linear only) | 🔴 Critical | `workflow_schema.go` + `orchestrator.go` | Infinite Composability |
| **AV-04** ConversationID is flat — no nested workflow stack | 🟠 High (when nesting is required) | `orchestrator.go` | Infinite Composability |
| **AV-05** Almanac registration uses core NATS, not JetStream | 🟠 High | `almanac_server.go` | Reliability / Durability |
| **AV-06** `InstanceState.Variables` unused; no fan-in merge | 🟡 Medium | `orchestrator.go` | Composability / State |

---

## Priority Execution Order

```
AV-03 (Schema DAG fields)
  └─ unblocks AV-04 (ConversationID nesting)
       └─ unblocks sub-workflow dispatch

AV-05 (JetStream Almanac)
  └─ standalone, fix independently

AV-06 (InstanceState accumulator)
  └─ implement alongside AV-03 DAG fields

AV-01/AV-02 (trade-offs)
  └─ optional refactors if/when moving to external or plugin-style agents
```

Start with **AV-03** — the schema is the foundation. Every other composability enhancement is blocked until `WorkflowStep` can express a graph.


---

**Plan: Decouple & Harden Orchestrator/Almanac**

**Summary**
- Enable DAG workflows with stateful orchestration, nested conversation IDs, and explicit task-queue source of truth.
- Harden discovery via JetStream registration + DLQ, and add DLQs for orchestrator triggers/inbox to prevent poison-message stalls.

**Key Changes**
- Workflow DAG & State (core)
  - Extend `tap/workflows/workflow_schema.go` with `depends_on`, `on_success`, `on_failure`, `sub_workflow`, schema version.
  - Migrate blueprints (e.g., `tap/workflows/csv_cleaner_pipeline.yaml`) to DAG fields; add migrator that infers linear `depends_on`.
  - Orchestrator state (`InstanceState`): add `completed_steps`, `variables`, `last_proof`.
  - Replace linear i+1 advancement with dependency-satisfied graph walk (fan-out/fan-in, success/failure branches, sub-workflow dispatch).
  - Build next payloads from merged `variables`; persist step outputs keyed by step ID.
  - ConversationID: `root[/child]/step`; update builders/parsers and sub-workflow resume logic.

- Task-Queue Source of Truth (trade-off resolution)
  - Make task queues explicit in agent config (`go/internal/config/defaults.yaml`); remove `GetTaskQueues` mutation from `daemon.go`.
  - Validate and warn on workflow vs config queue mismatches during transition.

- Almanac Durability + DLQ
  - Publish registrations via `js.Publish` with retry/backoff in `tap/pkg/lookup/almanac_helpers.go`.
  - Consume via durable, manual-ack JS subscription in `tap/pkg/lookup/almanac_server.go`; `MaxDeliver` + backoff; on terminal failure publish to `almanac.register.dlq`.
  - Add DLQ subject to ALMANAC stream (or small ALMANAC_DLQ stream) with error metadata (reason, attempts, timestamp, payload).

- Orchestrator DLQs
  - Add DLQ subjects `workflow.dlq.trigger` and `workflow.dlq.inbox` to WORKFLOWS stream (or dedicated WORKFLOWS_DLQ).
  - Trigger/inbox consumers: set `MaxDeliver` (e.g., 5) with backoff schedule; on terminal errors or exceeded deliveries, republish original message + error metadata to the appropriate DLQ, then `Term()`.
  - Keep success path and existing logging intact; add structured logs and counters for retries/DLQ emits.

- Docs & Ops
  - Update `docs/Composability.md` with new schema, ConversationID, task-queue policy, DAG behavior, and DLQ/JS requirements.
  - Add concise code comments where behavior is non-obvious.

**Tests**
- Unit: graph advancement (fan-out/fan-in), branch handling, sub-workflow dispatch, ConversationID parse/build, state accumulation, queue mismatch warning, Almanac retry/DLQ decision, orchestrator DLQ decision logic.
- Integration (NATS/JetStream harness): durable Almanac registration survives restart; poisoned registration lands in DLQ; trigger/inbox poison messages land in DLQ after retries; healthy messages process and ack; branched workflow completes with merged variables.
- Migration: legacy linear blueprint loads and produces equivalent execution order under DAG schema.

**Assumptions**
- Reusing existing streams for DLQ subjects is acceptable; if ops prefers isolation, create separate DLQ streams but keep subjects as named.
- Backoff example (1s, 5s, 30s, 2m, 5m) and `MaxDeliver=5` are acceptable defaults; adjust per SLOs.
- Agents can consume payloads built from merged per-step outputs (JSON) without additional schema enforcement for now.
