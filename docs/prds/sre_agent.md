# Technical PRD: SRE AI Auto-Remediation via Ephemeral Wasm/Firecracker

## 1. Executive Summary
**Objective:** Build an automated, zero-downtime remediation system for the core workflow orchestration engine. When a static worker fails during a workflow step, an LLM-powered SRE Agent will dynamically generate a Go-based hotfix, compile it to WebAssembly (Wasm), and seamlessly route subsequent traffic for that step to an ephemeral Firecracker microVM until a human-deployed permanent fix clears the automated patch.

**Out of Scope:** High-throughput caching optimizations, backpressure handling, and load balancing mechanics (to be addressed in subsequent infrastructure iterations).

---

## 2. Core Architecture & Components


| Component | Description |
| :--- | :--- |
| **Almanac (NATS JetStream)** | The centralized hive where standard AI Agents and Static Workers register their capabilities. |
| **Workflow Orchestrator** | The central engine that tracks state, routes payloads to appropriate workers via the Almanac, and handles failure interceptions. |
| **SRE AI Agent** | An LLM-powered agent tasked with analyzing broken payloads/code and generating functional Go code to fix the specific failure. |
| **Wasm Builder Service** | A dedicated internal service that accepts Go code from the SRE Agent and compiles it into a lightweight `.wasm` binary using **TinyGo**. |
| **Blob Storage (S3/MinIO)** | Persistent storage for the compiled `.wasm` hotfix artifacts. |
| **Firecracker Wasm Runtime** | Ephemeral, scale-to-zero microVMs equipped with a Wasm runtime (e.g., Wasmtime) and WASI/Host bindings to execute the hotfix. |
| **Postgres Database** | The single source of truth for state, specifically the `workflow_blueprint` table containing a `jsonb` column of workflow steps. |

---

## 3. Execution Sequences

### Sequence A: The Failure & Generation Loop
1. **Execution & Failure:** The Orchestrator routes a payload to a Static Worker for a specific workflow step. The worker encounters a fatal error (e.g., API schema change) and returns the error and payload to the Orchestrator.
2. **Workflow Pause:** The Orchestrator pauses the current workflow instance to prevent data loss.
3. **Prompting the SRE Agent:** The Orchestrator packages the static worker's original logic, the failed payload, and the error trace, sending them to the SRE AI Agent.
4. **Code Generation:** The SRE Agent writes a hotfix in **Go**.
5. **Compilation Gatekeeper:** The Builder Service attempts to compile the Go code using TinyGo.
    * *Failure:* If compilation fails (syntax/type error), the compiler output is fed back to the SRE Agent to fix and retry.
    * *Success:* The Builder outputs a `.wasm` binary.

### Sequence B: Storage and Blueprint Mutation

1. **Artifact Storage:** The Orchestrator saves the successfully compiled `.wasm` file to Blob Storage.
2. **JSONB Mutation (The "Justin Bieber" Patch):** The Orchestrator performs an atomic `UPDATE` on the Postgres `workflow_blueprint` table. It mutates the specific step inside the `jsonb` array to append the hotfix reference:
   ```json
   "step_id_3": {
     "action": "quickbooks_upsert",
     "worker_id": "static-qb-v1",
     "hotfix_artifact": "s3://wasm-bucket/qb-fix-123.wasm" 
   }
   ```

### Sequence C: The Circuit Breaker & Ephemeral Execution
1. **Workflow Resumption/Subsequent Runs:** The Orchestrator reads the `workflow_blueprint` from Postgres for the paused workflow (and any new workflows hitting this step).
2. **Routing Decision:** It detects the `hotfix_artifact` key. It bypasses the Almanac/Static Worker entirely.
3. **Provisioning:** The Orchestrator spins up a Firecracker microVM (~150ms boot time), loads the `.wasm` artifact from S3, and passes the payload.
4. **Execution:** The Wasm module executes the logic (utilizing WASI/Host bindings for any external networking, like HTTP requests to an API).
5. **Teardown:** The Wasm module returns the downstream payload to the Orchestrator, and the Firecracker VM is instantly terminated.

### Sequence D: Cache Invalidation (The Permanent Fix)
1. Human engineers are alerted to the hotfix via standard monitoring channels.
2. Engineers write, test, and merge a permanent fix into the main codebase for the Static Worker.
3. Upon CI/CD deployment of the new static worker, a database migration/script executes an `UPDATE` on the `workflow_blueprint` table to remove the `hotfix_artifact` key from the JSONB object.
4. The Orchestrator immediately resumes routing traffic to the updated Static Worker via the Almanac.

---

## 4. Technical Requirements & Constraints

### 4.1. The Builder Environment
* Must use **TinyGo** to ensure minimal binary size and rapid cold-start times.
* Must enforce strict compilation timeouts to prevent the LLM from hanging the builder.

### 4.2. Firecracker & Wasm Environment
* **WASI/Host Functions:** Standard Wasm lacks outbound network access. The Wasm runtime inside Firecracker must expose specific Host Functions (e.g., an HTTP client wrapper) so the LLM-generated Go code can interact with external/upstream/downstream APIs.
* **Resource Limits:** Firecracker VMs must be strictly metered (CPU/Memory limits) and have absolute timeout limits (e.g., 10 seconds maximum execution time) to prevent LLM-hallucinated infinite loops from consuming infrastructure resources.

### 4.3. Database Operations
* Updates to the `workflow_blueprint` `jsonb` column must use Postgres atomic JSONB operators (e.g., `jsonb_set` or the `||` concatenation operator) to prevent race conditions when multiple workflows are actively reading/writing.

---

## 5. Security & Isolation
* **Double Sandboxing:** The AI-generated code is constrained logically by the WebAssembly linear memory sandbox, which is physically constrained by the Firecracker hardware-level microVM.
* **Statelessness:** Firecracker VMs mount no host directories and share no memory. They receive the payload via STDIN/Arguments and output via STDOUT, ensuring the LLM-generated code cannot pollute host state.