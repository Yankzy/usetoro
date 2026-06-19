# Product Requirement Document (PRD)

## Project: Autonomous Semantic Bookkeeping Engine (Go Core)

**Infrastructure Stack:** Go 1.26+, NATS JetStream, AlloyDB Omni, Stripe Financial Connections

**Core Processing Paradigm:** Localized Graph Scheduling via AI-Sensor Pairing (**Zero BFS/DFS Overhead**)

---

## 1. Executive Summary & Strategic Two-Front Positioning

The platform completely separates its market presence into two distinct, sovereign web properties to capture both high-volume transactional retail users and premium enterprise contract revenue without brand dilution.

Both properties route traffic to a unified Go backend processing engine, executing isolated financial workflows based on request headers.

```
 nodebookkeeping.com  ──► [ X-Brand: Retail ]  ──┐
                                                 ├──► [ Unified API Routing Gateway ]
 fignode.com          ──► [ X-Brand: Fignode ] ──┘

```

### 1.1 nodebookkeeping.com (The Transactional Retail Utility)

* **Target Audience:** Freelancers, creator-economy participants, micro-SMBs, and gig workers.
* **Value Proposition:** An instant, on-demand financial data machine. Users drag and drop a folder of messy receipt images and bank statement CSV files to get a pristine, mathematically balanced General Ledger summary in 3 seconds for **$10 per run**.
* **Operational Mode:** 100% automated software loop. Zero human exception-pilots. Edge cases are blocked via software gates and used as an upgrade hook.

### 1.2 fignode.com (The Institutional CPA Citadel)

* **Target Audience:** Certified Public Accountants (CPAs) and professional accounting firms.
* **Value Proposition:** A multi-tenant, high-throughput client management workspace commanding a premium **$2,000 per month flat-rate license**.
* **Operational Mode:** Continuous real-time background processing via automated bank webhooks. System anomalies are surfaced directly to the CPA's native dashboard, positioning the licensed accountant as the ultimate high-tier verification referee.

---

## 2. Decoupled Micro-ASE Architecture

The engine splits the transactional bookkeeping lifecycle into three sovereign, asynchronous micro-engines communicating over a NATS JetStream mesh. This eliminates database row-locking and guarantees absolute data isolation.

* **The AP-ASE (Accounts Payable Engine):** Listens to NATS subject `ase.ingress.ap.*`. Ingests, normalizes, and extracts data from email inbox invoice PDFs, uploaded receipt files, and vendor billing sheets to establish active liabilities.
* **The AR-ASE (Accounts Receivable Engine):** Listens to NATS subject `ase.ingress.ar.*`. Tracks company milestone telemetry, auto-generates commercial invoices, logs accrued assets, and manages polite automated collections loops.
* **The GL-ASE (General Ledger & Reconciliation Referee):** Listens to NATS subject `ase.ingress.gl.*`. Sits entirely blind to raw, external unstructured conversations. Ingests live background bank data feeds exclusively via **Stripe Financial Connections webhooks** (`status = 'posted'`) and executes the final state-collapse balancing entries into the ledger.

---

## 3. The Ingestion Pipeline: AI Sensor Layer & Local Topological Sort

Because transaction matching inside an individual accounting workflow represents a tiny data footprint—typically containing only **2 to 5 vertices** per transaction family (e.g., an invoice, a manager approval, and a cash clearing event)—the computing overhead of global graph traversals like Breadth-First Search (BFS) or Depth-First Search (DFS) is entirely eliminated.

Instead, the core matching pipeline utilizes a lightweight "Tag-Team" loop: the **AI Sensor Layer (The Brain)** discovers the structural dependencies, and a **Local Topological Sort Engine (The Conductor)** schedules the physical execution order inside the database.

```
 [Messy Ingress Webhook/File] 
               │
               ▼
 AI Sensor Layer (The Brain) ──► Classifies text, maps invariants, draws dependency arrows.
               │
               ▼
 Local Topo Sort (The Conductor) ──► Counts local In-Degrees (2-5 nodes), enforces exact execution order.

```

### 3.1 Component A: The AI Sensor Layer (Dependency Generation)

When a financial object lands via NATS, an LLM sensor evaluates the text payload. It isolates unalterable structural invariants—**The Exact Absolute Amount** and **The Normalized Vendor Token** (mapped to a standard Entity ID inside AlloyDB Omni).

The AI does not write data to the ledger. Its sole task is to analyze the context and draw causal dependency arrows. For example, it looks at a Stripe bank clearing event and states: *"This is a cash outflow. I identify this as a `Bank_Payment` that mathematically depends on an open `Vendor_Invoice` node. I am drawing a directed edge pointing from the Invoice to the Payment."*

### 3.2 Component B: The Local Topological Sort Engine (Runtime Execution Scheduling)

The Go engine groups incoming items into highly isolated local clusters matching the identical Vendor ID and Amount within a tight 7-day window. It reads the local dependency arrows drawn by the AI layer and computes an in-memory `InDegree` count for just those 2 to 5 nodes.

Using **Kahn’s Algorithm**, the engine enforces a non-breaking bookkeeping execution sequence:

1. Any node within the local cluster possessing an `InDegree = 0` (the source invoice or bill) enters the active execution queue first.
2. The engine pops the source node, writes its balanced entries to the ledger, and decrements the `InDegree` of its dependent child node.
3. The dependent child node (the Stripe payment) drops to an `InDegree = 0` and is safely executed next, closing the liability out to absolute zero.

```go
type TransactionNode struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`          // "INVOICE", "PAYMENT", "APPROVAL"
	InDegree     int       `json:"in_degree"`     // Calculated dynamically based on AI-declared dependencies
	DependsOnIDs []string  `json:"depends_on_ids"` // Array generated by the AI Sensor Layer
	Amount       float64   `json:"amount"`
	VendorID     string    `json:"vendor_id"`
}

func (engine *ASEEngine) ProcessLocalCluster(cluster map[string]*TransactionNode) ([]*TransactionNode, error) {
	var executionQueue []*TransactionNode
	var finalizedSequence []*TransactionNode

	// Calculate initial local in-degrees based on AI dependency mapping
	for _, node := range cluster {
		for _, parentID := range node.DependsOnIDs {
			// If the required parent node is missing from the active batch or DB state, increment InDegree
			if _, exists := cluster[parentID]; !exists && !engine.CheckLedgerForNode(parentID) {
				node.InDegree++
			}
		}
		if node.InDegree == 0 {
			executionQueue = append(executionQueue, node)
		}
	}

	// Kahn's Algorithm Loop for local 2-5 node sequencing
	for len(executionQueue) > 0 {
		current := executionQueue[0]
		executionQueue = executionQueue[1:]
		finalizedSequence = append(finalizedSequence, current)

		for _, childID := range engine.GetLocalRegisteredChildren(current.ID, cluster) {
			childNode := cluster[childID]
			childNode.InDegree--
			if childNode.InDegree == 0 {
				executionQueue = append(executionQueue, childNode)
			}
		}
	}

	// Safety check: verify no cyclic data loops exist
	if len(finalizedSequence) != len(cluster) {
		return nil, errors.New("local ledger dependency loop blocked at engine boundary")
	}

	return finalizedSequence, nil
}

```

---

## 4. The Non-Negotiable Core: The Shadow General Ledger (SGL)

The engine cannot store unstructured "matches" or unallocated file pairs. Every single system run—including a temporary, single-use $10 file drop on `nodebookkeeping.com`—must instantly instantiate and populate a compliant, double-entry **Shadow General Ledger (SGL)** before exporting or rendering data.

### 4.1 The Database Schema

All financial workspaces are partitioned inside a relational core utilizing AlloyDB Omni. Strict isolation is maintained via the QuickBooks Online `realmId` or a temporary anonymous `session_id`.

```sql
CREATE TABLE shadow_general_ledger (
    entry_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    qbo_realm_id VARCHAR(50) NOT NULL,           -- Primary tenant namespace partition (fignode.com)
    session_id UUID,                             -- Populated exclusively for nodebookkeeping.com sessions
    account_code VARCHAR(10) NOT NULL,           -- US GAAP Standard Account Codes (e.g., '1010', '6010')
    debit NUMERIC(14, 2) DEFAULT 0.00,
    credit NUMERIC(14, 2) DEFAULT 0.00,
    transaction_timestamp TIMESTAMP WITH TIME ZONE NOT NULL,
    description TEXT NOT NULL,
    
    -- Absolute Double-Entry Enforcer Rule
    CONSTRAINT chk_double_entry_balance CHECK (
        (debit > 0.00 AND credit = 0.00) OR (credit > 0.00 AND debit = 0.00)
    )
);

-- B-Tree composite index optimized for Bitmap-Assisted Inline Filtering
CREATE INDEX idx_sgl_tenant_partition_tree 
ON shadow_general_ledger (qbo_realm_id, session_id, account_code);

```

### 4.2 Standard US GAAP Account Blueprint

When an execution pipeline commits to the SGL, it automatically maps transactions across a standardized Chart of Accounts template:

* **1010 (Operating Bank Cash):** Credited for banking outflows, debited for revenue inflows via Stripe feeds.
* **2010 (Accounts Payable):** Leveraged by the AP-ASE to record incoming vendor liabilities before final settlement.
* **6010 (Software & SaaS Expenses):** Target debit account for technology and subscription charges.
* **6020 (Travel & Entertainment):** Target debit account for transit, logistics, and corporate meals.

---

## 5. Brand-Specific Execution & Margin Protection Guardrails

```
                  [User Input Data Flow]
                            │
                            ▼
               [Local Topo Sort Evaluation]
                            │
               ┌────────────┴────────────┐
               ▼                         ▼
         InDegree = 0               InDegree > 0
               │                         │
               ▼                         ▼
   [Atomic Write to SGL]       [Self-Service Tier] ──► Block Human Desk
               │                         │             Display Up-Sell Prompt
               ▼                         ▼
     [Render Trial Balance]    [Enterprise Headcount] ──► Route to CPA Dashboard

```

### 5.1 nodebookkeeping.com Ruleset

* **Context Gaps:** If a user drops a Stripe bank statement showing a $1,200 payment, but the local topological sort engine flags an `InDegree > 0` because the matching invoice node is physically missing from the file batch, the transaction enters a `Context Gap` status.
* **The Software Gate:** **The data is completely air-gapped from any human review.** The engine freezes the execution queue for that specific cluster. The front-end UI highlights the unmatched line item as an unclassified element and generates a contextual upgrade prompt: *"We found a cash transaction that requires external context. Add the invoice manually, or upgrade to a Fignode-powered accounting partner to automate this."*
* **The Deliverable:** For all successfully completed local clusters, the SGL generates a pristine, web-rendered Trial Balance and Income Statement directly onto the user's browser, requiring a $10 payment to unlock the clean file download.

### 5.2 fignode.com Ruleset

* **Persistent Ingress:** Background workers continuously process streaming **Stripe Financial Connections** data lines against the CPA's permanently linked client accounts.
* **The CPA Referee Loop:** If the local topological sort engine identifies a `Context Gap` (e.g., a missing receipt or ambiguous vendor token stretching past a 72-hour aging window), the software does not query an internal exception desk. It packages the anomalous transaction payload into a clean JSON notification object and routes it straight to the CPA's native dashboard view.
* **The Interface Deliverable:** The CPA clicks the alert, views a set of pre-compiled smart account-mapping suggestions generated by the database, selects the correct code, and confirms the entry. The system drops the dependency count to zero, executes the atomic state collapse inside the SGL, and updates the client's production QuickBooks Online profile instantly.