TAP Worker Generation Prompt

Copy the block below and fill in the `[PLACEHOLDERS]` before sending it to an LLM.

---

````
You are an expert Go developer. Your task is to generate a complete, compilable Go source file for a new internal Worker in the application.

Worker Specification

- Worker Name: "[WORKER_NAME]" (e.g. CSVMappingWorker)
- Package Name: `workers`
- Worker ID (used to derive inbox via `core.BuildWorkerInbox`): `"[WORKER_ID]"`
- Worker Inbox Subject (derived canonical NATS subject): `"worker.inbox.[WORKER_ID]"`
- NATS queue group: `"[QUEUE_GROUP]-group"`
- Durable consumer name: `"[WORKER_ID]-durable"`
- `activity_type` (must match workflow YAML): `"workers.[DOMAIN].[ACTIVITY]"` (e.g. `"workers.database.insert_rows"`)
- Purpose / Business Logic:
  [Describe what the worker does in plain English.]

---

Framework Contracts You MUST Follow

12. Architecture Boundaries (Workers vs Agents)

- **CRITICAL RULE**: Workers DO NOT make LLM calls. They consume NATS `ACCEPT_PROPOSAL` envelopes dispatched by the **Workflow Orchestrator**, execute DB (`pgx`) updates to persist the proven state, and return. They are PASSIVE — the Orchestrator advances the pipeline based on the worker's success/failure.
- Workers do NOT publish results to other topics to trigger the next step. The Orchestrator drives all state transitions.
- If you need to make LLM calls or perform AI intelligence, that is the job of an **Agent**.

3. Worker Interface

Every worker MUST implement the `Worker` interface. The `Subscriptions()` method must use the canonical `worker.inbox.[WORKER_ID]` subject:

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

func (w *[WORKER_NAME]) Subscriptions() []SubscriptionConfig {
    return []SubscriptionConfig{
        {
            Subject: "worker.inbox.[WORKER_ID]",
            Group:   "[WORKER_ID]-group",
            Options: []nats.SubOpt{
                nats.Durable("[WORKER_ID]-durable"),
                nats.DeliverAll(),
                nats.AckExplicit(),
            },
        },
    }
}
```

3. Registration Boilerplate

Each worker must self-register in its `init()` function:

```go
func init() {
    RegisterFactory(func(deps Dependencies) (Worker, error) {
        return New[WORKER_NAME](deps.Store.Queries, deps.Queue, deps.Logger)
    })
}
```

4. Handling Orchestrator-Dispatched Envelopes

The Orchestrator sends an `ACCEPT_PROPOSAL` envelope containing the `core.Proof` payload. In workflow YAML, worker steps usually use `task_queue: [WORKER_ID]`, and orchestrator resolves it to `worker.inbox.[WORKER_ID]` using `core.BuildWorkerInbox`. Your `Handle` must:
1. Parse the TAP envelope.
2. Validate `env.Performative` with `core.IsValidPerformative`.
3. Parse the dispatched `TaskDefinition` and retain `complexity/reward/currency/expires_at` for audit/payment readiness.
4. Execute DB mutations.
5. Return `nil` on success (Manager will `Ack()`) or `error` for transient failures (Manager will `Nak()`).

```go
func (w *[WORKER_NAME]) Handle(ctx context.Context, msg *nats.Msg) error {
    var env core.Envelope
    if err := json.Unmarshal(msg.Data, &env); err != nil {
        return nil // malformed — term silently
    }
    if !core.IsValidPerformative(env.Performative) {
        return nil // invalid protocol verb
    }
    if env.Performative != core.ACCEPT_PROPOSAL {
        return nil // not for us
    }

    var task core.TaskDefinition
    if err := json.Unmarshal(env.Body, &task); err != nil {
        return nil
    }

    // Optional: log/payment trace fields
    _ = task.Complexity
    _ = task.Reward
    _ = task.Currency
    _ = task.ExpiresAt

    // Process task.Payload (often a proof) and write to PostgreSQL

    return nil
}
```

What to Return

Return only the Go source file for the new worker. Do not return anything else.
````
