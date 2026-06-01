# Product Requirement Document (PRD)

## Project: Temporal Semantic Stitching (TSS) Engine
## 1. Executive Summary & Problem Statement

### 1.1 The Core Problem

In automated accounting, a bank clearing transaction is treated as an isolated event. However, a bank clearing transaction is merely the trailing physical echo of an asynchronous, highly fragmented human workflow that occurred days prior.

Humans drop data in pieces across different channels at different times:

* **Event A (Context):** A Slack chat occurs on Monday detailing an upcoming business trip.
* **Event B (Proof):** An email invoice PDF lands on Tuesday.
* **Event C (Settlement):** The actual corporate card transaction clears the banking feed on Friday.

Standard AI frameworks fail because they only analyze Event C when it arrives, forcing an expensive Large Language Model (LLM) loop to guess the context. This pattern drives up API compute costs (COGS), increases ledger latency, and introduces a high probability of data hallucination.

### 1.2 The Solution: Temporal Semantic Stitching (TSS)

The TSS Engine transforms the **Autonomous Semantic Engine (ASE)** from a reactive database processor into a predictive, time-aware graph constructor. Operating entirely within a single-tenant data silo, TSS maintains an active, in-memory sliding window cache of unallocated business communication artifacts.

When a physical settlement event occurs (the Anchor Event), the engine evaluates the mathematical, temporal, and semantic affinity of the floating context fragments. It binds them together into a single, cohesive transactional history *before* the transaction ever reaches the Rule Engine or an external LLM queue, driving the processing cost of correlated events to absolute zero.

---

## 2. Technical Stack & Component Mapping

The TSS engine utilizes a specific multi-tier architecture designed to manage high-velocity, in-memory graph mutations alongside persistent relational embeddings.

```
 [Slack / Email Ingress] ──► NATS JetStream ──► Go Ingestion Daemons
                                                     │
                                                     ▼
                                        [Postgres: Generate Embeddings]
                                                     │
                                                     ▼
                                        [Redis Stack: Hydrate Buffer]
                                                     │
                                            (Sliding 7-Day Window)
                                                     ▼
 [Bank Settlement Event] ───────────────► [TSS Affinity Core Matcher]
                                                     │
                                            (Affinity Score ≥ 0.88)
                                                     ▼
                                        [Postgres Relational Ledger]

```

### 2.1 The Infrastructure Matrix

* **NATS JetStream (Ingress Log):** Consumes raw text strings from Slack webhooks and Email IMAP daemons asynchronously. Acts as the linear time log used to replay streaming historical interactions.
* **Redis Stack (Active Graph Cache):** Stores the `FloatingContextNode` dataset. Handles low-latency, real-time node mutations and background time-decay pointer calculations.
* **Postgres + `pgvector` (Context Vector Space):** Converts text fragments and PDF metadata strings into multi-dimensional vector embeddings, allowing the Go core to calculate linguistic proximity.
* **Postgres (Relational Matrix):** The strict double-entry target ledger. Accepts the final, consolidated data payload only after a successful TSS state collapse event occurs.

---

## 3. Technical Specifications & Mathematical Modeling

### 3.1 The TSS Node Schema

Unmapped communications are held inside the active runtime cache wrapped by the following structured Go objects:

```go
type FloatingContextNode struct {
    EventID       string    `json:"event_id"`
    TenantID      string    `json:"tenant_id"`
    SourceChannel string    `json:"source_channel"` // "SLACK", "EMAIL_BODY", "EMAIL_PDF"
    PayloadVector []float32 `json:"payload_vector"` // Gen-2 Vector via pgvector
    Timestamp     time.Time `json:"timestamp"`
    RawContent    string    `json:"raw_content"`
}

type SettlementAnchorEvent struct {
    TransactionID string    `json:"transaction_id"`
    TenantID      string    `json:"tenant_id"`
    Amount        float64   `json:"amount"`
    BankLabel     string    `json:"bank_label"`    // Raw bank feed string descriptor
    PayloadVector []float32 `json:"payload_vector"` // Vector embedding of the bank label
    Timestamp     time.Time `json:"timestamp"`
}

```

### 3.2 The Spatio-Temporal Affinity Equation

When a `SettlementAnchorEvent` ($e_a$) hits the engine, the TSS core scans the active `FloatingContextNode` pool ($e_c$) within that specific tenant's sliding window. It computes a unified **Temporal Affinity Score ($A$)** for every potential correlation path:

$$A(e_a, e_c) = \cos(\theta) \cdot e^{-\lambda \Delta t}$$

Where:

* $\cos(\theta)$ is the exact Cosine Similarity calculated between the two multi-dimensional vector embeddings within `pgvector`:

$$\cos(\theta) = \frac{\mathbf{e_a} \cdot \mathbf{e_c}}{\|\mathbf{e_a}\| \|\mathbf{e_c}\|}$$

* $\Delta t$ represents the absolute time distance calculated in hours separating the real-world execution points of the two events:

$$\Delta t = |t_a - t_c|$$

* $\lambda$ represents the calibrated decay constraint set by system operators to match the tenant's transaction cadence. The systemic baseline is hardcoded to $\lambda = 0.05$ (meaning a connection's weight decays by 50% every 14 hours).

> ### The Stitching Threshold Constraint
> 
> 
> An incoming bank transaction will execute an automated **Graph Weld** with a floating conversational node without executing external LLM lookups if and only if the Affinity Score satisfies:
> $$A(e_a, e_c) \ge 0.88$$
> 
> 

---

## 4. Functional Engine Workflow & Lifecycle

```
[Context Ingest] ──► [Vectorize & Cache] ──► [Anchor Event Hit] ──► [Affinity Match] ──► [State Collapse]

```

### Phase 1: Ingestion & Buffer Hydration

1. A user updates a project management channel or drops an invoice receipt via email.
2. A lightweight background worker computes its vector fingerprint and populates it as a `FloatingContextNode` inside the Redis memory frame.
3. The node is stamped with a Time-To-Live (TTL) header set to precisely **168 hours (7 days)**. If no matching financial anchor interacts with it during this lifecycle window, it is systematically pruned to protect database memory density.

### Phase 2: Anchor Processing

1. A hard transaction clears the client's automated banking feed (e.g., `$89.50 executed at AMZN MKTP`).
2. The ASE instantiates a `SettlementAnchorEvent` loop and generates a proximity vector embedding based on the raw bank description text.

### Phase 3: Matrix Match Evaluation

1. The Go core queries the active Redis pool for all unmatched context nodes sharing that specific `TenantID`.
2. It executes the time-decay affinity score function concurrently across the pool using native Go routines.

### Phase 4: The Joint State Collapse

1. If an explicit node matches with an affinity score of $0.94$, the engine halts further searching.
2. The core structures a unified ledger block: it binds the receipt image and conversational text metadata directly onto the physical bank clearing payload.
3. The engine clears the context node from the memory buffer cache and commits an ACID-compliant, finalized accounting entry directly to the primary Postgres database tables.

---

## 5. Edge Case Governance & Collision Management

### 5.1 Multi-Match Collision Management (Identical Value Conflicts)

A collision exception occurs when an anchor event matches multiple floating context nodes with near-identical affinity metrics (e.g., a business traveler uploads two separate ride-sharing receipts on the same afternoon for the exact same dollar amount of `$15.00`).

```
                              ┌──► Node A (Affinity: 0.91)
                              │
[Anchor Event: $15.00 Charge]─┤
                              │
                              └──► Node B (Affinity: 0.90)
                                        │
                             (Delta Within 0.05 Constraint)
                                        ▼
                           [STATUS_COLLISION_LOCKED]
                                        │
                                        ▼
                           [Dispatched to Morocco HITL]

```

#### Protocol Enforced:

If the mathematical variance between the top two highest scoring nodes drops below an operational delta threshold:

$$|A(e_a, e_{c1}) - A(e_a, e_{c2})| \le 0.05$$

The engine is forbidden from executing an automated state collapse.

1. The transaction status transitions to `STATUS_COLLISION_LOCKED`.
2. The automated processing loop is frozen to prevent incorrect ledger reporting.
3. The node history map is packaged into a JSON payload and pushed to the **Moroccan Exception-Pilot Operational Dashboard** for immediate human resolution.

### 5.2 Negative-Time Probing (Late Invoices)

In rare operational scenarios, a bank account is debited *before* the human uploads the receipt or emails the invoice copy (e.g., an automated subscription renewal hits the card, and the vendor emails the statement 24 hours later).

#### Protocol Enforced:

If an incoming bank anchor event finds zero matches inside the active memory buffer, it does not route directly to an expensive LLM prompt loop.

1. The anchor creates a placeholder object in Redis with an inverted tracking flag: `state: AWAITING_TEMPORAL_CONTEXT`.
2. The anchor node waits in active memory for an execution gate of **48 hours**.
3. If an incoming invoice or Slack note arrives within that 48-hour window and yields a retro-active affinity score of $\ge 0.88$, the system maps the correlation backward in time, clears the ledger entry, and commits the state to Postgres.

---

## 6. System Performance & Memory Constraints

To maintain extreme processing speeds, the TSS engine must execute within strict systems boundaries:

* **In-Memory Volatility:** The active graph matrix must be hosted entirely within RAM via optimized Redis hashes. Disk I/O interaction is strictly barred until the exact millisecond of Postgres state collapse.
* **Vector Execution Bound:** Cosine similarity evaluations must run within a bounded time horizon of **$\le 15\text{ms}$** per tenant transaction cluster to prevent processing delays in high-volume client accounts.
* **Hard Memory Eviction:** Any context node that fails to anchor to a physical financial settlement event within 7 calendar days must be compiled into an unallocated transaction bucket, stripped from active cache configurations, and preserved in cold relational storage to guarantee consistent memory performance.

Let’s strip away the technical jargon entirely. The link between **ASE** and **TSS** is simple: **ASE is the whole machine. TSS is a specific part inside that machine.**

---

### The Dead-Simple Analogy

* **ASE (Autonomous Semantic Engine) is the Car.** It is the actual engine block, the wheels, the chassis, and the dashboard. It is the entire system that listens to NATS, handles the memory inside AlloyDB Omni, spins up Go routines, and acts as the ultimate gatekeeper to the ledger.
* **TSS (Temporal Semantic Stitching) is the GPS Navigation System inside the car.** It is a single, specialized software program that runs *within* the ASE. Its only job is to solve one specific problem: **time-delayed data matching.**

---

### How They Talk to Each Other (Step-by-Step)

Look at how they work together when a Casablanca e-commerce client receives a bank deposit from a delivery courier:

1. **The ASE (The Car) wakes up:** A new transaction hits the bank feed on Thursday morning for 5,000 MAD. The ASE takes this transaction and says, *"I need to classify this, but the bank description is messy."*
2. **The ASE hands the task to TSS (The Navigation System):** The ASE switches on its internal **TSS protocol** and says, *"Go look at our memory buffer inside AlloyDB Omni from the last 7 days and find out what happened earlier this week that matches this money."*
3. **TSS does the math:** TSS runs a vector similarity search across the past week's data. It finds a Shopify invoice and a courier delivery log from Monday that equals exactly 5,000 MAD.
4. **TSS reports back to the ASE:** TSS says, *"I found the match. This Thursday bank deposit belongs to Monday's delivery log. The match confidence is 99%."*
5. **The ASE closes the loop:** The ASE takes that answer from TSS, sees that the confidence meets the guardrail requirement, clears the transaction out of temporary memory, and locks the clean data permanently into the accounting ledger.

---

### Summary

You don’t choose between ASE or TSS.

You build the **ASE** to be your core infrastructure platforms. Then, you write the **TSS** code inside it so your engine knows how to connect dots across time. Without the ASE, TSS has no engine to run on. Without TSS, the ASE wouldn't know how to match a Friday bank wire to a Monday receipt.
