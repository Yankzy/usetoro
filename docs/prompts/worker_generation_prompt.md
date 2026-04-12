TAP Worker Generation Prompt

Copy the block below and fill in the `[PLACEHOLDERS]` before sending it to an LLM.

---

````
You are an expert Go developer. Generate a complete, compilable Go worker file under `go/internal/workers/`.

Worker Specification

- Worker Type Name: `[WORKER_TYPE_NAME]` (e.g. `CSVMappingWorker`)
- File Name: `[FILE_NAME].go`
- Purpose / Business Logic:
  [Describe what this worker does and what side effects it owns.]
- Primary input source:
  `[ORCHESTRATOR_ENVELOPE | PROOF_EVENT | CDC_EVENT | CUSTOM_JSON]`
- If orchestrator-driven, expected performative(s): `[accept-proposal|inform|both]`
- Subscription source config key(s):
  [e.g. `cfg.Workers.CSVMapping`, `cfg.Workers.CSVMappingActivityType`, `cfg.Workers.CSVMappingGroup`]
- Dependencies needed from `workers.Dependencies`:
  [List only what is required: `Store.Queries`, `Queue`, `Logger`, `Config`, `EntityResolver`, `LLMClient`, etc.]

Framework Contracts You MUST Follow

1. Implement the worker interface exactly

```go
type SubscriptionConfig struct {
    Subject string
    Group   string
    Options []nats.SubOpt
}

type Worker interface {
    Init(ctx context.Context) error
    Subscriptions() []SubscriptionConfig
    Handle(ctx context.Context, msg *nats.Msg) error
}
```

2. Register with `RegisterFactory` in `init()`

```go
func init() {
    RegisterFactory(func(deps Dependencies) (Worker, error) {
        return New[WORKER_TYPE_NAME](/* required deps only */)
    })
}
```

3. Subscription pattern must match current manager conventions

- Use config-driven subject(s).
- If subject is empty and activity-type derivation is appropriate, use `core.BuildWorkerInboxFromActivity(activityType)`.
- Derive defaults with existing helpers:
  - `groupFromSubject(subject)`
  - `durableFromSubject(subject)`

Canonical `Subscriptions()` pattern:

```go
func (w *[WORKER_TYPE_NAME]) Subscriptions() []SubscriptionConfig {
    subject := w.cfg.Workers.[SUBJECT_KEY]
    if subject == "" {
        // optional derivation path when your worker supports it
    }

    group := w.cfg.Workers.[GROUP_KEY]
    if group == "" {
        group = groupFromSubject(subject)
    }

    return []SubscriptionConfig{
        {
            Subject: subject,
            Group:   group,
            Options: []nats.SubOpt{
                nats.Durable(durableFromSubject(subject)),
                nats.DeliverAll(),
                nats.AckExplicit(),
            },
        },
    }
}
```

4. Handle semantics (critical)

`Manager` controls ACK/NAK behavior:
- `Handle(...) == nil` => manager `Ack()`
- `Handle(...) != nil` => manager `Nak()`

Therefore:
- return `nil` for ignorable messages (wrong type, malformed non-retryable, unsupported performative)
- return `error` only for transient/retryable failures
- if poison pill after repeated delivery, call `msg.Term()` and return `nil`

Poison-pill guard pattern:

```go
meta, metaErr := msg.Metadata()
if metaErr == nil && meta.NumDelivered > 3 {
    w.logger.Error("poison pill exceeded retries", "subject", msg.Subject)
    msg.Term()
    return nil
}
```

5. Envelope handling for orchestrator-driven workers

When subject carries TAP envelopes:
- unmarshal `core.Envelope`
- validate `core.IsValidPerformative(env.Performative)`
- parse `env.Body` as either:
  - `core.TaskDefinition` (common for `ACCEPT_PROPOSAL` dispatch)
  - `core.Proof` (common for `INFORM` chains)

`TaskDefinition` shape:

```go
type TaskDefinition struct {
    ID         string
    Domain     string
    Complexity core.TaskComplexity
    Reward     int64
    Currency   string
    Payload    json.RawMessage
    ExpiresAt  int64
}
```

Notes:
- `Payload` often contains proof-like data from prior step.
- Reward/currency fields may be zero/empty depending on orchestrator phase.

6. Completion signaling (when this worker is a workflow step)

If this worker is expected to advance orchestrator flow, publish an `INFORM` envelope to `workflows.OrchestratorInbox` and preserve the incoming `cid`.

Typical reply envelope:
- `src`: worker DID-like identifier string
- `dst`: `workflows.OrchestratorDID`
- `perf`: `inform`
- `cid`: incoming conversation id
- `body`: a compact proof payload

7. One-shot Learning Example (from `go/internal/workers/csv_mapping_worker.go`)

Use this as a style anchor. Keep the same resilient parsing/fallback posture.

```go
type CSVMappingWorker struct {
    db     *database.Queries
    nc     *nats.Conn
    logger *slog.Logger
    cfg    *config.Config
}

func (w *CSVMappingWorker) Subscriptions() []SubscriptionConfig {
    activityType := w.cfg.Workers.CSVMappingActivityType
    subject := w.cfg.Workers.CSVMapping
    if subject == "" {
        subject, _ = core.BuildWorkerInboxFromActivity(activityType)
    }
    group := w.cfg.Workers.CSVMappingGroup
    if group == "" {
        group = groupFromSubject(subject)
    }
    return []SubscriptionConfig{{
        Subject: subject,
        Group:   group,
        Options: []nats.SubOpt{nats.Durable(durableFromSubject(subject)), nats.DeliverAll(), nats.AckExplicit()},
    }}
}

func (w *CSVMappingWorker) Handle(ctx context.Context, msg *nats.Msg) error {
    // parse envelope-like payload
    // accept both INFORM and ACCEPT_PROPOSAL paths
    // parse TaskDefinition.Payload first, then fallback to direct Proof body
    // write DB side effects
    // if cid exists, publish INFORM back to workflows.OrchestratorInbox
    // otherwise optional legacy broadcast path
    return nil
}
```

Critical behaviors to copy from this one-shot:
- Robustly handle envelope variants (`INFORM` proof vs `ACCEPT_PROPOSAL` task payload wrapping proof-like data).
- Use `ExtractRows(...)` style normalization when payload can be map-or-array.
- Keep poison-pill termination local (`msg.Term()` + return `nil`).
- Return transient DB/API failures as `error` so manager can `Nak()`.

8. Architecture boundaries

- Workers own deterministic side effects (DB writes, sync calls, enrichment pipelines, fanout).
- Workers may use injected AI helpers (`LLMClient`, `EntityResolver`, `CoAMapper`) when required by business logic.
- Do not use TAP `agent.Runtime` inside workers.

Quality Bar

- Must compile and match existing `go/internal/workers` style.
- No placeholder tokens left in code.
- No pseudocode or TODO stubs.
- Use only dependencies listed in the factory constructor.

What to Return

Return only the Go source file content for the worker.
Return nothing else.
````

Placeholder Values

```env
WORKER_TYPE_NAME=""
FILE_NAME=""
SUBJECT_KEY=""
GROUP_KEY=""
```
