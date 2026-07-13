This Product Requirement Document (PRD) outlines how to leverage the Go-based backend and NATS JetStream event architecture to build Toro's autonomous email infrastructure and conversational pipeline.

Instead of a closed-box marketing sequence, this architecture structures infrastructure as a programmatic, bi-directional conversational bridge driven by our Autonomous Semantic Engine (ASE).

---

# Product Requirement Document (PRD)

## Toro Autonomous AI Email Infrastructure & Conversation Bridging

### 1. System Architecture & Tech Stack Core

* **Core Language:** Go (Golang)
* **Event Broker:** NATS JetStream (durable streams, pull-consumers)
* **Email API:** Mailpool API (for programmatic inbound parsing, webhooks, and outbound delivery)
* **State Management:** Autonomous Semantic Engine (ASE) DAGs
* **Database:** PostgreSQL (storing conversation sessions, threads, and delivery states via sqlc)

---

## 2. Core Email Workflows & NATS Topology

The email infrastructure in Toro acts as a dynamic conversational bridge connecting clients, virtual AI employees (e.g., `sarah@cpa.usetoro.io`), and internal Slack teams. 

The architecture leverages NATS JetStream for decoupled, resilient worker processing.

```
[Inbound Webhook (Mailpool)] ──> [Inbound Email Worker] ──> [ASE Email Triage DAG / General Agent Ingress]
                                                                  │
                                                                  v
[Outbound Webhook (Mailpool)] <── [Omni-Chat Worker]  <── [ASE Bookkeeping / Batch Email Worker]
```

---

## 3. Step-by-Step Implementation & Worker Logic

### Step 1: Inbound Email Processing & Thread Resolution

* **Worker:** `mailpool_inbound_email_worker` (replacing legacy Postmark logic)
* **Objective:** Parse inbound webhook payloads from Mailpool, resolve entity/tenant context, and maintain conversational threads.
* **Implementation Truths:**
  * **Agent Alias Parsing:** Extracts the agent alias from the recipient email (e.g., parsing `mark` and `cpa2` from `mark@cpa2.usetoro.io`). Directs emails intended for the COO directly to `vcoo_ingress`.
  * **Thread Resolution:** Parses `In-Reply-To` and `Message-ID` headers to accurately map replies to existing conversation sessions in the database.
  * **Entity Resolution:** Resolves the `EntityID` using three fallback strategies: 
    1) Matching thread IDs (`GetConversationByExternalID`).
    2) Recent conversations by handle (`GetRecentConversationsByHandle`).
    3) Registered user email lookup (`GetEntityIDByEmail`).
  * **Attachment Handling:** Decodes base64 attachments and uploads them directly to S3 (`UploadFileToS3`), passing the S3 keys in the metadata to the LLM context.
  * **Slack Bridging:** Checks the `toro_threads_mappings` table to see if the email is a reply to a bridged Slack thread. If matched, it routes directly to the `omni_chat_worker` to post as a threaded Slack reply.
  * **Default Routing:** Unmatched incoming threads are published to `workers.general_agent_ingress` for standard agent processing.

### Step 2: The Autonomous Semantic Engine (ASE) Integration

* **DAGs:** `email_inbound` and Domain DAGs (e.g., `ase_gaap_us`)
* **Objective:** Understand email intent and extract structured data to unblock financial workflows.
* **Implementation Truths:**
  * **Email Triage DAG (`seed_email_dag.go`):** Inbound emails can be routed through a dedicated DAG to classify the intent using a specialized triage LLM agent.
  * **Bookkeeping Domain Tool (`bookkeeping_tools.go`):** When transactions are blocked requiring human context (e.g., `HOLD_AMBIGUOUS`), the system intercepts the flow and generates an alert payload containing the required questions.
  * **Email Domain Tool (`email_tools.go`):** When the client replies to a clarification email, the Email Tool maps the unstructured human reply back to the specific stuck transaction IDs (via LLM mapping) and emits an `ase.events.resume` event to unblock the DAG.

### Step 3: Outbound Omni-Channel Dispatch

* **Worker:** `omni_chat_worker`
* **Objective:** Deliver outgoing AI responses or system alerts back to the user via Email (using Mailpool).
* **Implementation Truths:**
  * Listens to the `proof.outgoing.chat` NATS subject.
  * Persists the outgoing message into the `conversations` table.
  * Generates robust, thread-safe `Message-ID`s (e.g., `<ase_txnID__hash@agents.usetoro.io>`) to ensure email clients group the messages correctly in threads.
  * Utilizes the Mailpool API to send the email payload.
  * **Bi-directional Slack Sync:** If the response originated from an internal Slack thread, it updates the `email_latest_message_id` pointer in `toro_threads_mappings` so the subsequent user reply properly routes back to the exact Slack thread.

### Step 4: Automated Batch Clarifications

* **Worker:** `batch_email_worker`
* **Objective:** Reduce notification fatigue by rolling up multiple transaction holds into a single digest email.
* **Implementation Truths:**
  * Triggered by a cleanup session completion (`workers.batch_email_generation`).
  * Fetches all held transactions (`GetHeldTransactionsBySession`) and generates a cohesive, professional email body using the LLM runtime.
  * Dispatches the generated digest to the Omni-Chat worker for email delivery.

### Step 5: Delivery Status Tracking

* **Worker:** `mailpool_outbound_events_worker`
* **Objective:** Maintain the real-time delivery state of all outgoing communications.
* **Implementation Truths:**
  * Ingests delivery events (Delivery, Bounce, SpamComplaint, Open, Click) from Mailpool webhooks.
  * Updates the respective `external_id` records in the `conversations` database table via `UpdateConversationDeliveryStatus`.

---

## 4. Key Engineering Guardrails

> ### 1. Thread Pointer Mapping for Slack Bridging
> The system utilizes a specialized `toro_threads_mappings` table. When sending a Slack message that mirrors an email thread, the system captures the generated Slack `ts` and binds it to the current Email `Message-ID`. This ensures continuous, bi-directional synchronization without breaking the conversational tree.

> ### 2. Strict Entity Resolution & Bouncing
> Unauthenticated inbound emails are dropped or bounced if the sender cannot be mapped to an existing `EntityID` (Tenant). This prevents system abuse and wasted LLM tokens on spam or misrouted messages.

> ### 3. Idempotent Resumes via NATS
> When an email reply triggers a transaction DAG resumption, the event is published to `ase.events.resume`. NATS JetStream ensures durable delivery so transient worker failures do not leave the DAG permanently blocked.
