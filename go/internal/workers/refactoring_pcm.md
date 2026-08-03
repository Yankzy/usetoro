### Step 1: The Postmark Ingress Worker (Entrypoint)
**File:** `go/internal/workers/postmark_inbound_email.go`

- **How it subscribes:** 
  In `defaults.yml`, the `postmark_inbound_email` worker is configured with `activity_type: workers.email.postmark_inbound`. The `BuildWorkerInboxFromActivity` function (in `tap/pkg/core/topics.go`) takes this string, splits off the `workers.` prefix, and prepends `worker.inbox.`.
- **Exact NATS Subject:** **`worker.inbox.email.postmark_inbound`**
- **What runs & why:** 
  When Postmark delivers the JSON webhook of an incoming email, this worker receives it. It parses the payload, uploads attachments to S3, and extracts the recipient email to determine the agent alias.
  - If the alias is `reconciliation` or starts with `rap_`, it hits lines 169–180.
  - It realizes this is meant for OCR processing, bypasses standard general agent flow, and publishes the raw payload to the OCR worker.
  - **Note:**  
    - WE need to refactor this worker to use switch on the activity_type.
    - There should be only one msg.Ack() or msg.Nak() in the whole worker.
    - The worker should only work related to postmark inbound emails. Once the destination is established it should package the payload and publish it to the destination subject.
    - If there is an error, the worker should publish it to the error subject.
    
- **Outbound NATS Subject:** **`worker.inbox.pcm_ocr`**

### Step 2: OCR & Payload Structuring
**File:** `go/internal/workers/pcm_ocr_worker.go`

- **How it subscribes:**
  In `defaults.yml`, the `pcm_ocr` worker has an explicit `subject: worker.inbox.pcm_ocr` defined, which overrides derivation.
- **Exact NATS Subject:** **`worker.inbox.pcm_ocr`**
- **What runs & why:**
  This worker simulates processing the physical attachments of the email. It resolves the sender to an `EntityID` and sets the `RealmID`. It builds a structured `PcmPayload` that contains the 4-way match documents (BC, BL, Facture, and Bank Settlement) extracted from the attachments.
  - It wraps this payload in a TAP `core.Envelope` with `dag_name` and `domain_tool` both set to `"pcm_bank_reconciliation"`.
  - It publishes this envelope to trigger the DAG execution via the ASE Bridge.
- **Outbound NATS Subject:** **`worker.inbox.ase_bridge`**

### Step 3: Domain Tool Initialization
**File:** `go/internal/erp/ase/domain_tools/pcm/pcm_bank_reconciliation_tools.go`

- **What runs & why:**
  Before the DAG can start, the orchestrator needs to know how to structure the memory for this specific task. The `pcm_bank_reconciliation` domain tool is fetched from the registry. 
  - Its `BuildAgents()` method unpacks the `PcmPayload` and flattens the `BC`, `BL`, `Facture`, and `Bank Settlement` into the unified `CombinedPayload` that the DAG nodes will operate on. 
  - It creates a new `AutonomousSemanticEngineNode` (the agent).

### Step 4: ASE Orchestration & DAG Execution
**File:** `go/internal/workers/ase_bridge_worker.go`

- **How it subscribes:**
  In `defaults.yml`, the `ase_bridge` worker has an explicit `subject: worker.inbox.ase_bridge` defined. It also explicitly subscribes to `ase.events.resume` to handle paused agents.
- **Exact NATS Subjects:** **`worker.inbox.ase_bridge`** and **`ase.events.resume`**
- **What runs & why:**
  The worker parses the TAP Envelope and launches the agent asynchronously via a GoRoutine calling `agent.Run(ctx, dagToUse, deps.Store)`. 

This initiates the DAG execution defined in **`go/internal/erp/ase/dags/pcm_bank_reconciliation.yml`**. The agent dynamically evaluates and transitions through these nodes step-by-step:

1. **`root` node (`kind: normalization_and_hash`):**
   - It runs the `normalizer` prompt through the LLM to standardize all currencies to Moroccan Dirham (MAD) and format dates/names from the OCR payload.
2. **`document_chain_matcher` node (`kind: 4_way_match`):**
   - It runs the `matcher` prompt to mathematically validate that amounts across the BC, BL, Facture, and Settlement line up exactly.
   - It uses a dynamic edge (`4_way_match_math`) to route the workflow based on the mathematical result.
3. **`vendor_resolution_cache` node (`kind: cache_lookup`):**
   - It attempts to resolve the vendor name into a canonical `Fignode` entity without using the LLM by hitting the database (dynamic edge `vendor_db_lookup`).
4. **`vendor_resolution_llm` node (`kind: vendor_resolution`):**
   - If the cache lookup results in `UNRESOLVED`, this node falls back to using the LLM (`vendor_resolver` prompt) for semantic matching.
5. **`export` node (`kind: terminal`):**
   - Formats the reconciled payload via the `exporter` prompt.
   - Updates the agent state to `READY_FOR_SYNC`, terminating the DAG.

### Step 5: Post-DAG Operations & Alerts
**File:** `go/internal/workers/ase_bridge_worker.go` (Completion routines)

- **What runs & why:**
  Once the DAG finishes (or halts), the `ase_bridge_worker.go` detects the terminal state via an `OnStateChange` callback hook.
  
  **Scenario A: The DAG halted (Missing Document)**
  If the DAG stopped because of a missing document, the state becomes `HOLD_MISSING_CONTEXT`. 
  - The bridge asks the `pcm_bank_reconciliation` domain tool to generate an alert payload (e.g., *"We need a photo of the official Facture..."*).
  - It derives the subject for `workers.general_agent_ingress` → **`worker.inbox.general_agent_ingress`**.
  - It publishes the alert to this subject, which eventually routes back to the client via `omni_chat` (Slack) or email.

  **Scenario B: The DAG completed (or after any DAG run)**
  - At the end of the `Handle` function, the bridge triggers an asynchronous batch email job. It derives the subject for `workers.batch_email_generation` → **`worker.inbox.batch_email_generation`** and publishes a trigger to send batch updates to the CPA.
  - It then sends an `INFORM` TAP Envelope back to the Orchestrator via **`orchestrator.inbox`** (or the explicit `msg.Reply` subject) to confirm the workflow step is completed.