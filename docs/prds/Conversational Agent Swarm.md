### **Mark, Ken, and Sarah**

"This is the master blueprint. We are taking the Omni-Channel Gateway, the NATS JetStream orchestration, and the dynamic context routing, and unifying them into a single, scalable product document. Here is the PRD for the Conversational Agent Swarm."

---

# **PRODUCT REQUIREMENTS DOCUMENT (PRD)**

**PROJECT:** Toro OS "Conversational Agent Swarm"
**DATE:** May 11, 2026
**LOCATION:** Casablanca HQ
**LEAD:** Core Infrastructure & Product Team

## **1. EXECUTIVE SUMMARY**

The **Conversational Agent Swarm** is a multi-tenant, stateless AI architecture designed specifically for accounting professionals. It solves the "User Fatigue" and "Context Bloat" problems by allowing CPAs to dynamically spawn, manage, and interact with a network of specialized AI workers (Bookkeepers, Document Chasers, OCR Extractors) entirely through natural language and simple slash commands (`/new`) via SMS, WhatsApp, or Slack.

Instead of point-to-point API tangles, agents communicate asynchronously via a NATS JetStream event bus. This ensures infinite horizontal scalability, mathematical isolation of tenant data, and a massive reduction in LLM token costs.

---

## **2. CORE USER EXPERIENCE (UX)**

The system relies on "Zero-UI" principles. The CPA manages the firm strictly through chat.

### **2.1 The Omni-Channel Gateway**

* **Single Endpoint:** The firm interacts with a single phone number or Slack integration.
* **Seamless Handoff:** The CPA does not need to know which agent is active; the Master Gateway routes the message to the correct active `session_id`.

### **2.2 The `/new` Slash Command (Dynamic Provisioning)**

To prevent users from dumping all requests into one massive, token-heavy thread, the system enforces contextual boundaries.

* **Trigger:** User types `/new`.
* **Action:** The system suspends the current active context and responds: *"I am your new agent. What do you want me to do?"*
* **Specialization:** The user replies (e.g., *"Handle missing W-9s"*). The system locks this new session with a highly specific system prompt and isolates its memory.

### **2.3 Context Switching**

* If the user types `/list`, the system returns active sessions: `1. Bookkeeper`, `2. W-9 Chaser`, `3. Client Onboarding`.
* The user can switch contexts by typing `/switch 1`.

---

## **3. SYSTEM ARCHITECTURE (THE BACKEND)**

The architecture abandons stateful "pet" bots in favor of stateless Go Workers acting on a central queue.

### **3.1 The Identity & Gateway Router (Go Worker)**

1. Catches the inbound webhook from Twilio/Slack.
2. Queries Postgres: `SELECT tenant_id FROM users WHERE phone_number = inbound_number`.
3. Rejects unregistered numbers instantly (Zero-Trust Security).
4. Routes the payload to the correct NATS subject based on the active `session_id`.

### **3.2 NATS JetStream (The Orchestration Bus)**

Agents **never** communicate directly with each other. They publish to and subscribe from topics.

* **Topics:** `toro.tenant.{id}.events.document_needed`, `toro.tenant.{id}.events.image_received`, `toro.tenant.{id}.events.categorization_failed`.
* **Behavior:** When the Bookkeeper Agent needs a document, it publishes to `document_needed` and goes to sleep. The Chaser Agent subscribes to `document_needed`, wakes up, texts the client, and goes back to sleep.

### **3.3 Postgres `JSONB` State Management**

* **Table `agent_sessions`:** Stores `id`, `tenant_id`, `role_prompt`, and `chat_history_array`.
* **Token Optimization:** By strictly isolating `chat_history_array` by task, the LLM context window remains tiny, dramatically reducing API costs and preventing hallucination bleed-over between tasks.

---

## **4. THE BASE AGENT PRIMITIVES**

The swarm launches with three core, specialized primitives that interact via the event bus:

1. **The Bookkeeper Agent:**
* **Trigger:** Monthly cron job or manual chat command.
* **Action:** Maps CSV data to the Chart of Accounts.
* **Event Emission:** Emits `missing_receipt` or `unrecognized_vendor` to the bus if data is incomplete.


2. **The Chaser Agent:**
* **Trigger:** Listens for `missing_receipt` or `w9_needed` events on NATS.
* **Action:** Automates outbound SMS/Email to the end-client. Manages follow-up cadences (e.g., text every 3 days).


3. **The OCR / Vision Agent:**
* **Trigger:** Listens for `image_received` events (when a client replies to the Chaser).
* **Action:** Runs vision extraction, validates the JSON output, and emits `document_ready` back to the Bookkeeper.



---

## **5. SECURITY, COMPLIANCE, & AUDITABILITY**

Because this system manages financial and tax data autonomously, strict guardrails are hard-coded into the Go infrastructure.

### **5.1 The Hallucination Circuit Breaker**

* If a single task (e.g., `Task_ID: 994`) bounces between agents on the NATS queue more than **3 times** without a `resolved` status, the system freezes the task.
* It alerts the CPA via chat: *"⚠️ Swarm loop detected on Home Depot receipt. Manual review required. [Link to Dashboard]"*

### **5.2 Immutable Audit Trails**

* Every time an agent takes an action or passes a message to another agent, the Go Worker writes a read-only record to the `swarm_audit_log` Postgres table.
* This provides a complete, timestamped history of exactly *why* the AI made a decision, satisfying IRS and firm compliance requirements.

### **5.3 Granular IAM (Human-in-the-Loop)**

* **Read-Only vs. Read-Write:** Junior staff can use the chat interface to query data via the swarm. Only verified Firm Partners can issue destructive/write commands (e.g., *"Finalize the Q3 ledger"*).