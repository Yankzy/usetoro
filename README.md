This is a formal **Technical Product Requirements Document (PRD)** for **Toro**.

**Product Name:** Toro
**Version:** 1.0
**Core Objective:** To solve the "Integration Hell" B2B developers face by providing two distinct services:

1. **The Hub:** A robust webhook infrastructure to ingest, debug, and reliable forward events.
2. **The Concierge:** A managed sync engine for Plaid that handles the polling logic and delivers clean, normalized financial data.

---

# Section 1: The Webhook Hub ("The Pipe")

**Objective:** Build a high-concurrency ingestion engine that acts as the "middleman" for all third-party webhooks (Stripe, Twilio, GitHub). It must provide visibility (logs), reliability (retries), and developer velocity (local tunneling).

### 1.1 Functional Requirements

#### 1.1.1 Ingestion & Routing

* **Requirement:** The system must expose dynamic, unique URL endpoints (Buckets) for users (e.g., `api.toro.dev/h/{bucket_id}`).
* **Performance:** Must acknowledge receipt to the provider (return `200 OK`) within **100ms** to prevent timeouts, regardless of downstream latency.
* **Queueing:** All incoming payloads must be immediately pushed to a durable queue (Redis/Kafka) before processing.
* **Broadcasting:** Incoming events must be broadcast via **WebSockets** to the connected frontend dashboard and CLI in real-time.

#### 1.1.2 The "Native" Tunnel (CLI)

* **Requirement:** A Go-based CLI tool (`toro listen`) that creates a secure WebSocket tunnel to localhost.
* **Flow:**
1. User runs `toro listen --port 3000`.
2. Toro Server receives webhook -> serializes to binary -> sends down WebSocket.
3. CLI receives binary -> POSTs to `localhost:3000`.
4. CLI captures response (200/500) -> sends back to Server -> Server logs the local response status.



#### 1.1.3 Reliability & Replay

* **Retention:** Store full headers and body payloads for 30 days (Tier 2) or 7 days (Tier 1).
* **Manual Replay:** User can click "Resend" on any historical event. System must issue a new request to the configured destination.
* **Modified Replay:** User can edit the JSON payload in-browser before replaying (for edge-case testing).

### 1.2 AI & "Vibe" Requirements

#### 1.2.1 "Drift Detection" (Schema Monitor)

* **Logic:** The system must maintain a "Fingerprint" of the expected JSON structure for each bucket.
* **Action:** If a new webhook arrives with missing keys or changed data types (e.g., `amount` changed from `int` to `string`), tag the event as **"Schema Drift"** and alert the user.

#### 1.2.2 "Wreckage Analysis" (Auto-Diagnostics)

* **Trigger:** When a webhook returns a `5xx` error from the user's server.
* **Process:** Asynchronously send the Payload + Error Log to an LLM.
* **Output:** A natural language summary pinned to the log (e.g., *"Your server crashed because the 'email' field is null, but your code likely expects a string."*).

### 1.3 Technical Stack & Data Model (Section 1)

* **Language:** Go (Golang) - Optimized for high-concurrency HTTP handling.
* **Database:** PostgreSQL (Partitioned by time for log retention).
* **Real-time:** `gorilla/websocket` for the tunnel and UI updates.

**Data Schema (Simplified):**

```go
type WebhookEvent struct {
    ID          string
    BucketID    string
    Headers     map[string]string
    Body        JSONB
    ReceivedAt  time.Time
    // The response from the customer's server
    DestinationStatus int 
    DestinationLatency int
}

```

---

# Section 2: The Plaid Concierge ("The Syncer")

**Objective:** Abstract away the complexity of Plaid's `transactions/sync` API. The customer should never have to write a polling loop or manage a cursor. They simply receive a clean POST request containing the new transactions.

### 2.1 Functional Requirements

#### 2.1.1 Account Onboarding

* **Input:** User submits `access_token` and `item_id` via the Toro API.
* **Storage:** Toro securely encrypts and stores these tokens in a "Vault" table.
* **State:** Toro initializes a `cursor` value of `null` for this new item.

#### 2.1.2 The Sync Workflow (The "Concierge" Logic)

* **Trigger:** System listens for the `SYNC_UPDATES_AVAILABLE` webhook from Plaid.
* **Workflow (Temporal/Go):**
1. **Wake Up:** Worker acknowledges the hook.
2. **Fetch Loop:**
* Call Plaid `/transactions/sync` using the stored `cursor`.
* If `has_more == true`, append transactions to a buffer and loop again immediately.
* Update the stored `cursor` in DB.


3. **Aggregation:** Collect all `added`, `modified`, and `removed` transactions from the loop.
4. **Delivery:** Send a **single** cleaned JSON POST to the customer's configured `webhook_url`.



#### 2.1.3 Durability & Retries

* **Requirement:** If the Customer's server returns a `500` or times out during delivery, the system must **not** lose the transactions.
* **Strategy:** Use Exponential Backoff (1m, 5m, 1h) to retry delivery for up to 24 hours.

### 2.2 AI Requirements (The Value Add)

#### 2.2.1 Merchant Normalization

* **Process:** Before delivery, pass raw Plaid descriptions through a local NLP model or LLM API.
* **Transformation:**
* `"Uber 072515 SF**POOL"` → `Merchant: "Uber"`, `Category: "Ride Share"`.


* **Benefit:** Customer receives clean data, not raw bank gibberish.

#### 2.2.2 "The Clean Payload" (API Contract)

The customer will receive this JSON structure, regardless of how messy the bank data was:

```json
{
  "event_type": "transactions.new",
  "account_id": "acc_12345",
  "sync_date": "2026-05-20T10:00:00Z",
  "data": {
    "added": [
      {
        "id": "tx_999",
        "amount_cents": 1450,
        "currency": "USD",
        "merchant_clean": "Netflix",
        "raw_description": "NFLX DIGITAL NT Svc",
        "date": "2026-05-19"
      }
    ],
    "removed": ["tx_888"],
    "modified": []
  }
}

```

### 2.3 Technical Stack (Section 2)

* **Orchestration:** **Temporal.io** (Essential for managing long-running sync loops and durable retries).
* **Language:** Go (Worker Nodes).
* **Encryption:** AES-256 for storing Plaid Access Tokens.

---

### 3. Monetization Gates (Technical Enforcement)

To ensure the business model works, the following limits must be enforced in code:

| Feature | Tier 1 (Hobby) | Tier 2 (Pro - $49) | Tier 3 (Agency - $249) |
| --- | --- | --- | --- |
| **Log Retention** | 1 Day | 30 Days | 90 Days |
| **Tunneling** | Single Connection | Multi-seat | Multi-seat |
| **Plaid Sync** | Manual Trigger Only | Auto-Sync (Up to 50 Items) | Auto-Sync (Unlimited) |
| **AI Features** | None | Schema Monitor | Merchant Cleaning & Diagnostics |

### 4. Next Steps for Engineering

1. **Phase 1 (Week 1):** Build the **Go Ingestion Server** (Part 1). Just the HTTP handler + Redis Queue + WebSocket.
2. **Phase 2 (Week 2):** Build the **CLI Tool** (`toro listen`) to verify the tunneling works.
3. **Phase 3 (Week 3):** Implement the **Temporal Workflow** for the Plaid Loop (Part 2).
