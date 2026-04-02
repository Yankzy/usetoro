# Toro Worker Dispatcher Architecture

This package (`internal/workers`) manages the background processes and NATS JetStream event consumers for the Toro platform.

We use an **Event-Driven Registry & Dispatcher Pattern**. This means:
- We do not run unbounded, statically-blocking `for` loops inside goroutines (`<-ctx.Done()`) for every background task.
- Instead, the `Manager` handles NATS connections, context cancellation, and subscription lifecycle centrally.
- Workers are defined functionally and invoke a `.Handle()` method purely when a JetStream payload arrives.

## Implementing a New Worker

To create a new background worker, just implement the `Worker` interface:

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
// Return a list of SubscriptionConfigs that tell the Dispatcher which exact 
// JetStream subjects this worker desires, which consumer group to use, 
// and what nats.SubOpts (like ManualAck, DeliverNew, etc.) to set.
func (w *MyNewWorker) Subscriptions() []SubscriptionConfig {
    return []SubscriptionConfig{
        {
            Subject: "test.accounting.events.>",
            Group:   "my-new-worker-group", // Used for NATS Queue Subscriptions (Consumer durability)
            Options: []nats.SubOpt{
                nats.ManualAck(), 
                nats.BindStream("TEST_STREAM"), // Ensure the stream exists!
            },
        },
    }
}

// 3. Handle incoming events
// Only invoked when an event arrives!
func (w *MyNewWorker) Handle(ctx context.Context, msg *nats.Msg) error {
    // 1. Process Payload
    // 2. Handle Errors (Returning an error triggers a NAK automatically depending on Manager config, or you can manually msg.Nak())
    // 3. Ack the message if successful: msg.Ack() 
    //    *(Note: Using nats.AckExplicit or nats.ManualAck means you MUST ack!)*
    
    msg.Ack()
    return nil
}
```

## Registering and Starting a Worker

All workers are centrally registered and started in the application entrypoint (usually `cmd/sync/main.go`).

```go
import "github.com/Yankzy/usetoro/internal/workers"

// ... somewhere in main.go ...

// 1. Initialize the Dispatcher Manager
workerManager := workers.NewManager(logger, natsConn)

// 2. Initialize your worker instance (inject DB, config, etc.)
myWorker := &workers.MyNewWorker{ /* ... */ }

// 3. Register it
workerManager.Register(myWorker)

// 4. Start the Manager (This is blocking and captures context cancellation!)
if err := workerManager.StartAll(ctx); err != nil {
    logger.Error("Dispatcher failed", "err", err)
}
```

## Background Setup Constraints
If your worker truly needs a background ticking goroutine (e.g. batch-flushing embeddings like Pinecone `VectorSyncWorker` does), you should:
1. Kick off the routine inside your `Init(ctx)` method.
2. Ensure that your goroutine correctly respects `<-ctx.Done()` so that when the Manager shuts down, it cleans up without leaking. 
3. *Do not* block inside `Init(ctx)`. `Init` must return synchronously.

## Message Acknowledgment (ACK / NAK / TERM)
If your `SubscriptionConfig` uses `nats.ManualAck()` or `nats.AckExplicit()`, it is the **Worker's responsibility** to call:
- `msg.Ack()` for successful completion.
- `msg.Nak()` for transient errors (JetStream will retry).
- `msg.Term()` for poison pills (JSON syntax errors, invalid unrecoverable payloads) so JetStream stops redelivering forever.

*The Manager currently logs errors if your `Handle` returns `err != nil`, and runs a fallback `msg.Nak()`, but manual precision inside `Handle` is highly recommended.*
