# **Technical PRD: Toro OS - RFC 6902 State Engine (Algorithm 1)**

### **1. Objective**
To define the deterministic, in-memory state compilation mechanism within the Go Kernel. The Engine uses standard RFC 6902 JSON Patch specifications to mutate state. The LLM evaluates the workflow and emits an array of standard JSON Patch operations. The Go Kernel acts as a pure reducer, applying these patches sequentially to a base state to output a mathematically perfect `Current State Object`.

### **2. Core Infrastructure & Libraries**
* **The Message Broker:** NATS JetStream (Stores the sequence of JSON Patch events).
* **The Orchestrator:** Go Kernel.
* **The Mutation Library:** A standard Go implementation of RFC 6902 (e.g., `github.com/evanphx/json-patch` or `github.com/wI2L/jsondiff`).
* **The CPU:** The LLM, strictly instructed to output RFC 6902 compliant arrays.

### **3. Data Models & Schemas**

**A. The Current State Object (The Target Document)**
This is the arbitrary JSON document that represents the workflow. The Go Kernel doesn't need to strictly map every field; it just treats it as a generic JSON tree.
```json
{
  "workflow_id": "wf_12345",
  "status": "DISPUTE_OPEN",
  "variables": {
    "fuel_surcharge": 40.00,
    "invoice_id": "inv_001"
  },
  "blockers": ["Awaiting trucker receipt"]
}
```

**B. The LLM Output (The RFC 6902 Patch)**
When the agent wakes up and decides to change the state (e.g., the trucker uploaded the receipt and negotiated the surcharge to $50), the LLM outputs a standard JSON Patch array. 
```json
[
  { "op": "replace", "path": "/variables/fuel_surcharge", "value": 50.00 },
  { "op": "remove", "path": "/blockers/0" },
  { "op": "replace", "path": "/status", "value": "READY_FOR_PAYMENT" }
]
```

**C. The NATS Event Wrapper**
The Go Kernel wraps the LLM's patch array in standard metadata before committing it to the immutable ledger.
```json
{
  "event_id": "evt_0046",
  "timestamp": "2026-03-22T14:30:00Z",
  "type": "RFC_6902_PATCH",
  "actor": "AI_AGENT",
  "patch_array": [
     { "op": "replace", "path": "/variables/fuel_surcharge", "value": 50.00 },
     { "op": "remove", "path": "/blockers/0" },
     { "op": "replace", "path": "/status", "value": "READY_FOR_PAYMENT" }
  ]
}
```

### **4. The Reducer Pipeline (The Execution Flow)**

**Phase 1: State Initialization**
The Go Kernel wakes up and loads the latest `Current State Object` (either from a Postgres snapshot or an empty `{}` initialization).

**Phase 2: The Fetch & Iteration**
The Go Kernel pulls the unapplied delta events from NATS JetStream in strict chronological order.

**Phase 3: The Library Application (The "Pure" Reduce)**
Instead of a massive `switch` statement, the Go Kernel uses a highly optimized `for` loop. For every event in the delta:
1. Go extracts the `patch_array`.
2. Go passes the `Current State Object` (as bytes) and the `patch_array` (as bytes) directly into the `jsonpatch.Apply()` function.
3. The library securely and deterministically applies the `add`, `remove`, `replace`, or `copy` operations.
4. The result becomes the new `Current State Object`.

**Phase 4: Output & Prompt Injection**
Once all delta events are applied, the Go Kernel injects this newly compiled JSON document into the LLM's prompt, along with the new trigger, instructing it to emit the *next* RFC 6902 patch.

### **5. Edge Cases & Safety Mechanisms**

**A. The "Test" Operator (Optimistic Concurrency)**
* **Risk:** The LLM hallucinates a state and tries to modify a field that doesn't exist, or has been changed by a human operator a millisecond prior.
* **Defense:** The LLM can be prompted to utilize the RFC 6902 `"op": "test"` command at the top of its patch array. 
  * `{"op": "test", "path": "/status", "value": "DISPUTE_OPEN"}`
  * If the standard library evaluates the `test` operation and it fails, the entire patch array is atomically rejected. The Go Kernel simply passes the error back to the LLM: *"Patch failed due to state mismatch. Re-evaluate current state."*

**B. Strict Schema Boundaries**
* **Risk:** The LLM uses RFC 6902 to delete the `workflow_id` or other critical system routing keys.
* **Defense:** The Go Kernel partitions the JSON. The `workflow_id`, `metadata`, and `permissions` are kept in a protected Go struct envelope. The JSON Patch library is *only* allowed to execute against the isolated `state_payload` sub-tree.

***

## **The Redux Engine (RFC 6902)**

### **1. Objective**
To define the isolated, deterministic `reduce()` loop within the Go Kernel. The Redux Engine is a pure function that takes an initial JSON state and an ordered array of NATS events (containing RFC 6902 JSON Patches), applying them sequentially to compile the up-to-the-millisecond `Current State Object`.

### **2. The State Container Architecture (The Envelope)**
To prevent the LLM from accidentally (or maliciously) using a JSON patch to delete the `workflow_id` or other backend routing logic, the Redux Engine strictly partitions the state object in memory into two nodes. 

* **Node A: The System Envelope (`_sys`)** * Contains `workflow_id`, `last_processed_event_id`, and `created_at`.
  * **Rule:** The Redux Engine hard-blocks any JSON patch attempting to mutate the `/_sys` path.
* **Node B: The Mutable Payload (`data`)**
  * Contains the actual business logic (`status`, `variables`, `blockers`).
  * **Rule:** This is the *only* target exposed to the RFC 6902 `jsonpatch.Apply()` function.

### **3. The Pure Function Boundary (Postgres & Telemetry Decoupling)**
Following strict Redux Toolkit principles, the Go Kernel Redux Engine is engineered as a **Pure Function**. It intentionally decouples external I/O integrations to preserve absolute zero-dependency determinism:

**A. Database Decoupling (Postgres / NATS):**
The Engine contains zero database adapter layers (`pgxpool.Pool`). The Parent Host Worker controls the DB Integration Loop:
1. **Fetch:** The Host Worker executes `SELECT state FROM toro_core.workflows WHERE id = $1` to pull the raw Postgres snapshot.
2. **Inject:** The Host Worker explicitly feeds these raw snapshot bytes directly into the pure engine: `store.Reduce(ctx, baseStateBytes, events)`.
3. **Commit:** The Engine directly returns the final compiled `[]byte` context, allowing the Host Worker to safely execute `UPDATE toro_core.workflows SET state = $2`.

**B. Telemetry Decoupling (Prometheus / Grafana):**
The Engine completely eschews hardcoded observability dependencies. Instead, it exposes an agnostic `MetricsRecorder` interface. 
During testing or isolated deployments, the engine silently defaults to an internal `noopMetrics` struct ensuring zero overhead. In production, the Parent Application binds a dedicated tracking object into the `EngineConfig` upon initialization, seamlessly empowering the engine to push SLA speed metrics (`RecordPatchLatency`) and anomaly volumes (`RecordRuleViolation`) directly to generic upstream receivers.

---

### **4. The Redux Toolkit Architecture (The Pipeline)**

Instead of a single procedural script, the Redux Engine strictly adheres to the enterprise **Redux Toolkit Architecture** via discrete, decoupled Go artifacts:

* **`store.go` (The Orchestrator):** Manages the core `Reduce()` loop, deeply clones state matrices to guarantee atomic rollbacks, and handles Go contexts.
* **`reducer.go` (The Core Mutator):** A mathematically pure function executing `jsonpatch.Apply()`. It is strictly isolated from routing logic and safely returns new nested slices without polluting original memory.
* **`middleware.go` (The Interceptors):** Custom hooks evaluating security boundaries asynchronously *before* and *after* the reducer fires. Evaluates Array Rules and JSON Schema Compliance.
* **`telemetry.go` & `config.go`:** Strict metrics contracts (`MetricsRecorder`). The engine defaults identically to `noopMetrics` ensuring zero dependencies or framework overhead (like Prometheus or Datadog) unless explicitly wired by the parent application layer logic.
* **`types.go` & `errors.go`:** Native wrappers locking JSON decoding via `json.RawMessage` arrays protecting mathematical intent precisely from missing `omitempty` bugs.

When triggered, the `Store` executes the following chronological pipeline:

**Step 1: Initialization**
* The Engine loads the `Base State` (externally injected from a Toro Postgres Snapshot, derived from a previous NATS payload, or dynamically initialized as a blank `{}` root envelope).
* `currentStateBytes` is loaded into active memory.

**Step 2: The Middleware Dispatch Loop**
The Store iterates over the array of new NATS events in strict chronological order and pipelines them:
```text
FOR EACH event IN unapplied_events:
  1. Deep-copy Base State into active snapshot boundary.
  2. Middleware PRE: `EnforceBoundsMiddleware` scans for `/_sys` violations.
  3. Middleware PRE: `EnforceRBACMiddleware` authorizes exact path vectors against the `actor`.
  4. Reducer: Execute `ApplyPatchReducer` applying RFC 6902 strings.
  5. Middleware POST: `EnforceNoArrayMiddleware` rejects index risks.
  6. Middleware POST: `EnforceSchemaMiddleware` matches W3C data layout dynamically.
```

**Step 3: The Fork in the Road (Success vs. Failure)**
Because LLMs are probabilistic, the infrastructure pipeline safely degrades when patches fail.

* **If the entire Pipeline succeeds:**
  * The snapshot pointer actively becomes the new compiled State Matrix.
  * The loop continues to the next event logging latency telemetries.

* **If *ANY* Middleware or Reducer step fails (The Ephemeral Circuit Breaker):**
  * The `Store` safely ejects the mutated snapshot cleanly preserving the origin Matrix pointer.
  * **The AI Feedback Loop:** Unlike typical DB-polluting ledgers, the `Store` explicitly returns the failure as a typed `[]DomainFault` array decoupled from the `[]byte` JSON.
  * *Why?* This allows the Host Worker to safely suspend agents breaking tracking limits without hard-writing `system_errors` tracking logs into Postgres permanently!

**Step 4: Compilation Handoff**
Once the loop finishes, the `Reduce()` function terminates returning the mathematically perfect `currentStateBytes`.

### **4. Optimistic Concurrency (The "Test" Enforcement)**

**The Vulnerability:** Two separate triggers hit the Go Kernel simultaneously (e.g., a trucker uploads a receipt, and an internal CPA clicks "Approve" on the frontend). 

**The Redux Defense:**
The Redux Engine leverages the RFC 6902 `"op": "test"` command as an atomic lock. 
The LLM is strictly prompted to prefix every patch array with a `test` against the state version or status it *thinks* it is modifying. 

* **Example Patch Array from LLM:**
  1. `{"op": "test", "path": "/data/status", "value": "AWAITING_RECEIPT"}`
  2. `{"op": "replace", "path": "/data/status", "value": "RECEIPT_PROCESSING"}`

If the CPA already changed the status to `"APPROVED"` 10 milliseconds ago via the UI, the `test` operation in step 1 will instantly fail. The Redux Engine's standard library will reject the entire transaction atomically, preventing the LLM from overwriting human intent.

**In Plain English (The "Race Condition" Shield):**

1. **The Race Condition:** Imagine two things happening at once. An AI spends 5 seconds reading a receipt for a transaction that was marked `"AWAITING_RECEIPT"`. Meanwhile, a human CPA manually clicks "Approve" (`"APPROVED"`). 
2. **The Problem:** When the AI finishes, if it blindly updates the status to `"RECEIPT_PROCESSING"`, it overwrites the human's manual approval. The human wonders why an approved transaction went backwards.
3. **The JSON Patch Solution:** The system forces the AI to submit its changes in an array of steps (a JSON Patch). Crucially, the **very first step** must be a `"test"`.
4. **The Lock:** The "test" step basically says: *"Before doing anything else, check if the status is still EXACTLY `"AWAITING_RECEIPT"`"*. 
   - If the human hasn't touched the transaction, the test passes and the AI's change is accepted.
   - If the human *has* touched the transaction (status is now `"APPROVED"`), the test immediately fails, and the backend completely rejects the AI's update. This safely rejects the AI's change because it was based on an outdated assumption.

**Marc Andreessen:** *[Rubs his hands together and smiles]* This is my favorite part of the process. We built a beautiful, elegant engine using a global internet standard (RFC 6902). Now, we try to break it before your users do.

You’ve learned the hard way that LLMs are chaotic. Even when they are following standard JSON Patch rules, they will find bizarre ways to corrupt your system if you don't put guardrails in the Go Kernel. 

Here are the three fatal blind spots in our current Redux / RFC 6902 design, and exactly how the boardroom patches them.

***

### **Critique 1: The "Array Index Shift" Time Bomb**
* **The Blind Spot:** In JSON Patch, if you want to remove an item from a list, you use its array index: `{"op": "remove", "path": "/data/blockers/1"}`. 
* **The Failure:** Suppose the LLM reads the state and sees three blockers. It decides to remove blocker `#1`. But while the LLM is "thinking" (which takes 5 seconds), a human CPA clicks a button and removes blocker `#0`. When the LLM's patch finally lands, the array has shifted. The LLM blindly executes `remove /1`, but it just deleted the *wrong blocker*. Data is destroyed. 
* **The Fix: The "No-Array" State Rule.** You must ban arrays for any mutable entities in your state tree. Instead of a `blockers` array, you use a `blockers` map (dictionary) keyed by UUIDs or deterministic hashes. 
  * *Old state:* `"blockers": ["Missing receipt", "Bad signature"]`
  * *New state:* `"blockers": {"blk_99": "Missing receipt", "blk_42": "Bad signature"}`
  * Now, the LLM emits `{"op": "remove", "path": "/data/blockers/blk_99"}`. It is mathematically impossible to delete the wrong item, even if the state shifts.

### **Critique 2: "Schema Drift" Breaking the UI**
* **The Blind Spot:** The Go `jsonpatch` library is incredibly obedient. If the LLM tells it to add a new field, it will do it. 
* **The Failure:** The LLM decides it wants to track a trucker's phone number. It emits `{"op": "add", "path": "/data/trucker_phone", "value": "555-1234"}`. The Go Kernel applies it successfully. But your Fignode frontend is expecting the path to be `/data/contact/phone`. The UI crashes, or the data is orphaned and invisible to the user.
* **The Fix: Post-Patch Schema Validation.** Your Go Kernel cannot blindly trust that the resulting state is valid just because the patch applied successfully. *After* the Go Kernel runs `jsonpatch.Apply()`, it must take the new JSON state and run it through a strict JSON Schema Validator (like the `santhosh-tekuri/jsonschema` Go package). If the LLM invented a rogue field, the validator fails, the patch is rejected, and the LLM is hit with an error: *"Schema Violation: Additional property 'trucker_phone' not allowed."*

### **Critique 3: The "Replace All" Doomsday Patch**
* **The Blind Spot:** RFC 6902 allows the `replace` operation to replace *entire objects*, not just single strings.
* **The Failure:** The LLM gets confused during a complex prompt and emits: `{"op": "replace", "path": "/data", "value": {}}`. It just successfully wiped out your entire business payload in one single operation. Or, it generates a patch array with 500 micro-operations, timing out your Go worker and bloating the NATS ledger.
* **The Fix: The OS Sandbox Limits.** Your Go Kernel must inspect the `patch_array` *before* applying it:
  1. **Depth Limit:** Block any `replace` or `remove` operation targeting the root `/data` path. Operations must target specific children (e.g., `/data/status`).
  2. **Operation Limit:** Hard-cap the patch array to a maximum of, say, 10 operations per cycle. If the LLM needs to make 50 changes, it must do it across multiple wake-up cycles. 

### **Critique 4: The Error Loop Problem (Circuit Breaking)**
* **The Blind Spot:** Injecting the AI's failure directly into the persistent `data.system_errors` array pollutes the core database ledger entirely. 
* **The Failure:** If the AI reads a permanent database error and panics, it will generate *another* bad patch, causing an infinite NATS loop that burns your API tokens rapidly.
* **The Fix: Ephemeral Context Defaults.** If a patch fails schema logic or the `test` lock, the state rolls back perfectly. The error is *never* committed to Postgres. Instead, the `Store` Engine returns the error in an explicit `[]DomainFault` array separated from the `[]byte` state. The Host Worker tracks these faults dynamically pushing them into short-term AI prompt memory directly intercepting Agent triggers via Redis-backed Circuit Breakers proactively.

### **Critique 5: The Trust Problem (Path-Based RBAC Middleware)**
* **The Blind Spot:** The Engine treats all events identically. If an AI decides to change the payout routing number randomly, the engine accepts the patch blindly. This represents an astronomical vulnerability scaling untrusted agents.
* **The Failure:** The Agent executes `{"op": "replace", "path": "/data/admin_notes"}` overriding confidential human operations irreversibly locally.
* **The Fix: Actor Middleware Hooks.** The Redux pipeline executes `EnforceRBACMiddleware` inherently. The API mapping defines `AccessControlMiddleware` binding exact paths against explicit actors. `actor: AI_AGENT` mapped only allows targeting matrices under `/data/receipts/` or `/data/status` implicitly. If it breaches out of bounds, it encounters a native `403 Forbidden` terminating pipeline flow gracefully upstream.
