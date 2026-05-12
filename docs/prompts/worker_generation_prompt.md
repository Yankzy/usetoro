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
- If orchestrator-driven, expected performative(s): `request` ONLY.
- Subscription source config key(s):
  [No longer needed, workers use `cfg.Workers.GetForWorker(w)` directly]
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
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("[WORKER_TYPE_NAME]: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("[WORKER_TYPE_NAME]: failed to derive inbox", "activity_type", activityType, "error", err)
			return nil
		}
	}

	group := workerCfg.Group
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

`Worker` controls ACK/NAK behavior directly due to explicit acks (`nats.AckExplicit()`):
- MUST call `msg.Ack()` and return `nil` on success.
- MUST call `msg.Nak()` and return `error` for transient/retryable failures.
- MUST call `msg.Term()` and return `nil` for ignorable/malformed messages (wrong type, malformed json).
- if poison pill after repeated delivery, call `msg.Term()` and return `nil`.

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

When subject carries TAP envelopes or raw bodies:
- Unmarshal to `core.Envelope` optionally
- Use `core.UnmarshalTaskPayload(data, &payload)` which handles both raw payloads and `TaskDefinition` or `Proof` bodies.

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
    _, workerCfg := w.cfg.Workers.GetForWorker(w)
    activityType := workerCfg.ActivityType
    subject := workerCfg.Subject
    if subject == "" {
        subject, _ = core.BuildWorkerInboxFromActivity(activityType)
    }
    group := workerCfg.Group
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
    // accept ONLY REQUEST performative using core.UnmarshalTaskPayload
    // write DB side effects
    // if transient error:
    //     msg.Nak()
    //     return err
    //
    // msg.Ack()
    return nil
}
```

Critical behaviors to copy from this one-shot:
- Strictly handle ONLY the `REQUEST` performative.
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
```
