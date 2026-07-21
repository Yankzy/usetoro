# Product Requirement Document (PRD)

## Module: Autonomous Cognitive Enrichment Engine (CEE)

## 1. System Overview & Objective

The Cognitive Enrichment Engine (CEE) is the core financial intelligence layer of our platform. It transforms unstructured, messy, multi-source transactional data inputs (CSV, Excel, PDF, Live Streams) into highly enriched, deterministic, compliance-ready financial objects for accounting systems like QuickBooks Online (QBO).

By combining deterministic string trees, time-series multi-account matching, and an information-theoretic LLM processing DAG, the CEE completely bypasses the need for external financial enrichment tools.

---

## 2. Infrastructure & Storage Architecture

The CEE utilizes a tiered, memory-to-disk architecture designed to minimize latency, eliminate redundant LLM compute, and ensure absolute reproducibility.

### 2.1 The Cache & Relational Layer

* **L1 In-Memory Cache (Redis):** Stores exact-string mappings. Keys are structured as `cee:raw:[Sanitized_Descriptor_Hash]`. Returns deterministic JSON metadata under **2ms**.
* **L2 Relational Master Ledger (PostgreSQL):** Houses the relational tables for master entities, structural regex rules, and transactional context. Utilizes the `pg_trgm` extension for trigram fuzzy indexing.

### 2.2 Database Schema Definitions

```go
package schema

import "time"

type AccountHolderType string
const (
	HolderConsumer AccountHolderType = "CONSUMER"
	HolderBusiness AccountHolderType = "BUSINESS"
)

type MacroClass string
const (
	ClassAsset     MacroClass = "ASSET"
	ClassLiability MacroClass = "LIABILITY"
	ClassRevenue   MacroClass = "REVENUE"
	ClassExpense   MacroClass = "EXPENSE"
	ClassEquity    MacroClass = "EQUITY"
	ClassTransfer  MacroClass = "TRANSFER"
)

// MasterMerchant stores the golden record of global/cached entities
type MasterMerchant struct {
	ID                 int64      `db:"id"`
	NormalizedName     string     `db:"normalized_name"`     // e.g., "Amazon Web Services"
	PrimaryDomain      string     `db:"primary_domain"`      // e.g., "aws.amazon.com"
	LogoURL            string     `db:"logo_url"`            // CDN asset pointer
	MCC                int        `db:"mcc"`                 // Merchant Category Code
	NAICS              string     `db:"naics"`               // Official industry tax mapping
	DefaultMacroClass  MacroClass `db:"default_macro_class"` 
	DefaultQBOCategory string     `db:"default_qbo_category"`
	IRSReciptThreshold float64    `db:"irs_receipt_threshold"` // Default $75.00 compliance flag
}

// MasterPattern maps raw bank variations back to a MasterMerchant
type MasterPattern struct {
	ID               int64  `db:"id"`
	CleanedStem      string `db:"cleaned_stem"`       // e.g., "AWS AMAZON"
	MasterMerchantID int64  `db:"master_merchant_id"` // Foreign Key to MasterMerchant
	IsIntermediary   bool   `db:"is_intermediary"`   // Identifies if it's Stripe, Toast, Square, etc.
}

```

---

## 3. Step-by-Step Data Processing Pipeline

```
[File Ingest] ➔ Phase 1: Structural DAG ➔ Phase 2: Core Sanitizer ➔ Phase 3: Match Matrix 
                                                                          │
       ┌─────────────────────────────── Cache Hit ────────────────────────┘
       ▼
Phase 4: Multi-Account Sweep Gate ➔ Phase 5: Deterministic Post-Enrichment ➔ [QBO Engine]
       ▼
   Cache Miss
       ▼
Phase 6: Cognitive LLM Node (Shannon Entropy Gate)
       ├── Low Entropy  ➔ Write to L1/L2 Cache ➔ Proceed to Phase 4
       └── High Entropy ➔ Execute Hold State   ➔ Dispatch Hound Agent (Sarah the virtual employee)

```

### Phase 1: Structural Layout Parsing (The Ingestion Node)

* **Input:** Multi-format file payloads (CSV, XLS, PDF).
* **Execution:** The engine extracts the raw layout boundary. It isolates the first **20 lines** (including headers) and dispatches them to a lightweight layout LLM with our rigid schema constraint.
* **Output:** Generates a deterministic column-mapping index (`MapIndex[0] = Date`, `MapIndex[1] = Description`, etc.) and determines the credit/debit numerical orientation of the file.

### Phase 2: Core String Sanitization

Before running matching routines, raw text inputs are stripped of processing artifacts, dates, locations, and card indicators via compiled Go Regex routines:

```go
var noiseRegex = regexp.MustCompile(`(?i)([\d]{4,})|(\b[A-Z]{2}\b)|(SQ\s*\*)|(AMZN\*)|(PAYPAL\s*\*)|(TST\*)|(\bUS\b)|(CARD\s*\d+)`)

func SanitizeDescriptor(raw string) string {
	cleaned := noiseRegex.ReplaceAllString(raw, "")
	return strings.ToUpper(strings.TrimSpace(cleaned))
}

```

### Phase 3: The Multi-Tier Match Matrix

The sanitized descriptor is evaluated across three sequential logic sub-nodes:

1. **L1 Exact Lookup:** Queries Redis for an identical string match. If found, returns the `MasterMerchantID`.
2. **L2 Prefix/Trie Substring Match:** Evaluates if the sanitized string contains known prefixes in the `MasterPattern` database table.
3. **L3 Postgres Trigram Matching:** Executes a mathematical similarity calculation over indexed character blocks:
```sql
SELECT master_merchant_id FROM master_patterns 
WHERE cleaned_stem % $1 AND similarity(cleaned_stem, $1) > 0.75 
ORDER BY similarity(cleaned_stem, $1) DESC LIMIT 1;

```



### Phase 4: The Multi-Account Sweep & Transfer Gate

If the transaction is an inflow or matches bank transfer terminology (`INTERNAL TRANSFER`, `ONLINE PAYMENT`), the engine cross-analyzes the transaction against all known accounts within the organizational boundary to prevent phantom revenue creation.

An engine node checks if an unlinked transaction matches an inverse counterpart:

* `txA.OrgID == txB.OrgID`
* `txA.AccountID != txB.AccountID`
* `txA.Amount == -(txB.Amount)`
* `|txA.Date - txB.Date| <= 48 Hours`

If verified, the transaction bypasses the expense/revenue classification entirely and maps directly to `MacroClass = TRANSFER`.

---

## 4. The Cognitive LLM Node & Shannon Entropy Gate

When a transaction generates a **Cache Miss** at Phase 3, it enters the cognitive processing node. This node uses structured intelligence processing to safely evaluate the transaction.

### 4.1 Calculating Operational Entropy

The LLM node evaluates the sanitized descriptor text along with contextual metadata (`AccountHolderType`, `Amount`, `History`) and calculates a probability distribution across our possible target accounting classes ($x_i$).

We apply the Shannon Entropy equation to measure the engine's uncertainty:

$$H(X) = -\sum_{i=1}^n P(x_i) \log_2 P(x_i)$$

```
Example Scenario A: "AWS CONSOLE BILL"
LLM Output Distribution:
- Expense: 0.99
- Revenue: 0.005
- Transfer: 0.005

Calculated Entropy: H(X) = 0.08 (Extreme Certainty)
Action: Auto-Approve, write "AWS CONSOLE BILL" to Master Cache, pass to QBO.

```

```
Example Scenario B: "WELLS FARGO ONLINE PMNT" (No inverse account visible)
LLM Output Distribution:
- Transfer: 0.45
- Revenue: 0.35
- Equity Contribution: 0.20

Calculated Entropy: H(X) = 1.48 (High Uncertainty / Chaos)
Action: Exceeds System Threshold (Capped at 0.50). Route to Hold State.

```

### 4.2 The Hold State & Hound Agent Dispatch Loop

When $H(X) > 0.50$, the transaction is air-gapped from the production financial statements:

1. The record state transitions to `STATUS_HOLD`.
2. The engine spawns an asynchronous **Hound Agent** via NATS JetStream.
3. The Hound Agent scans local filesystem arrays (invoice caches, receipt folders) looking for a value match ($\pm\text{ settlement variances}$).
4. If local discovery fails, the Hound Agent sends an interactive message directly to the human workspace (Slack/Dashboard Hub):
*"We detected an ambiguous inflow of $10,000 from Wells Fargo. Is this an internal transfer, or new client revenue?"*
5. Upon human response, the Hound Agent updates the organizational memory profile, writes the mapping pattern to the core database cache, drops the entropy score to **0.00**, and frees the transaction to sync.

---

## 5. Post-Enrichment Deterministic Accounting Engine

Once the merchant entity is resolved (either via the match trees or the verified LLM node), the execution flow leaves the probabilistic layer and enters a strict, rule-based execution cycle:

1. **Intermediary Extraction:** If an intermediary flag is present (e.g., `Toast Inc`), the engine routes processing to the underlying merchant data while appending the payment processor ID to the transaction metadata for audit tracking.
2. **IRS Compliance Evaluation:** If `Amount > MasterMerchant.IRSReciptThreshold` (e.g., $75.00), the line item automatically sets `ReceiptRequired = true`. If no receipt document object reference is attached, it generates an automated line alert.
3. **QBO Schema Mapping:** The transaction is converted into the target QuickBooks Online structural general ledger format using the `DefaultQBOCategory` code found in the `MasterMerchant` record.
