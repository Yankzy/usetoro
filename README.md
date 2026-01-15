# **Product Requirements Document: Toro v1.0**

**Product Name:** Toro (The Bull)
**Tagline:** The Stability Layer for Modern Backends.
**Core Value:** "Don't crash your database. Don't fight API limits. Just send it to Toro."

---

## **Section 1: The Webhook Hub ("The Pipe")**

*The Foundation: Ingest, Debug, Tunnel.*

**Objective:** Build a high-concurrency ingestion engine that acts as the "middleman" for all third-party webhooks (Stripe, Twilio, GitHub). It provides visibility, local tunneling, and automated debugging.

### **1.1 Functional Requirements**

* **1.1.1 The "Instant" Receiver**
* **Endpoint:** Dynamic buckets (`api.toro.dev/h/{bucket_id}`).
* **SLA:** Must respond `200 OK` to the provider within **50ms** (ACK only). Processing happens asynchronously.
* **Architecture:** `Ingest -> Redis Queue`. The main HTTP thread never touches the database.


* **1.1.2 The "Native" Tunnel (CLI)**
* **Tool:** `toro listen --port 3000`
* **Protocol:** Persistent WebSocket.
* **Behavior:** Forwards payloads to `localhost`. Captures the local response code (200/500) and sends it back to the cloud dashboard for debugging.


* **1.1.3 "Wreckage Analysis" (AI Debugger)**
* **Trigger:** When a forwarded webhook fails (Status 5xx).
* **Action:** Asynchronously compares the payload against the last successful one.
* **Output:** Natural language root cause pinned to the log (e.g., *"Crash Reason: The 'email' field is null."*).



### **1.2 Technical Stack**

* **Lang:** Go (Fast HTTP handling).
* **Real-time:** `gorilla/websocket`.
* **Storage:** Postgres (Logs), Redis (Hot Queue).

---

## **Section 2: The Write Buffer ("The Airbag")**

*The Vibe Guard: Protecting Supabase/Postgres from N8N & AI Agents.*

**Objective:** Solve the "Connection Exhaustion" problem for low-code builders. Serve as a durable queue between chaotic sources (AI Agents, N8N workflows) and fragile destinations (Supabase, SQL).

### **2.1 Functional Requirements**

* **2.1.1 The "Safe" Write Endpoint**
* **Input:** Users send JSON to `api.toro.dev/buffer/{buffer_id}` instead of writing directly to their DB.
* **Behavior:** Toro accepts the request instantly and releases the N8N workflow (preventing timeouts).


* **2.1.2 Connection Pooling Worker**
* **Mechanism:** A dedicated Go worker pulls jobs from the queue.
* **Constraint:** It maintains **ONE** persistent connection pool to the user's Supabase/Postgres instance.
* **Throughput:** Even if 5,000 agents trigger at once, Toro writes them to the DB sequentially (or in controlled batches of 50). **Zero crashes.**


* **2.1.3 The "Hallucination Firewall"**
* **Schema Enforcement:** User defines a strict schema (e.g., `age: int`).
* **AI Cleaning:** If an AI agent sends messy data (e.g., `age: "twenty"`), Toro intercepts it.
* **Repair:** Uses a micro-LLM call to fix the type (`"twenty"` -> `20`) before attempting the SQL INSERT.
* **Quarantine:** If unfixable, the record is moved to a "Dead Letter Queue" for manual review, ensuring the database stays clean.



---

## **Section 3: The Plaid Concierge ("The Syncer")**

*The Partner: Managed Financial Data Sync.*

**Objective:** Abstract away the complexity of Plaid's `transactions/sync` API. The customer receives clean, normalized financial data via webhook, without writing polling loops.

### **3.1 Functional Requirements**

* **3.1.1 The "Sync Loop" Engine**
* **Trigger:** Receives `SYNC_UPDATES_AVAILABLE` from Plaid.
* **Action:** Temporal Workflow wakes up.
* **Logic:**
1. Retrieves `cursor` from Vault.
2. Loops Plaid API until `has_more == false`.
3. Aggregates 1,000+ transactions into memory.




* **3.1.2 Merchant Normalization (AI)**
* **Process:** Passes raw descriptions (`"UBER *TRIP..."`) through a local NLP model.
* **Output:** Adds `merchant_clean` and `category_normalized` fields to the JSON.


* **3.1.3 Delivery**
* **Action:** POSTs a single, clean JSON payload to the customer’s API.
* **Reliability:** Uses exponential backoff (up to 24h) if the customer’s server is down.



### **3.2 Technical Stack**

* **Engine:** **Temporal.io** (For durable execution and retries).
* **Security:** AES-256 (Access Token Vault).

---

## **4. Monetization Strategy (B2B)**

| Feature | **Tier 1: Builder ($29/mo)** | **Tier 2: Team ($99/mo)** | **Tier 3: Scale ($299/mo)** |
| --- | --- | --- | --- |
| **Focus** | Solo Vibe Coders | Small Agencies / Startups | High-Volume SaaS |
| **The Pipe** | 7-day retention | 30-day retention | 90-day retention |
| **The Airbag** | **Queue only.** (Protects DB from crashes). | **AI Cleaning.** (Auto-fixes data types). | **High Throughput.** (Dedicated Workers). |
| **The Syncer** | Manual Trigger. | 50 Connected Accounts. | 500+ Connected Accounts. |

---

## **5. Roadmap & Implementation Plan**

1. **Phase 1: The "Uncrashable" MVP (Weeks 1-2)**
* Build the Go Ingestion Server (Section 1).
* Build the Redis-to-Postgres worker (Section 2 - The Buffer).
* *Goal:* Sell to N8N users immediately. "Stop crashing Supabase."


2. **Phase 2: The Tunnel (Week 3)**
* Release `toro` CLI.
* *Goal:* Developer stickiness.


3. **Phase 3: The Plaid Engine (Week 4+)**
* Implement Temporal workflows.
* *Goal:* High-ticket B2B sales.
