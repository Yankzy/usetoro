You hit on a critical operational reality in Morocco. In the real world, Moroccan B2B transactions rarely jump straight from a bank statement line to a final invoice (*Facture*).

Moroccan B2B commerce runs on a **physical document chain (*la chaîne documentaire*)**:

```
[ Bon de Commande (BC) ] ──► [ Bon de Livraison (BL) ] ──► [ Facture Définitive ] ──► [ Traite / Chèque / Virement ]
   (Purchase Order)             (Delivery Slip)                 (Invoice)                    (Payment)

```

Many Moroccan SMEs pay suppliers based on a physical **Bon de Livraison (BL)** at the time of delivery, while the official **Facture** isn't generated until the end of the month. Furthermore, under Moroccan tax law (*régime de l'encaissement*), **TVA is only legally deductible upon actual cash settlement**, making the link between payment instrument and delivery order non-negotiable for the *DGI* (tax authority).

Here is the updated, complete PRD incorporating **Multi-Document Order Reconciliation (4-Way Matching)**.

---

# Technical PRD: ToroDB Auto-Reconciler (Fiduciaire & B2B Edition)

## Status

[x] Draft
[x] Review
[x] Approved
[x] Executed

**Document Version:** 4.0 (ASE Integration & Fignode Schema Update)

**Target Architecture:** Golang, Autonomous Semantic Engine (ASE), NATS JetStream, PostgreSQL/AlloyDB (Fignode Schema)

**Core Objective:** Automate multi-document reconciliation (*BC $\rightarrow$ BL $\rightarrow$ Facture $\rightarrow$ Relevé Bancaire*) for Moroccan businesses and Fiduciaires with 99% accuracy using Toro's ASE framework.

---

## 1. System Architecture: The Autonomous Semantic Engine (ASE)

The core pipeline runs on Toro's **Autonomous Semantic Engine (ASE)**. Each transaction/document chain functions as an autonomous micro-agent (`AutonomousSemanticEngineNode`) navigating a dynamically loaded DAG. Execution is completely decoupled via **NATS JetStream** and verified by an ephemeral in-memory **Redux engine** using RFC 6902 JSON patches.

### The DAG Topology (`pcm_bank_reconciliation.yml`)

1. **Ingestion & Agent Building (`DomainTool.BuildAgents`)**: Multi-format ingestion (PDF bank statements, BL scans, Sage ledgers) is handled by the domain tool, which constructs the initial `AutonomousSemanticEngineNode` agents.
2. **`normalization_and_hash` Node**: Standardizes vendor names and strings.
3. **`document_chain_matcher` Node**: Links `BC_Number`, `BL_Number`, and `Facture_Number` across the extracted payloads.
4. **`vendor_resolution` Node**: Evaluates the vendor. It relies on the Classifier to consult the in-memory cache and if unresolved, dispatches to the generic LLM agent fleet.
5. **Shannon Entropy & Guardrail State**: 
   - If Unified Confidence Score ($C$) $\ge 0.98$, the agent collapses to `READY_FOR_SYNC`.
   - If $C < 0.98$, the agent transitions to a holding state (e.g., `HOLD_AMBIGUOUS` or `HOLD_MISSING_CONTEXT`) for human review.
6. **`export` Node**: The `StatePersister` writes the final matched data, which can then be exported to Sage with the proper Moroccan *Plan Comptable* accounts and TVA settlement dates.

---

## 2. Multi-Document Order Reconciliation (The 4-Way Matcher)

When an order is reconciled, the ASE executes a 4-way comparison across four data points before resolving the state:

| Document Level | Extracted Data Fields | Matching Logic |
| --- | --- | --- |
| **1. Bon de Commande (BC)** | BC Number, Line Items, Unit Price, Agreed Payment Term (*au comptant*, 30/60 days). | Sets the commercial baseline & price ceiling. |
| **2. Bon de Livraison (BL)** | BL Number, Delivered Quantities, Stamp/Signature presence. | Verifies physical receipt of goods (prevents paying for undelivered stock). |
| **3. Facture Définitive** | Facture Number, ICE Number, Total HT, TVA Rate (20%, 14%, 10%, 7%), Total TTC. | Verifies legal tax invoice matches the BL quantities & BC prices. |
| **4. Bank Settlement** | Settlement Date, Amount, Payment Method (*Virement*, *Chèque*, *Effet/Traite*). | Verifies cash movement matches the Facture TTC. Establishes the exact TVA deductibility date. |

### Handling Moroccan Discrepancies

* **Partial Deliveries:** If `Quantity(BL) < Quantity(BC)`, the engine flags the invoice.
* **BL without Facture:** If a payment exists against a BL but no official *Facture* exists by month-end, the agent routes the item to `Account 4417` (*Fournisseurs - Factures non parvenues*).

---

## 3. Hybrid Entity Resolution (Fignode Schema)

Vendor resolution occurs in the high-velocity `fignode` schema, keeping UI-centric and reconciliation states isolated from the core authoritative ledger. 

### The Database Schema

```sql
-- Master Vendors
CREATE TABLE fignode.canonical_vendors (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fiduciaire_id UUID NOT NULL,
    display_name VARCHAR(255) NOT NULL, -- e.g., "Maroc Telecom"
    ice_number VARCHAR(15), -- Identifiant Commun de l'Entreprise (15 digits)
    default_account VARCHAR(20) NOT NULL, -- e.g., "6144"
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Variant Lookup Table
CREATE TABLE fignode.vendor_aliases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fiduciaire_id UUID NOT NULL,
    raw_variant VARCHAR(255) NOT NULL, -- e.g., "iam casablanca maraines"
    canonical_vendor_id UUID NOT NULL REFERENCES fignode.canonical_vendors(id) ON DELETE CASCADE,
    source VARCHAR(50) NOT NULL, -- 'historical', 'llm_auto', 'human_review'
    confidence_score NUMERIC(3,2) DEFAULT 1.00,
    CONSTRAINT unique_variant_per_tenant UNIQUE (fiduciaire_id, raw_variant)
);

CREATE INDEX idx_fignode_vendor_aliases_lookup ON fignode.vendor_aliases (fiduciaire_id, raw_variant);

-- State Tracking for 4-Way Match
CREATE TABLE fignode.order_reconciliations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fiduciaire_id UUID NOT NULL,
    bank_transaction_id UUID,
    facture_id UUID,
    bl_id UUID,
    bc_id UUID,
    status TEXT NOT NULL DEFAULT 'PENDING',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
```

---

## 4. LLM Confidence Routing & UI Hand-off

Unresolved classifications are sent to the LLM fleet via NATS JetStream. Results are evaluated by the Domain's `Classifier` based on the returned Shannon Entropy and Unified Confidence Score ($C$):

* **$C \ge 0.98$ (Auto-Commit):** Micro-agent successfully collapses state and transitions to `READY_FOR_SYNC`. Go writes the variant to `fignode.vendor_aliases`. Future matches hit the cache.
* **$C < 0.98$ (Low Confidence / Exception):** The transaction is halted and placed in a `HOLD_AMBIGUOUS` state. It appears in the Fignode employee dashboard's **Review Tab** alongside the Excel/PDF preview. A human reviewer resolves it, which feeds into the ASE **Backtracking Layer** to dynamically update future prompts.

---

## 5. Async WhatsApp Chaser (State Hydration via `ResumeAgent`)

When the 4-Way Matcher identifies a gap (e.g., Bank payment made, but missing the underlying *Facture* or *BL*):

1. **Pause & Persist:** The ASE calculates $C < 0.98$ due to missing context and places the agent in `HOLD_MISSING_CONTEXT`. The `StatePersister` serializes its state to the `fignode` database.
2. **Automated WhatsApp Outreach (`GenerateAlertPayload`)**: The engine generates an alert payload that a webhook service uses to send a WhatsApp message to the SME owner in Darija/French:
> *"Salam, we see a payment of 15,000 DH to Supplier X on the 12th. We have the BL, but we need a photo of the official Facture to claim your TVA deduction. Please send a photo here."*

3. **Hydrate & Resume (`ResumeAgent`)**: When the user replies with a photo, the webhook endpoint triggers the OCR process, reconstructs the agent from the database via `DomainTool.ResumeAgent()`, attaches the extracted *Facture* details, and resumes DAG execution.