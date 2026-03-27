# Toro OS Technical PRD: Workflow 1
## The E-Commerce Payout Reconciler (The "Zoom Killshot")

### 1. Executive Summary & The "Magic Trick"
* **Objective:** Automate the mathematical nightmare of reconciling bulk e-commerce payouts (Stripe/Shopify) against individual gross sales, gateway fees, and rolling reserves, matching them perfectly to a single bank deposit.
* **The Demo Value:** On a Zoom call, the CPA uploads a messy Stripe payout CSV and a QuickBooks bank feed deposit. In 250ms, Fignode UI displays a perfectly balanced, multi-line journal entry where *Gross Sales - Fees = Net Deposit* to the exact penny. 
* **Why it closes the deal:** LLMs cannot do exact accounting math. This workflow proves that Toro OS forces the AI into a deterministic mathematical straitjacket.

### 2. Architectural Definitions (The Toro Physics)
* **NATS JetStream (The Vault):** The immutable event bus. All state transitions are published here as events (e.g., `event.payout.uploaded`).
* **Workers (The Muscle):** Decoupled Go routines subscribed to specific NATS subjects. They react, process, and publish new events. They do *not* command each other.
* **Agents (The Brain):** LLMs wrapped in a specific Worker shell. They react to data-mapping events, reason about the data, and invoke Tools.
* **Tools (The Straitjacket):** Deterministic Go functions called by the Agent (e.g., `CalculateGatewayFees()`) to ensure no hallucinated arithmetic enters the stream.

### 3. The Event-Driven State Machine (Step-by-Step Flow)

**Phase A: Ingestion & Normalization**
1.  **Fignode UI (Action):** The CPA drags and drops the Stripe Payout CSV and selects the corresponding $14,230.55 Bank Deposit from the Plaid/QBO feed.
2.  **API Gateway Worker:** Receives the payload via WebSockets.
    * *Action:* Publishes event `event.payout.raw_ingested` to NATS with the binary payload attached.
3.  **Data Normalization Worker:** Listens for `event.payout.raw_ingested`. 
    * *Action:* Parses the CSV, extracts headers, and converts unstructured rows into a structured Go struct (e.g., Gross, Fee, Net, Transfer ID).
    * *Action:* Publishes event `event.payout.normalized` to NATS.

**Phase B: Agentic Reasoning & Tool Invocation**
4.  **Reconciliation Agent (Worker Wrapper):** Listens for `event.payout.normalized`. 
    * *Action:* The Agent analyzes the structured data. It identifies that it needs to split the lump sum into specific ledger accounts (Sales Revenue, Stripe Merchant Fees, Bank Account).
    * *Action:* The Agent invokes the **Tool:** `DraftJournalEntry(GrossArray, FeeArray, DepositAmount)`.
5.  **Tool Execution (Deterministic):** The Go tool executes the exact float64 arithmetic. It forces the accounting equation: $Assets = Liabilities + Equity$. If the sum of the gross minus fees does *not* equal the bank deposit down to the penny, the Tool throws an error. If it matches, the Tool generates a strict RFC 6902 JSON Patch.
6.  **Agent Output:** The Agent completes its cycle. 
    * *Action:* Publishes event `event.ledger.proposal_generated` to NATS, containing the verified JSON Patch.

**Phase C: The Human Failsafe & Commit**
7.  **WebSocket Sync Worker:** Listens for `event.ledger.proposal_generated`.
    * *Action:* Pushes the proposed, perfectly balanced Journal Entry to the Fignode React/Wails UI via WebSockets.
8.  **Fignode UI (Action):** The CPA sees the "Magic Trick" on screen. A messy CSV has been instantly transformed into a beautiful, multi-line double-entry ledger proposal. The CPA clicks "Approve."
9.  **API Gateway Worker:** Receives the approval.
    * *Action:* Publishes event `event.ledger.human_approved`.
10. **QBO Sync Worker:** Listens for `event.ledger.human_approved`.
    * *Action:* Executes the API call to Intuit QuickBooks to hard-commit the Journal Entry. 
    * *Action:* Publishes `event.ledger.sync_complete`.

### 4. Technical Constraints & Fallbacks

* **The Penny Anomaly (Rounding Errors):** If the `DraftJournalEntry` Tool detects a $0.01 mismatch due to gateway rounding, the Agent is *strictly forbidden* from creating a plug figure to hide it. 
    * *Fallback:* The Agent publishes `event.reconciliation.anomaly_detected`. The UI routes this to the Fignode Mobile App as a swipe-card for the CPA to manually apply the $0.01 variance to a "Reconciliation Discrepancy" account.
* **Worker Idempotency:** Every Worker must be idempotent. If a NATS message is delivered twice due to a network blip, the Worker checks the SQLite cache to ensure it doesn't process the same payout twice.

### 5. Development Milestones for this PRD
* **Milestone 1:** Build the `Data Normalization Worker` to parse Stripe/Shopify CSVs into standard Toro structs.
* **Milestone 2:** Build the `DraftJournalEntry` Go Tool (The deterministic calculator).
* **Milestone 3:** Wire the NATS JetStream subjects (`payout.raw`, `payout.normalized`, `ledger.proposal`, `ledger.approved`).
* **Milestone 4:** Build the Fignode UI component to ingest the CSV and instantly render the returned JSON patch as a visual ledger entry.

