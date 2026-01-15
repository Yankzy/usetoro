# **Product Requirements Document: Toro v1.1**

**Product Name:** Toro (The Bull)
**Domain:** `usetoro.io`
**Core Objective:** To be the "Stability Layer" for modern Vibe Coders and B2B SaaS teams. We sit between chaotic inputs (Webhooks, AI Agents) and fragile infrastructure (Postgres, Localhost).

---

## **Section 1: The Pipe (Inbound Webhooks)**

*The Gateway: Ingest, Debug, Tunnel.*

**Problem:** Developers struggle to debug webhooks on localhost, and they lose data when their servers crash.
**Solution:** A high-concurrency ingestion engine that acts as a "durable middleman" for all third-party events (Stripe, Twilio, GitHub).

### **1.1 Functional Requirements**

* **1.1.1 The "Instant" Receiver**
* **Endpoint:** `api.usetoro.io/h/{bucket_id}`
* **Behavior:**
* Accepts `POST/PUT` requests instantly.
* **SLA:** Returns `200 OK` to the provider within **50ms** (ACK only) to prevent timeouts.
* **Architecture:** `Ingest -> Redis Queue`. The main HTTP thread never blocks.


* **Broadcasting:** Pushes payload via WebSocket to the Dashboard and CLI in real-time.


* **1.1.2 The "Native" Tunnel (CLI)**
* **Goal:** Replace Ngrok with a built-in, authenticated solution.
* **Command:** `toro listen --port 3000`
* **Flow:**
1. Toro Cloud receives webhook.
2. Forwards binary frame down WebSocket to User's CLI.
3. CLI hits `localhost:3000`.
4. CLI captures the response (e.g., `500 Error`) and sends it back to the Cloud Dashboard.




* **1.1.3 "Wreckage Analysis" (AI Diagnostics)**
* **Trigger:** When a tunneled request returns status `5xx` or `4xx`.
* **Action:** Asynchronously bundles the *Failed Payload* + *Local Error Log* -> LLM.
* **Output:** A pinned "Crash Report" in the dashboard.
> *Example: "Stripe sent a null `tax_id`, but your local server threw a `TypeError: Cannot read property 'length' of null`."*





---

## **Section 2: The Airbag (The Write Buffer)**

*The Vibe Guard: Protecting Supabase/Postgres from N8N & AI Agents.*

**Problem:** "Vibe Coding" stacks (N8N, Retool, LangChain) crash databases because they open too many concurrent connections (Connection Exhaustion).
**Solution:** A managed "Write Queue" that sits between the Agent and the Database.

### **2.1 Functional Requirements**

* **2.1.1 The "Safe" Buffer Endpoint**
* **Endpoint:** `api.usetoro.io/buffer/{buffer_id}`
* **Use Case:** User points their N8N "HTTP Request" node here instead of connecting directly to Postgres.
* **Behavior:** Toro accepts the JSON payload instantly and releases the N8N workflow.


* **2.1.2 The Connection Pooling Worker**
* **Mechanism:** A dedicated Go worker pulls jobs from the Redis queue.
* **The Guard:** This worker maintains **ONE** high-quality, persistent connection pool to the user's Supabase instance.
* **Throughput:** Even if 1,000 AI agents trigger simultaneously, Toro writes them to the DB sequentially (or in batches of 50).
* **Result:** **Zero Database Crashes.**


* **2.1.3 The "Hallucination Firewall"**
* **Schema Enforcement:** User defines a strict schema (e.g., `{ "age": "integer", "email": "string" }`).
* **Sanitization:** If an AI Agent sends `{"age": "twenty"}`, Toro intercepts it.
* **Auto-Repair:** Uses a micro-LLM to fix the type (`"twenty"` -> `20`) before attempting the SQL INSERT.
* **Quarantine:** Unfixable records are sent to a "Dead Letter Queue" for manual review, preventing database corruption.



---

## **Section 3: The Concierge (Plaid Sync)**

*The Partner: Managed Financial Data Sync.*

**Problem:** Integrating Plaid requires building complex polling loops, managing cursors, and handling messy bank descriptions.
**Solution:** A "Set and Forget" engine. We handle the loop; the customer gets clean webhooks.

### **3.1 Functional Requirements**

* **3.1.1 The Sync Engine (Temporal)**
* **Input:** User submits Plaid `access_token` and `item_id`.
* **Trigger:** System listens for Plaid's `SYNC_UPDATES_AVAILABLE` webhook.
* **Workflow:**
1. **Wake Up:** Temporal Workflow starts.
2. **Fetch:** Calls Plaid `/transactions/sync` using the stored `cursor`.
3. **Loop:** Continues paging until `has_more == false`.
4. **Save:** Updates the new cursor in the Toro Vault.




* **3.1.2 Merchant Normalization (AI)**
* **Process:** Passes raw bank descriptions through a specialized cleaning model.
* **Transformation:**
* *Raw:* `"PAYPAL *STEAM GAMES 4253 CA"`
* *Clean:* `{ "merchant": "Steam", "category": "Entertainment", "logo": "steam.png" }`




* **3.1.3 The "Clean" Delivery**
* **Action:** POSTs a single, sanitized JSON payload to the customer's server.
* **Reliability:** If the customer's server is down, Toro retries for 24 hours (Exponential Backoff).



---

## **4. Technical Standards**

* **Language:** **Go (Golang)**. Chosen for high concurrency and single-binary deployment.
* **Orchestration:** **Temporal.io**. Essential for the durability of the Plaid Sync loops and reliable retries.
* **Database:** **PostgreSQL**. Used for log retention, user vaults, and cursors.
* **Queue:** **Redis**. Used for the "Write Buffer" and real-time WebSocket broadcasting.

---

## **5. Monetization Strategy**

| Feature | **Tier 1: Builder ($29/mo)** | **Tier 2: Team ($99/mo)** | **Tier 3: Scale ($299/mo)** |
| --- | --- | --- | --- |
| **Focus** | Solo Vibe Coders | Agencies & Startups | High-Volume SaaS |
| **The Pipe** | 7-day retention. | 30-day retention. | 90-day retention. |
| **The Airbag** | **Crash Protection.** (Queueing). | **AI Cleaning.** (Auto-fixes data types). | **High Throughput.** (Dedicated Workers). |
| **The Concierge** | Manual Sync Trigger. | 50 Connected Accounts. | 500+ Connected Accounts. |

---

## **6. The Toro CLI Reference**

*Designed for the "Flow State"*

* **Login:** `toro login` (Opens browser auth).
* **Tunnel:** `toro listen --port 3000` (Starts the WebSocket tunnel).
* **Sync:** `toro sync` (Manually triggers a Plaid sync for local testing).
* **Status:** `toro status` (Shows active tunnels and queue health).
