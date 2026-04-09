# Toro Worker Dispatcher Architecture

This package (`internal/workers`) manages the background processes and NATS JetStream event consumers for the Toro platform.

We use an **Event-Driven Registry & Dispatcher Pattern**. This means:
- We do not run unbounded, statically-blocking `for` loops inside goroutines (`<-ctx.Done()`) for every background task.
- Instead, the `Manager` handles NATS connections, context cancellation, and subscription lifecycle centrally.
- Workers are defined functionally and invoke a `.Handle()` method purely when a JetStream payload arrives.

## Registration & Dispatch Lifecycle

The worker system follows a structured lifecycle managed by the central `Manager`:

### 1. Registration Phase
Workers are initialized as standard Go structs and added to the `Manager` via the `.Register(Worker)` method. At this stage, workers are "dormant" and stored in an internal registry slice.

### 2. Dispatch (Startup) Phase
When `Manager.StartAll(ctx)` is invoked:
- **Initialization**: Each worker's optional `.Init(ctx)` method is executed sequentially.
- **Subscription binding**: The manager iterates through each worker's `.Subscriptions()` and creates NATS **Queue Subscriptions**. Using Queue Groups ensures that even if multiple instances of Toro are running, a single message is only handled by one worker instance (Load Balancing).

### 3. Execution (The Middleware Layer)
The `Manager` acts as a protective middleware for every incoming message before passing it to the worker:
- **Panic Recovery**: Every `Handle()` call is wrapped in a `recover()` block. If a worker panics, the manager logs the stack trace and calls `msg.Nak()` to allow for a retry.
- **Auto ACK/NAK**: 
    - If `Handle()` returns `nil`, the manager calls `msg.Ack()`.
    - If `Handle()` returns an `error`, the manager calls `msg.Nak()`.

### 4. Shutdown Phase
The `Manager` respects context cancellation. When the root context finishes, it calls `.Unsubscribe()` on all active NATS consumers to ensure a graceful exit without dropping in-flight messages.

## Worker vs. Agent Distinction

In the Toro architecture, we distinguish between **TAP Agents** and **Workers**:
- **TAP Agents**: Use the Agent Scaffolding (`tap/pkg/agent`), are usually stateful or LLM-driven, and communicate via `INFORM` messages and `Proofs`.
- **Workers**: Are purely internal Go implementations that consume `Proofs` or system events to perform side effects in the database, sync with external APIs, or trigger further internal workflows.

## Existing Workers

| Worker | Responsibilities | Subject(s) |
| :--- | :--- | :--- |
| **CSVMappingWorker** | Consumes AI-mapped CSV columns, inserts cleanup rows into DB. | `proof.accounting.cleanup.columns` |
| **EnrichmentWorker** | Normalizes vendors/customers and predicts COA accounts for rows. | `proof.accounting.cleanup.inserted` |
| **AttachableWorker** | Handles file attachments and receipt syncing. | `ledger.shadow_erp_attachables.insert` |
| **TransactionWorker**| Processes and stores proposed ledger transactions. | `ledger.shadow_erp_proposed_transactions.*` |
| **FignodePublisher** | Syncs high-velocity accounting data to the Fignode layer. | `proof.accounting.cleanup.reconcile.>` |
| **VectorWorker**     | Manages embeddings and vector database synchronization. | `events.vector.>` (dynamic) |

## Implementing a New Worker

To create a new background worker, implement the `Worker` interface:

```go
package workers

import (
    "context"
    "github.com/nats-io/nats.go"
)

type MyNewWorker struct {
    // dependencies e.g., db *database.Queries, logger *slog.Logger
}

// 1. Initialization (Optional)
// Executed once when the Manager starts up the registry.
// Useful for spawning bounded background flusher routines or loading specific cache states.
func (w *MyNewWorker) Init(ctx context.Context) error {
    return nil // No background loop needed
}

// 2. Define Subscriptions
func (w *MyNewWorker) Subscriptions() []SubscriptionConfig {
    return []SubscriptionConfig{
        {
            Subject: "proof.accounting.example.>",
            Group:   "example-worker-group", 
            Options: []nats.SubOpt{
                nats.Durable("example-worker-durable"),
                nats.DeliverAll(),
                nats.AckExplicit(),
            },
        },
    }
}

// 3. Handle incoming events
// Only invoked when an event arrives!
func (w *MyNewWorker) Handle(ctx context.Context, msg *nats.Msg) error {
    // 1. Unmarshal Payload (usually a TAP Envelope or JSON proof)
    // 2. Execute logic (DB updates, API calls)
    // 3. Acknowledge the message
    
    msg.Ack()
    return nil
}
```

## Registering and Starting a Worker

Workers use an automated **Registry Pattern** via `init()` functions, similar to Agents. You do not need to manually register your worker in `main.go`.

### 1. Self-Register Using `init()`
At the bottom of your worker file, call `RegisterFactory`:

```go
func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		// Only extract the dependencies you need from the Dependencies container
		return NewMyNewWorker(deps.Store.Queries, deps.Queue, deps.Logger)
	})
}
```

### 2. Loading the Registry
All workers are centrally loaded in the application entrypoint (`cmd/protocol/main.go`). The central `Dependencies` struct is hydrated once, and all registered factories are invoked.

```go
import "github.com/Yankzy/usetoro/internal/workers"

// 1. Initialize the Dispatcher Manager
workerManager := workers.NewManager(logger, natsConn)

// 2. Hydrate centralized Dependencies
workerDeps := workers.Dependencies{
    Logger: logger,
    Queue:  natsConn,
    Store:  st,
    // ... other injected services
}

// 3. Load all workers that registered themselves via init()
if err := workerManager.LoadFromRegistry(workerDeps); err != nil {
    logger.Error("failed to load background workers from registry", "error", err)
}

// 4. Start the Manager (This is blocking and captures context cancellation!)
if err := workerManager.StartAll(ctx); err != nil {
    logger.Error("Dispatcher failed", "err", err)
}
```

## Message Acknowledgment (ACK / NAK / TERM)

If your `SubscriptionConfig` uses `nats.AckExplicit()`, it is the **Worker's responsibility** to call:
- `msg.Ack()` for successful completion.
- `msg.Nak()` for transient errors (JetStream will retry).
- `msg.Term()` for poison pills (JSON syntax errors, unrecoverable invalid payloads) so JetStream stops redelivering.

> [!IMPORTANT]
> Always check `msg.Metadata().NumDelivered` to detect and terminate poison pills after a few attempts, preventing infinite retry loops.
