# Toro OS Technical PRD: Workflow 3
## The "Shoebox" Historical Rescue (High-Velocity UI & Bulk Execution)

### 1. Executive Summary & The "Time-to-Value" Demo
* **Objective:** Ingest massive historical datasets (thousands of neglected transactions across 12-18 months), automatically categorize the high-confidence deterministic patterns, and route the ambiguous long-tail to a high-speed, swipe-based mobile UI for the human operator.
* **The Demo Value:** You upload a monstrous, 2,000-row bank statement CSV covering an entire year. In seconds, Toro OS auto-categorizes 1,600 of them (the recurring SaaS subscriptions, payroll, utilities) with 100% mathematical certainty. The remaining 400 anomalies instantly pop up on the CPA's Fignode Mobile App. You share your iPhone screen on the Zoom call and clear 50 transactions in 60 seconds using the Tinder-style swipe UI. 
* **Why it closes the deal:** It proves that your architecture bridges the gap between backend compute scale and human-in-the-loop velocity. You don't just find the anomalies; you build the fastest possible UX to resolve them. 

### 2. Architectural Definitions (The Toro Physics)
* **The Pattern Matcher (Deterministic Tool):** A Go routine that doesn't use AI. It uses strict regex and historical NATS ledger data (e.g., "If Vendor exactly matches 'Gusto', and Amount is between $1k-$5k, it is Payroll"). This is infinitely faster and cheaper than an LLM.
* **The Anomaly Queue (The Deck):** A specific NATS JetStream subject (`queue.triage.historical`) that holds transactions the Pattern Matcher and the AI Agent failed to categorize with high confidence. 
* **The Mobile Failsafe (React Native Reanimated):** The frontend UI designed specifically for low-friction, high-velocity human decision making, pre-fetching the next 10 NATS events to ensure zero perceived latency during swipes.

### 3. The Bulk Execution Machine (Step-by-Step Flow)

**Phase A: Mass Ingestion & Deterministic Triage**
1. **Fignode Pro Desktop (Action):** The CPA drops a 2,000-row CSV or a massive Plaid historical sync into the desktop client.
2. **Bulk Ingest Worker:** Receives the payload.
   * *Action:* Slices the CSV into individual JSON structs.
   * *Action:* Publishes 2,000 individual `event.transaction.historical_ingested` messages to NATS in a high-throughput burst.
3. **Pattern Matching Worker (The Sieve):** Subscribes to the ingestion stream. 
   * *Action:* Evaluates every transaction against strict deterministic rules and historical ledger memory. 
   * *Result:* 1,600 transactions hit 100% confidence matches (e.g., AWS, Gusto, WeWork). 
   * *Action:* Publishes 1,600 `event.ledger.proposal_generated` messages directly to the commit queue. 
   * *Result:* The remaining 400 transactions fail the deterministic check (e.g., a $4,000 charge to "Stripe" that could be a software tool or a contractor payout). 
   * *Action:* Publishes 400 `event.transaction.anomaly_detected` messages.

**Phase B: The AI Fallback (The Second Sieve)**
4. **The LLM Agent (Worker Wrapper):** Listens for `event.transaction.anomaly_detected`.
   * *Action:* The Agent attempts probabilistic reasoning on the 400 anomalies. 
   * *Result:* It successfully reasons through 200 of them based on website context or surrounding transactions. 
   * *Action:* Publishes 200 `event.ledger.proposal_generated` messages.
   * *Result:* The final 200 transactions are too ambiguous. The Agent's confidence score drops below the threshold. 
   * *Action:* Publishes 200 `event.triage.human_required` messages.

**Phase C: The High-Velocity Human Failsafe**
5. **The UI Pre-Fetch Worker:** Listens for `event.triage.human_required`.
   * *Action:* Pushes the 200 transactions via WebSockets to the CPA's Fignode Mobile App, storing them in the local SQLite cache to ensure the UI doesn't lag while swiping.
6. **Fignode Mobile App (Action):** The CPA opens the "Shoebox Triage" deck. 
   * *Action:* The screen shows: "Stripe - $4,000". The UI suggests three likely categories based on the Chart of Accounts. 
   * *Action:* The CPA taps "Contractor Expense" and swipes right to approve. 
7. **The Sync Worker:** Receives the swipe payload via WebSockets.
   * *Action:* Publishes `event.ledger.human_approved` back to NATS. 
   * *Action:* The NATS sequence immediately loads the next card on the mobile app in under 10ms.

### 4. Technical Constraints & Fallbacks
* **Rate Limiting & Backpressure:** If a CPA uploads 50,000 transactions, NATS will handle it, but the QBO Sync Worker might hit Intuit's API rate limits (e.g., 500 requests per minute). 
    * *Fallback:* The QBO Worker implements a Token Bucket algorithm or reads from a NATS Consumer with a strict `MaxDeliver` rate, naturally buffering the sync without dropping data.
* **Offline UI Degradation:** If the CPA is swiping on a train and loses cell service, the app must not freeze.
    * *Fallback:* Fignode Mobile writes the swipe actions (`event.ledger.human_approved`) to the local device SQLite. When the WebSocket reconnects, it flushes the backlog to the NATS API Gateway. 

### 5. Development Milestones for this PRD
* **Milestone 1:** Build the Deterministic Pattern Matching Go Tool (The Sieve) to bypass LLM costs for obvious transactions.
* **Milestone 2:** Implement NATS JetStream Consumer backpressure to protect downstream API rate limits (QuickBooks).
* **Milestone 3:** Finalize the React Native `reanimated` swipe-deck UI, optimizing for pre-fetching and zero-latency card transitions.
* **Milestone 4:** Build the Offline-First SQLite sync layer on the mobile client.
