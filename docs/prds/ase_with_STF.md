# Product Requirement Document (PRD)

## Project: Autonomous Semantic Engine (ASE) — US Core Bookkeeping (Revised)

**Author:** Head of Engineering / AI Infrastructure Architect

**Status:** Updated for Production Infrastructure

**Target Market:** United States SMBs & Mid-Market Corporates

**Data Rail Engine:** Stripe Financial Connections (Replacing Plaid)

**Core Matching Engine:** Structural Transaction Fingerprinting (STF) via Fuzzy Temporal Clustering (**Zero Embeddings for Core Matching**)

---

## 1. Architectural Changes & Guardrails

This revision modifies the core backend architecture to align with two non-negotiable product updates:

* **Plaid Eviction:** All banking and transaction feeds are migrated exclusively to **Stripe Financial Connections**. We will consume background asynchronous data refreshes via native Stripe webhook frames.
* **Vector Match Removal:** We have stripped fuzzy vector embeddings completely out of the core transaction matching logic. The engine matches entries using **Structural Transaction Fingerprinting (STF)** based on physical invariants (Exact Amounts + Vendor Tokens) combined with elastic time-decay calculations. Vector lookups via Vertex AI are restricted *only* to initial text string normalization (e.g., mapping messy vendor text to a clean entity list) during data ingestion.
* **Non-Negotiable Ledger Core:** The engine cannot run isolated pairing lookups. Every single processing run—including a single-use $10 self-service test—must instantiate and populate a strict double-entry **Shadow General Ledger (SGL)** before exporting data to external services.

---

## 2. Updated Core Technical Stack & Ingress Mapping

The platform operates as a decoupled micro-ASE architecture communicating over NATS JetStream, using AlloyDB Omni as its ACID-compliant relational core.

```
 [Email / Slack Ingress] ──► NATS: `ase.ingress.ap.*` ──► [ AP-ASE Engine ]
                                                              │
                                                        (Accrues Bill)
                                                              │
                                                              ▼
 [Stripe Webhook Event]  ──► NATS: `ase.ingress.st.*` ──► [ GL-ASE Referee ]
 (financial_connections)                                      ▲
                                                              │
                                                       (Accrues Invoice)
                                                              │
 [System Telemetry]      ──► NATS: `ase.ingress.ar.*` ──► [ AR-ASE Engine ]

```

### 2.1 Stripe Financial Connections Ingress Log

The **GL-ASE** deprecates Plaid handles and registers direct listeners for Stripe Financial Connections webhook objects. The engine captures two core events:

1. `financial_connections.account.refreshed_transactions`: Sent asynchronously when Stripe finishes a daily background bank data scrape.
2. The transaction sub-object status tracking array, processing states mapped directly to ledger variables:
* `status = 'posted'`: Hard settlement trigger. Executes immediate STF evaluation.
* `status = 'pending'`: Cache hydration trigger. Enforces temporary pending logs.



---

## 3. Structural Transaction Fingerprinting (STF) Protocol

Core transaction matching uses exact structural invariants and relative time distances rather than fuzzy text semantic comparisons.

### 3.1 Peak Extraction & Normalization

When an event lands in the engine, it strips away metadata and isolates the two **Landmark Peaks**:

1. **The Invariant Numeric Peak:** The exact absolute numerical amount (e.g., `45.00`).
2. **The Normalized Vendor Peak:** Messy vendor strings are passed through a quick internal lookup matrix inside AlloyDB Omni to output a distinct, standard Entity ID.

### 3.2 The Fuzzy Temporal Clustering Calculation

When a `posted` transaction clears the Stripe Financial Connections pipeline, the engine isolates all open context nodes (receipts, emails, or invoices) within that specific QuickBooks `realmId` namespace that match the exact Amount and Vendor ID.

It calculates a temporal proximity score for every matching candidate:

$$\text{Score}(e_a, e_c) = e^{-\left(\frac{\Delta t}{2\sigma}\right)^2}$$

Where:

* $\Delta t$ is the absolute real-world time delta in hours separating the context creation time from the Stripe bank clearing timestamp:

$$\Delta t = |t_{\text{context}} - t_{\text{bank}}|$$

* $\sigma$ is our elasticity tuning constant calibrated specifically to the constraints of US commercial rails (Default value: $\sigma = 24.0\text{ hours}$ to safely accommodate standard 2-to-3 day ACH settlement delays and 60-hour weekend card batching delays).

> ### The Auto-Reconciliation Constraint
> 
> 
> The engine will automatically execute a state collapse and lock the match into the general ledger without human intervention if and only if a unique candidate achieves a score satisfying:
> $$\text{Score}(e_a, e_c) \ge 0.85$$
> 
> 

---

## 4. The Shadow General Ledger (SGL) Core Framework

To guarantee compliance, the system cannot store unstructured "matches." Every transaction pair must generate balancing debit and credit entries inside an internal database representation of a double-entry ledger.

### 4.1 The Database Schema

Every tenant workspace—whether a permanent enterprise subscription or a temporary $10 self-service session—is provisioned with an isolated SGL runtime:

```sql
CREATE TABLE shadow_general_ledger (
    entry_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    qbo_realm_id VARCHAR(50) NOT NULL,          -- The tenant's identity namespace
    session_id UUID,                            -- Nullable, utilized for $10 ephemeral sandbox tracking
    account_code VARCHAR(10) NOT NULL,          -- Standard US GAAP code (e.g., '1010', '6010')
    debit NUMERIC(12, 2) DEFAULT 0.00,
    credit NUMERIC(12, 2) DEFAULT 0.00,
    transaction_timestamp TIMESTAMP WITH TIME ZONE NOT NULL,
    description TEXT NOT NULL,
    
    CONSTRAINT chk_double_entry_balance CHECK (
        (debit > 0 AND credit = 0) OR (credit > 0 AND debit = 0)
    )
);

-- Indexing optimized for Bitmap-Assisted Inline Filtering
CREATE INDEX idx_sgl_tenant_partition ON shadow_general_ledger (qbo_realm_id, session_id);

```

### 4.2 Standard US GAAP Default Account Mapping

When a user launches a session, the SGL maps transactions using standard accounting codes:

* **1010 (Operating Cash):** Credited for banking outflows, debited for revenue inflows via Stripe links.
* **2010 (Accounts Payable):** Used by the AP-ASE to track incoming vendor bills before cash settlement.
* **6010 (Software / SaaS Expenses):** Target debit account for recurring software subscriptions.
* **6020 (Travel & Entertainment):** Target debit account for food, transport, and client dinners.

---

## 5. Usage-Based Product-Led Growth (PLG) Engine

To allow self-service customers to experience the system instantly for $10, the engine implements a split operational pipeline based on tier validation.

```
                  [User Input Data Flow]
                            │
                            ▼
               [STF Core Match Algorithm]
                            │
               ┌────────────┴────────────┐
               ▼                         ▼
        Score ≥ 0.85                Score < 0.85
               │                         │
               ▼                         ▼
   [Atomic Write to SGL]       [Self-Service Tier] ──► Block Human Desk
               │                         │             Display Up-Sell Prompt
               ▼                         ▼
     [Render Trial Balance]    [Enterprise Headcount] ──► Route to Morocco HITL

```

### 5.1 The $10 Credit Pack Tier (Pure Automation Gate)

* **Ingress Method:** Direct web dashboard file drag-and-drop (CSV bank exports + receipt folder files).
* **Isolation:** The engine generates an ephemeral `session_id`, runs the STF matching calculations inside AlloyDB Omni, and populates the `shadow_general_ledger`.
* **The Margin Protection Guardrail:** If an entry scores below the 0.85 threshold, **the transaction is completely air-gapped from our Moroccan human-in-the-loop operational pilots.** The system outputs an interactive UI element showing a low-confidence flag and prompts the user to manually resolve it, or upgrade to the enterprise subscription to unlock autonomous human pilot remediation.
* **The Deliverable:** The interface compiles the SGL data and instantly renders a pristine, balanced Trial Balance and Income Statement directly onto the user's screen.

### 5.2 The Enterprise Digital Headcount Tier ($2,500/Month)

* **Ingress Method:** Permanent live pipelines via real-time Stripe Financial Connections webhooks and integrated corporate Slack channels.
* **Isolation:** Strict tenant routing using the QuickBooks Online `qbo_realm_id` combined with Row-Level Security (RLS).
* **The Operation:** High-confidence matches clear instantly. Any remaining low-confidence anomalies ($< 0.85$) are seamlessly packaged at the end of the day and sent to the Moroccan human exception pilots. The books are locked, validated, and pushed directly into the client's production QuickBooks Online environment before 9:00 AM EST every single morning.