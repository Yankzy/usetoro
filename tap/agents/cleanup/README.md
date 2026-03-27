# Accounting Cleanup Agent

The Cleanup Agent processes raw accounting data. It integrates JetStream queuing and the Redux Engine to structure CSV transaction records via OpenAI inference models.

## 1. Execution & Initialization Lifecycle

The Cleanup Agent operates as an internal infrastructure agent managed by `ProtocolDaemon` (`tap/pkg/daemon/daemon.go`). 

### Initialization Flow:

1. **Instantiation:** The daemon invokes `cleanup.NewCleanupAgent` to build the `AgentConfig`. 
2. **NATS Subscription Binding:** Registered via `supervisor.RegisterInternalAgent()` using the `did:toro:agent:cleanup_1` identifier. 
3. **JetStream Polling:** Takes tasks from `tasks.accounting.cleanup.>` using the `cleanup-group` durable queue.

## 2. Redux Engine Integration

The Agent records state mutations using JSON Patch arrays via the Redux Engine.

- **Payload Extraction:** Parses `[]RawRow` items from the incoming envelope.
- **LLM Mapping:** Exposes rows to the LLM via `runtime.Exec` targeting the model specified in `defaults.yaml`.
- **JSON Patch Generation:** The `llmCallback` converts the OpenAI mapping output into a `map[string]Object`. 
- **Array Limitation:** Outputs data as dictionary keys to comply with `EnforceNoArrayMiddleware`. Dispatches RFC 6902 `add` structures into the local Redux store.

## 3. Event Propagation

Once the Redux Engine commits the JSON map, the Agent uses its `onComplete` JetStream exit hook to publish the finalized payload to `proof.accounting.cleanup.columns`.

This topic wakes the `cleanup_worker` (running inside the `sync` container) to insert the structured array directly into the PostgreSQL `fignode.staging_transactions` table. This insertion subsequently triggers the Enrichment Worker pipeline.
