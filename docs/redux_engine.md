# Toro OS: The Redux Engine (RFC 6902)
## Enterprise State Compilation

The `tap/pkg/redux` package provides deterministic state mutations based on Redux paradigms. Instead of executing direct database queries, Agents emit **RFC 6902 JSON Patches**. The Engine applies these operations to an in-memory JSON state tree.

### I/O Decoupling
* **Stateless Execution:** Evaluates raw bytes in memory. Postgres queries are deferred upstream to the Host APIs.
* **Agnostic Observability:** Executes `noopMetrics` by default. Parent applications inject Prometheus tracking via `EngineConfig`.

---

## Technical Defenses & Enterprise Constructs

Because Large Language Models are probabilistic, the Redux Engine must trap inconsistencies before they contaminate the Toro ledger.

### 1. Ephemeral Context (The Circuit Breaker)
* **The Concept:** Traditional AI pipelines log LLM failures (e.g., "bad path") into the persistent DB. If an AI reads that error, panics, and generates another failed patch, it triggers an infinite retry loop that wastes API tokens.
* **The Defense:** The `Store` ejects illegal patches from memory instantly. It returns a typed `[]DomainFault` array to the API Gateway instead of writing to the JSON state map. The Gateway feeds this fault back to the LLM and tracks consecutive failures via a Redis Circuit Breaker, suspending the agent if necessary.

### 2. The Idempotency Match (Monotonic Sequences)
* **The Concept:** NATS JetStream guarantees "At-Least-Once" delivery. A distributed partition might force a NATS consumer to replay the same event twice.
* **The Defense:** `SequenceID` tracking. The API Gateway supplies an `expectedSequence` uint64 to `Reduce()`. If `event.SequenceID != expectedSequence`, the Engine registers an `ErrSequenceMismatch` and skips the patch, ensuring perfect transactional ordering.

### 3. Optimistic Concurrency (The "Test" Lock)
* **The Concept:** A human CPA and an AI Agent attempt to edit the same invoice status at precisely the same nanosecond.
* **The Defense:** The LLM is forced to begin its patches with `{"op": "test", "path": "/status", "value": "AWAITING"}`. If the CPA changes the DB record to `APPROVED` moments earlier, the RFC 6902 test fails. The Engine rejects the entire LLM atomic transaction, preventing race conditions.

### 4. Array Index Bounds (`EnforceNoArrayMiddleware`)
* **The Concept:** Operating on JSON arrays (e.g., `{"op": "remove", "path": "/receipts/1"}`) causes reference corruption if concurrent LLMs or humans alter array length.
* **The Defense:** The `EnforceNoArrayMiddleware` halts the pipeline if JSON arrays are returned. It forces schemas to use dictionaries (`map[string]Object`) mapped to deterministic UUID keys.

### 5. Idempotent Adds vs. Replaces
* **The Concept:** Executing `{"op": "replace", "path": "/status"}` fails structurally if the `/status` key has not been explicitly initialized in the base schema earlier in the session.
* **The Defense:** Agents structurally enforce `{"op": "add"}` over `replace`. In RFC 6902, `add` acts as an upsert, safely overwriting existing values or instantiating new keys.

### 5. Path-Based Security (The Trust Problem)
* **The Concept:** An AI agent attempts to maliciously rewrite the routing topology (e.g., editing direct deposit fields).
* **The Defense:** `EnforceRBACMiddleware` evaluates exact path strings against configuration files. An `actor: AI_AGENT` is restricted to target matrices like `/receipts/` or `/status`. If it attempts to access out-of-bounds fields, it receives a `403 ErrRBACViolation`.

### 6. Byte Limits (OOM Memory Safeties)
* **The Concept:** A corrupted agent submits a 50 Megabyte base64 string to over-fill Go routine RAM limits.
* **The Defense:** `EnforcePayloadBoundariesMiddleware` evaluates the byte slice lengths against the `MaxPayloadBytes` capacity. It halts the pipeline before deep structural parsing begins, preventing Docker Out-Of-Memory panics.

### 7. Schema Drift Avoidance
* **The Concept:** Generative agents invent random W3C mappings or generate unstructured keys, breaking React UI clients.
* **The Defense:** The Engine passes the new state through a pre-compiled JSON schema mapping. Any undocumented structural drift results in an immediate rejection, guaranteeing UI consistency.

### 9. State Compaction & Observability (WebSockets)
* **The Concept:** High-frequency patching produces huge I/O overhead. Syncing every patch to PostgreSQL synchronously creates fatal lock exhaustion when agents run concurrently.
* **The Defense:** The Engine streams the `[RFC6902Event]` entirely to JetStream (`workflow.trace.>`).
* **WebSockets:** The API Gateway subscribes to the `workflow.trace` channel and streams the raw JSON Patches directly down to active Client WebSockets to hydrate the React UI organically without database polling.
* **The Compaction Daemon:** A Rollup Worker (`tap/pkg/redux/rollup.go`) batches the NATS streams asynchronously, condensing payloads into single bulk `toro_core.workflows` database inserts. 

---

## Agent Usage Guide

To build a Toro OS Agent that accesses the JetStream Batch Rollups & Tracing pipeline, you simply rely on the `BaseAgent` wrapper:

### 1. Execute the Global Workflow Wrapper
Instead of attempting strictly customized LLM invocations and executing bare database writes, wrap your logic inside `ExecuteGlobalWorkflow()`. 

The wrapper abstracts all Database fetching, securely instantiates the Redux loop, publishes explicit limits to JetStream, executes strict LLM error callbacks preventing payload panics, and executes Fignode database logic downstream securely:

```go
func (a *MyAgent) process(ctx context.Context, sessionID pgtype.UUID) error {
	schemaString := `{ "type": "object", "properties": { "status": { "type": "string" } } }`
	rbacRules := redux.RBACPolicy{
		AllowedPrefixes: map[string][]string{
			"my_agent": {"/status"},
		},
	}

	// Phase 1: AI Feedback Loop
	llmCallback := func(previousErrors []redux.DomainFault, currentSeq uint64) ([]redux.RFC6902Event, error) {
		patchBytes := a.promptLLM(previousErrors)

		event := redux.RFC6902Event{
			SequenceID: currentSeq,
			Actor:      "my_agent",
			PatchArray: []json.RawMessage{patchBytes},
		}
		return []redux.RFC6902Event{event}, nil
	}

	// Phase 2: Database Handoff
	onComplete := func(nextState []byte) error {
		return a.customFignodeDBInsert(ctx, nextState)
	}

	return a.ExecuteGlobalWorkflow(
		ctx,
		a.db,
		sessionID,
		entityID,
		schemaString,
		rbacRules,
		nil,
		llmCallback,
		onComplete,
	)
}
```
