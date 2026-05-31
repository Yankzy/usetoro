# PRODUCT REQUIREMENT DOCUMENT (PRD)

## Project: Autonomous Semantic Engine (ASE)

## 1. Executive Summary & Core Philosophy

### 1.1 The Paradigm Leap: Data as a Cognitive Runtime

Traditional enterprise architectures treat financial data as a passive noun and execution code as an active verb. Relational databases require absolute, deterministic parameters at the exact millisecond of a database write. Conversely, human economic activity, tracked via disparate bank feeds, statements, Slack channels, and invoices, is inherently asynchronous, fragmented, non-deterministic and unavoidably conversational. Traditional accounting automation tools fail because they attempt to force incomplete language strings instantly into rigid database schemas, resulting in structural system errors, processing gridlocks, or ledger hallucinations.

The **Autonomous Semantic Engine (ASE)** completely rejects the passive storage model. It converts financial ledger items into a decentralized, self-reconciling Directed Acyclic Graph (DAG) node network running natively within the Go runtime ecosystem.

Inside the ASE, an incoming bank transaction is not merely a static database row; it is a living, concurrent micro-agent (**Uses tap/agents/general_agent/agent.go**). The moment an incomplete financial event enters the network, it is instantiated as an independent Transaction Agent. Its mandate is clear: triage its own missing context, traverse the DAG Nodes, interface autonomously with the virtual workforce over the NATS event mesh, reduce its internal uncertainty to zero, continuously update its state in the `fignode.staging_transactions` holding table, and safely collapse itself into the permanent QBO ledger.

---

## 2. Core Technical Stack Architecture

The ASE Node Network requires an architecture that provides microsecond in-memory state manipulation, zero-latency pub/sub event routing, and an absolute ACID-compliant relational core. The ASE achieves this through a Tri-Tier Component Matrix:

### 2.1 The Infrastructure Matrix

| Infrastructure Layer | Component Technology | Operational Mandate inside the ASE Network |
| --- | --- | --- |
| **Ingress Event Log** | NATS JetStream | Captures raw conversational events, CSV statement uploads, and webhook streams asynchronously. Acts as the immutable, append-only timeline that can be replayed to reconstruct node state histories. |
| **Active Memory & DAG Layer** | Redis Stack (In-Memory) | Hosts the ephemeral `ASENode` graph maps, active node state indicators, conditional execution locks, and cross-node telemetry links. High-throughput allows native Go actors to read and mutate state instantly. |
| **Semantic Proximity Vector DB** | Postgres + `pgvector` | Houses vector embeddings of historical enterprise communication contexts, account category schemas, and client-specific entity definitions right next to relational data. |
| **Staging Holding Table** | Postgres (`fignode.staging_transactions`) | The persistent holding area. While nodes exist in active memory, they continuously write their state updates, entropy reductions, and classification results back to this table. |
| **Deterministic Base Ledger** | QuickBooks Online (via Sync) | The final destination. An airtight, ACID-compliant double-entry accounting matrix. No agentic flux or loose probabilistic keys are allowed past this gate. |

---

## 3. Technical Specifications & Mathematical Modeling

### 3.1 The ASE Accounting Node Structure

Every transaction ingestion event generates an isolated, stateful memory object in the Redis layer, wrapped by an active Goroutine execution loop inside the ASE. Instead of a concrete data value, missing parameters are handled as discrete arrays of **Probabilistic Candidates** tracked by the active node as it traverses the accounting DAG.

```go
package ase

import (
	"time"
)

type ProbabilityCandidate struct {
	Value       string    `json:"value"`        // e.g., "Account 6100 - Travel Expenses"
	Confidence  float64   `json:"confidence"`   // Mathematical weight between 0.00 and 1.00
	SourceEvent string    `json:"source_event"` // Specific NATS message UUID introducing context
}

type AutonomousSemanticEngineNode struct {
	NodeID          string                          `json:"node_id"`
	TenantID        string                          `json:"tenant_id"`
	SourceStatement string                          `json:"source_statement"` // e.g., "Checking_May2026.csv"
	RawDescription  string                          `json:"raw_description"`
	CashDirection   string                          `json:"cash_direction"`   // INFLOW or OUTFLOW
	CurrentEntropy  float64                         `json:"current_entropy"`
	CurrentState    string                          `json:"current_state"`    // TRIAGE, THINK, HOLD, COLLAPSED
	HoldReason      string                          `json:"hold_reason,omitempty"` 
	Candidates      map[string][]ProbabilityCandidate `json:"candidates"`    // Keys: "macro_class", "account_type", "resolved_account_id", "counterparty"
	LifetimeProbes  int                             `json:"lifetime_probes"`
	CreatedAt       time.Time                       `json:"created_at"`
	UpdatedAt       time.Time                       `json:"updated_at"`
}

```

### 3.2 Mathematical Modeling of Accounting Entropy

An active ASE Node continuously calculates its own internal uncertainty using an application of Shannon Entropy. For any given transactional parameter $X$ (such as the target general ledger code), the node evaluates its internal entropy $H(X)$:

$$H(X) = -\sum_{i=1}^{n} P(x_i) \log_2 P(x_i)$$

Where $P(x_i)$ represents the current weight score of candidate inference $x_i$.

The unified Node Confidence Score ($C$) governing the individual micro-agent is modeled as:

$$C = 1 - \frac{\sum_{k \in \text{properties}} H(k)}{\text{Total System Properties Required}}$$

### 3.3 The Hard Commit Guardrail

An individual `AutonomousSemanticEngineNode` continuously updates `fignode.staging_transactions` with its progress. However, it is structurally barred from flagging itself as `READY_FOR_SYNC` (to push to QuickBooks Online) until its internal confidence satisfies the State Collapse Condition:

$$C \geq 0.98$$

---

## 4. The Accounting DAG Branching Tree Topology

When a transaction is ingested, it is instantiated as a root node and flows down a strictly structured Directed Acyclic Graph based on standard QuickBooks Online Chart of Accounts (CoA) rules, embedded with stateful interception locks.

```
                       [Root: Macro Classifier Node]
                                     │
     ┌───────────────┬───────────────┼───────────────┬───────────────┐
     ▼               ▼               ▼               ▼               ▼
[1.0 Asset]   [2.0 Liability]  [3.0 Equity]    [4.0 Income]    [5.0 Expense]

```

### 4.1 Branch 1.0: Asset Node Matrix (`ase.node.asset`)

* **1.1 QBO Account Type: Bank Accounts**
* *1.1.1 Sub-Node:* Checking Accounts $\rightarrow$ `STP_PATHWAY` (Immediate collapse if operational).
* *1.1.2 Sub-Node:* Savings Accounts $\rightarrow$ `STP_PATHWAY`.
* *1.1.3 Sub-Node:* Inter-Account Clearing $\rightarrow$ **[HOLD: Transfer Match Validation Node]** (Pauses to verify matching internal account transfers across distinct CSV sessions).


* **1.2 QBO Account Type: Accounts Receivable (AR)**
* *1.2.1 Sub-Node:* Open Invoices $\rightarrow$ **[HOLD: Revenue Match Node]** (Intercepts bank deposits, blocks generic cash-inflow deposit hydration, routes to AR Specialist to clear open ledger invoices).
* *1.2.2 Sub-Node:* Unapplied Cash $\rightarrow$ Escalates to conversational slot-filling protocol.


* **1.3 QBO Account Type: Other Current Assets**
* *1.3.1 Sub-Node:* Prepaid Expenses $\rightarrow$ **[GATE: Prepaid Expense Specialist Node]** (Constructs monthly operational amortization burn-down timelines).


* **1.4 QBO Account Type: Fixed Assets**
* *1.4.1 Sub-Node:* Machinery & Equipment $\rightarrow$ **[GATE: Fixed Asset Depreciation Node]** (Triggers capitalization threshold validation; if $\geq \$2,500$, instantiates a depreciation schedule).
* *1.4.2 Sub-Node:* Corporate Vehicles $\rightarrow$ **[GATE: Fixed Asset Depreciation Node]**.


* **1.5 QBO Account Type: Other Assets**
* *1.5.1 Sub-Node:* Security Deposits $\rightarrow$ Capital retention hold tracking.



### 4.2 Branch 2.0: Liability Node Matrix (`ase.node.liability`)

* **2.1 QBO Account Type: Credit Card**
* *2.1.1 Sub-Node:* Credit Card Balances $\rightarrow$ **[QUARANTINE: Statement Dependency Hold Node]** * *Condition:* Intercepts any checking account outflow marking a credit card bill payment or any direct statement card inflow.
* *Action:* Freezes state collapse, marks state as `TRANSFER_HOLD`, and signals the document-chasing layer to request the mirror statement.




* **2.2 QBO Account Type: Accounts Payable (AP)**
* *2.2.1 Sub-Node:* Vendor Bills $\rightarrow$ **[HOLD: Bill Matching Node]** (Forces validation matching against incoming PDF/OCR bills processed by the AP Specialist).


* **2.3 QBO Account Type: Other Current Liabilities**
* *2.3.1 Sub-Node:* Payroll Liabilities $\rightarrow$ **[GATE: Payroll Allocator Node]** (Executes multi-line journal splits for withholdings and gross pay allocations).
* *2.3.2 Sub-Node:* Sales Tax Payable $\rightarrow$ Routes to Tax Nexus Gatekeeper.
* *2.3.3 Sub-Node:* Accrued Liabilities $\rightarrow$ Routes to Accruals Specialist.


* **2.4 QBO Account Type: Long-Term Liabilities**
* *2.4.1 Sub-Node:* Notes Payable / Bank Loans $\rightarrow$ Splits principal payoffs from interest expenses.



### 4.3 Branch 3.0: Equity Node Matrix (`ase.node.equity`)

* **3.1 QBO Account Type: Equity**
* *3.1.1 Sub-Node:* Owner's Investment / Capital $\rightarrow$ `STP_PATHWAY` (Validates external funding inflows).
* *3.1.2 Sub-Node:* Owner's Draw / Personal Spending $\rightarrow$ **[RE-ROUTE: Commingling Intercept Node]** (Intercepts personal expenses mistaken for business lines, routing them to the Balance Sheet to protect P&L fidelity).
* *3.1.3 Sub-Node:* Retained Earnings $\rightarrow$ **[HARD COMPLIANCE GUARD]** (Direct posting strictly forbidden by API security guidelines. Instantly triggers a Probability Inversion Violation).



### 4.4 Branch 4.0: Income / Revenue Node Matrix (`ase.node.income`)

* **4.1 QBO Account Type: Income**
* *4.1.1 Sub-Node:* Gross Sales / Service Revenue $\rightarrow$ Standard operational inflow routing.
* *4.1.2 Sub-Node:* Merchant Payout Distributions $\rightarrow$ **[SPLIT: Merchant Fee Extraction Node]** (Unpacks incoming Stripe/Square cash pools, separating net sales from card processing expenses).


* **4.2 QBO Account Type: Other Income**
* *4.2.1 Sub-Node:* Interest Income $\rightarrow$ Non-operating revenue collection.



### 4.5 Branch 5.0: Expense Node Matrix (`ase.node.expense`)

* **5.1 QBO Account Type: Cost of Goods Sold (COGS)**
* *5.1.1 Sub-Node:* Direct Material Purchases $\rightarrow$ Standard operational inventory depletion tracking.


* **5.2 QBO Account Type: Expense**
* *5.2.1 Sub-Node:* Operational Overheads (Rent, Utilities, Software SaaS) $\rightarrow$ Accelerated straight-through paths.
* *5.2.2 Sub-Node:* Cryptic Contractor Payouts $\rightarrow$ **[HOLD: Identity Verification Node]** (Triggers when peer-to-peer transfers lack structured metadata, freezing progress until the owner provides matching vendor parameters).


* **5.3 QBO Account Type: Other Expense**
* *5.3.1 Sub-Node:* Non-operating Losses / Penalties.



---

## 5. The ASE Node Lifecycle Workflow

Every transactional node functions as a concurrent Go routine utilizing native channels to cycle through four distinct operational phases within the ASE runtime environment:

```
[Event Ingest] ──► (Phase 1: Triage) ──► (Phase 2: Think) ──► (Phase 3: Activate) ──► [Phase 4: Collapse]

```

### Phase 1: Triage (Self-Assessment)

Upon instantiation via statement ingestion, the node reads its own raw values, normalizes the **Bank Sign Convention** into logical book values (+ for inflows, - for outflows), and catalogs its missing properties required for complete double-entry verification.

### Phase 2: Think (Semantic Proximity & Batch Classification)

When multiple transaction micro-agents arrive at the same DAG Node (e.g., the Macro Classifier Node), they can be batched together for efficiency. The DAG Node leverages the existing Temporal activities (e.g., `workers.classification_stage` from the bookkeeping DAG) to evaluate all transactions currently in its state. The DAG Node sends the batch to the LLM worker, which applies the configured system prompts. The individual transaction agents then parse their specific responses, recalculate their internal entropy, and update their row in `fignode.staging_transactions`. If structural requirements are still missing, the transaction sets its state to `HOLD` and defines its exact missing slots.

### Phase 3: Activate (Real-World Interfacing & Probing)

Because the node confidence falls below the $C \geq 0.98$ threshold, the node publishes an event to the NATS fabric. This awakens the specialized decentralized virtual workforce to scrape emails, check file attachments, or prompt users over chat channels to fill the missing slot.

### Phase 4: State Collapse (The Hard Commit)

When the missing context is received via NATS, the transaction agent consumes the data, processes it through the validation criteria, and recalculates its system entropy. Once $C \geq 0.98$, the agent performs a final update to `fignode.staging_transactions`, marking itself as completely qualified. It terminates its Go runtime state, evicts itself from the Redis cache layer, and hands its record off to the QBO Sync Worker for the final push into the permanent QuickBooks ledger.

---

## 6. Decentralized Virtual Workforce Orchestration

To run this decentralized network in an asynchronous manner, the ASE utilizes a stateless configuration map of specialized virtual micro-agents operating over NATS JetStream queues.

```yaml
# Virtual Employees Config (Stateless email/system prompt aliases)
see the Virtual Employees in `defaults.yml`.

```

---

## 7. Cross-Node Telemetry & Peer-to-Peer Reconciliation

Because every transaction node functions as an independent, intelligent Goroutine inside the ASE DAG framework, nodes can securely collaborate across the event-mesh network to resolve compound financial puzzles without requiring a centralized, high-overhead control coordinator.

Nodes continuously broadcast non-sensitive metadata summaries to a localized NATS telemetry channel (`ase.telemetry.*`).

```
[Node A: Active Wire Advance Node] ─── Sends Structural Telemetry ───► [NATS JetStream Mesh]
                                                                              │
                                                                              ▼
[Node B: Floating Invoice PDF Node] ─── Verifies Matching State ◄─────────────┘
                                               │
                                               ▼
                              [Joint Atomic Database Commit]

```

### 7.1 Real-World Employee Reimbursement Use Case

* **The Ingestion Event:** A checking account CSV session ingests an unrecognized bank outflow string: `-$450.00 | Desc: "ONLINE TRANSFER TO 998271"`.
* **The Node Action:** The instantiated transaction agent moves to a `POTENTIAL_TRANSFER` branch and hits an `AMBIGUOUS_HOLD` state. It locks itself in Redis, updates the `fignode.staging_transactions` status, blocks QBO synchronization, and triggers an event for `sarah` to ping the user.
* **The Conversational Injection:** The owner replies via chat: *"That was a Venmo payback to Sarah, our project lead, reimbursing her out-of-pocket spending for the team's flights."*
* **The Telemetry Merge & Collapse:** The agent catches the conversation event via NATS, maps "Sarah" to the internal employee directory, and shifts its DAG routing matrix directly into Branch **5.2.2 (Cryptic Contractor/Employee Disbursements)**. It updates its target to **Travel Expenses Account ID** in the staging table, passes the compliance threshold check ($C = 0.991$), terminates its active Goroutine loop, and queues itself for sync to the permanent QBO ledger.

---

## 8. System Failure Modes & Operational Governance

### 8.1 The Human-in-the-Loop (HITL) Routing Threshold

If an ASE node is locked in a high-entropy state ($C < 0.80$) and fails to gather clarifying information or matching statement documentation via automated probing within 48 hours, it must enforce a protective execution boundary to prevent pipeline memory gridlocks.

* **The Action:** The node halts its autonomous polling loops, archives its historical conversational logs from Redis, updates its state signature to `EXCEPTION_QUEUE`, and terminates its active processing loop.
* **The Destination:** The node securely presents its state map on the Moroccan Exception-Pilot Operational Dashboard. A trained human operator reviews the timeline via a high-speed interface, resolves the conflict with a manual override, and pushes the finalized data to the hard Postgres commit.

### 8.2 Dynamic Conflict Governance

If a user inputs contradictory data across channels (e.g., classifying an expense as "Client Meals" via a Slack message, but uploading an invoice explicitly marked as "Software Training" via email), the node registers a **Probability Inversion Violation**.

* **Conflict Action Protocol:** The node instantly resets all its internal confidence weights to 0.00, freezes its automated worker loops, blocks any further self-directed channel executions, and routes its entire history graph directly to the human exception queue to preserve financial fidelity.