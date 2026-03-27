# Toro OS Technical PRD: Workflow 2
## The Autonomous "Client Chaser" (Durable Execution & The Saga Pattern)

### 1. Executive Summary & The "Time Defiance" Demo
* **Objective:** Automate the asynchronous, multi-day process of querying a client for missing receipt context and categorizing the expense once the information is received.
* **The Demo Value:** You show the VC a live Fignode UI. An ambiguous $412 Home Depot charge appears. You click "Ask Client." The UI instantly moves it to a "Waiting on Client" tab. You then switch to your iPhone, reply to an automated SMS: *"It was lumber for the new office desk."* Instantly, on the Zoom screen, Fignode moves the transaction from "Waiting" to "Ready for Review," perfectly categorized as `Office Supplies`.
* **Why it closes the deal:** It proves your architecture handles asynchronous state across multiple days and multiple communication channels (SMS/WebSockets/NATS) without a single server timeout or race condition. 

### 2. Architectural Definitions (The Toro Physics)
* **The Saga State (The Memory):** When a workflow pauses, the exact state of the transaction (TxID, Amount, Vendor, ClientID, AI Context) is serialized and persisted as an immutable event in the NATS JetStream Vault. The Go worker completely shuts down, freeing up RAM.
* **External Triggers (The Wake-Up Call):** Inbound webhooks (e.g., Twilio SMS reply) that act as external events to rehydrate the Saga and wake up the next Worker.
* **Workers (The Relays):** Decoupled Go services that handle specific legs of the journey: The "Asker," the "Listener," and the "Categorizer."

### 3. The Event-Driven Saga Machine (Step-by-Step Flow)

**Phase A: The Anomaly & The Pause (Day 1)**
1. **Fignode UI (Action):** The AI Agent flags a $412 Home Depot transaction as "Ambiguous" and pushes it to the CPA's Fignode Mobile App. The CPA swipes left, selecting the option: `Trigger Client Query`.
2. **API Gateway Worker:** Receives the swipe action via WebSockets.
   * *Action:* Publishes event `event.transaction.ambiguous_flagged` with the TxID and ClientID.
3. **The Communication Worker (The Asker):** Listens for `event.transaction.ambiguous_flagged`.
   * *Action:* Looks up the client's phone number in the SQLite cache.
   * *Action:* Triggers a Twilio API call to send an SMS: *"Hi [Client], we see a $412 charge at Home Depot on [Date]. What was this for?"*
   * *Action:* **CRITICAL STEP:** Publishes event `event.saga.client_query_pending` to NATS, saving the exact state of the transaction. The Go routine then gracefully terminates. The workflow is now "asleep" in the NATS Vault.

**Phase B: The Rehydration (Day 3)**
4. **The Webhook Listener Worker:** Three days later, the client replies to the Twilio SMS: *"Lumber for the new office desk."* Twilio fires a webhook to Toro OS.
   * *Action:* The Listener Worker receives the HTTP payload. It extracts the phone number and the text body.
   * *Action:* Publishes event `event.inbound.sms_received` to NATS.
5. **The Saga Orchestrator Worker:** Listens for `event.inbound.sms_received`. 
   * *Action:* It queries the NATS JetStream history for the last open `event.saga.client_query_pending` associated with that phone number. 
   * *Action:* It pulls the exact context (the $412 Home Depot charge) out of deep storage. It **rehydrates** the state.
   * *Action:* Publishes event `event.saga.context_rehydrated` with both the original transaction data and the new SMS text.

**Phase C: The Resolution & The Commit (Day 3 - Milliseconds Later)**
6. **The Categorization Agent (Worker Wrapper):** Listens for `event.saga.context_rehydrated`.
   * *Action:* The Agent reads the combined context: [Vendor: Home Depot] + [Client Text: "Lumber for the new office desk"].
   * *Action:* The Agent uses its reasoning to map "office desk" to the QBO Chart of Accounts. It selects `Office Expenses / Furniture`.
   * *Action:* Invokes the deterministic Go Tool to generate the strict RFC 6902 JSON Patch.
   * *Action:* Publishes event `event.ledger.proposal_generated`.
7. **WebSocket Sync Worker:** Listens for `event.ledger.proposal_generated`.
   * *Action:* Pushes the proposed categorization to the Fignode UI. The transaction instantly moves from the "Waiting on Client" tab to the "Ready for Approval" tab. 

### 4. Technical Constraints & Fallbacks
* **The Timeout Policy (Saga Expiration):** If a client ignores the SMS for 7 days, the Saga cannot stay pending forever. 
    * *Fallback:* A dedicated "Cron Worker" sweeps NATS daily for `event.saga.client_query_pending` events older than 7 days. If found, it publishes `event.saga.timeout_escalated`, which pushes the transaction back to the CPA's Fignode UI marked "Client Unresponsive - Manual Override Required."
* **Context Collisions:** If a client has *three* pending Home Depot charges and replies *"Desk lumber"* to the SMS, the AI must not guess which transaction it belongs to.
    * *Fallback:* The Saga Orchestrator detects multiple pending states for one number. It triggers a fallback event to SMS the client again: *"Did you mean the $412 charge on Tuesday, or the $80 charge on Thursday?"*

### 5. Development Milestones for this PRD
* **Milestone 1:** Build the Twilio/SMS Webhook Listener Worker.
* **Milestone 2:** Engineer the NATS "State Rehydration" logic (the ability to query past events and merge them with new incoming webhook data).
* **Milestone 3:** Build the "Saga Timeout/Cron" Worker to ensure no transaction gets permanently lost in limbo.
* **Milestone 4:** Update the Fignode React UI to support multi-tab states ("Inbox," "Waiting on Client," "Ready to Sync").
