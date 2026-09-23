# Moroccan Bookkeeping & Bank Reconciliation Pipeline (CGNC / Sage 100)

Moroccan accountants process financial data and reconcile accounts by adhering strictly to the *Code Général de Normalisation Comptable* (CGNC) standards.

---

## 🏛️ Fundamental Accounting Principle: Categorization Precedes Reconciliation

In real-world accounting, **Categorization & Ledger Entry (*Saisie Comptable*) MUST strictly precede Bank Reconciliation**:
1. **You cannot reconcile what is not yet in the ledger**: Invoices, receipts, and bills must first be classified into PCGE accounts (Class 1–7) and posted to the General Ledger (including Account `5141`).
2. **Reconciliation is verification, not creation**: The bank statement is an external record used to verify the internal ledger.
3. **Missing entries are handled only after matching**: Only after matching reveals orphan bank lines (bank fees, interest, direct debits) do we auto-post adjustments (*imputations d'office*).

### Subsystem Division of Responsibilities
* **Document Worker & OCR Agent (Go)**: Native ToroDB OCR document agent in [agent.go](file:///Users/Yankz/programming/usetoro/tap/agents/ocr_agent/agent.go), [document_ocr_worker.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/knowledge_system/document_ocr_worker.go), and statement ingestion in [pcm_statement_intake.go](file:///Users/Yankz/programming/usetoro/go/internal/workers/pcm_statement_intake.go).
* **ASE Runtime (Go / YAML)**: PCGE Categorization, Moroccan CGI Tax Rules (VAT/RAS), and balanced journal creation in [pcm_bank_cash_accounting_dag.yml](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/dags/pcm_bank_cash_accounting_dag.yml).
* **Multi-Account Routing Engine (`reconciliation_prod/routing` - Python)**: Global feasibility matrix ($F_{i,a}$), LLM routing utility ($S_{i,a}$), and CP-SAT multi-bank invoice partitioner.
* **State Engine (PostgreSQL `051` / Go)**: Stateful ERB session lifecycle, balance continuity, and prior-period carryovers in [bank_reconciliation_service.go](file:///Users/Yankz/programming/usetoro/go/internal/services/accounting/bank_reconciliation_service.go).
* **Account Pointage Engine (`reconciliation_prod/reconciliation` - Python)**: Bipartite graph partitioning, 4-tier Lexicographic CP-SAT optimization, and 8-point invariant validation.
* **Sage Exporter (Go)**: Standard `.PNM` file export for Sage 100 in [exporter.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/sage/pnm/exporter.go).

---

## 📋 Canonical 6-Step Implementation Audit

```
┌─────────────────────────┐     ┌─────────────────────────┐     ┌─────────────────────────┐
│ Step 1: Document Intake │ ──► │ Step 2: PCGE Saisie     │ ──► │ Step 3: Multi-Account   │
│  (Go / OCR Agent)       │     │ (Go / ASE PCGE & Taxes) │     │ Routing (Python) & State│
└─────────────────────────┘     └─────────────────────────┘     └───────────┬─────────────┘
                                                                            │
                                                                            ▼
┌─────────────────────────┐     ┌─────────────────────────┐     ┌─────────────────────────┐
│ Step 6: Seal & Export   │ ◄── │ Step 5: Post Missing    │ ◄── │ Step 4: Account Pointage│
│   (Sage 100 PNM File)   │     │ (ASE Fee/VAT Generator) │     │ (Python / CP-SAT Solver)│
└─────────────────────────┘     └─────────────────────────┘     └─────────────────────────┘
```

---

### Step 1. Collect Source Documents (*Collecte des Pièces Justificatives*)
Gather all *pièces justificatives* (invoices, receipts, cheque stubs, payment vouchers) alongside official monthly bank statements (*relevés bancaires*).

* **Role**: Ingestion & Extraction (No accounting classification at this stage).
* **Status**: `COMPLETED`
* **What is Done**:
  - Native ToroDB OCR Document Agent analyzing images, PDFs, bank statements, receipts, and invoices via [agent.go](file:///Users/Yankz/programming/usetoro/tap/agents/ocr_agent/agent.go) and [document_ocr_worker.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/knowledge_system/document_ocr_worker.go).
  - Structured extraction of bank statement balances, transaction lines, polarity indicators, and column mappings in [pcm_statement_intake.go](file:///Users/Yankz/programming/usetoro/go/internal/workers/pcm_statement_intake.go) and [csv_mapping](file:///Users/Yankz/programming/usetoro/tap/agents/csv_mapping/).
* **What is Needed**: N/A (Complete).


---

### Step 2. Execute Data Entry & Categorization (*Saisie Comptable*)
Record all known business transactions into the General Ledger across the respective journals (Sales, Purchases, Treasury). Under the Moroccan CGNC, all bank transactions must be booked to **Class 5** accounts, specifically **5141 (Banques - soldes débiteurs)**.

* **Role**: Categorization, CGI Tax Assessment & Double-Entry Journal Construction.
* **Status**: `COMPLETED`
* **What is Done**:
  - Macro PCGE routing (Classes 1–7) and micro account mapping in [pcm_bank_cash_accounting_dag.yml](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/dags/pcm_bank_cash_accounting_dag.yml).
  - Moroccan CGI tax rules: SIMPL-TVA Art. 112/101, Rent RAS Art. 15 ter (5%), Foreign RAS Art. 15 (10% + 20% reverse charge), and ADII customs.
  - Balanced double-entry draft builder ($\sum \text{Debit} = \sum \text{Credit}$) and 8-digit PCGE account resolver.
  - Multi-journal Excel exporter in [excel_exporter.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/domain_tools/pcm_cash/excel_exporter.go).
* **What is Needed**: N/A (Complete).

---

### Step 3. Initialize ERB Session & Multi-Account Routing (*Ouverture & Routage Multi-Banques*)
For companies operating multiple bank accounts (e.g. Attijariwafa, Banque Populaire, BMCE), unassigned invoices must first be routed to the feasible bank account. Then, initialize the reconciliation session for the target period, lock the physical statement opening/closing balance, and carry forward outstanding discrepancies from prior months.

* **Role**: Multi-Account Partitioning & Stateful Session Lifecycle (Not pointage).
* **Status**: `HALF-COMPLETED (Routing & Backend Services Built, Pipeline Trigger In-Progress)`
* **What is Done**:
  - **Multi-Account Routing Engine ([reconciliation_prod/routing/](file:///Users/Yankz/programming/usetoro/python-worker/app/reconciliation_prod/routing/))**:
    - **Phase 1 (Feasibility)**: [feasibility.py](file:///Users/Yankz/programming/usetoro/python-worker/app/reconciliation_prod/routing/feasibility.py) computes binary Dynamic Programming subset-sum feasibility matrix $F_{i,a}$ for every invoice $i$ across bank accounts $a$.
    - **Phase 2 (Scoring)**: [scorer.py](file:///Users/Yankz/programming/usetoro/python-worker/app/reconciliation_prod/routing/scorer.py) queries LLM for semantic utility scores $S_{i,a} \in [0, 1000]$ on feasible accounts.
    - **Phase 3 (Optimization)**: [optimizer.py](file:///Users/Yankz/programming/usetoro/python-worker/app/reconciliation_prod/routing/optimizer.py) runs an Integer Programming CP-SAT model to globally partition invoices to bank accounts maximizing $(\text{Amount} \times 10^6) + (\text{Utility} \times 1)$ subject to account capacity constraints.
    - **NATS Worker**: [routing_worker.py](file:///Users/Yankz/programming/usetoro/python-worker/app/reconciliation_prod/routing_worker.py) subscribes on `worker.inbox.routing`.
  - **SQL Schema ([051_bank_reconciliation_state.sql](file:///Users/Yankz/programming/usetoro/sql/schema/051_bank_reconciliation_state.sql))**:
    - `shadow_erp.bank_reconciliation_states`: Durable snapshot header recording `statement_opening_balance`, `book_opening_balance`, `bank_statement_balance`, `book_bank_balance`, `outstanding_book_inflows/outflows`, `difference`, and `state_hash`. Enforces `CHECK (status <> 'CLOSED' OR difference = 0)`.
    - `shadow_erp.bank_reconciliation_state_memberships`: Records discrete evidence dispositions (`RECONCILED`, `UNRECONCILED`, `CARRIED_FORWARD`, `MISSING_EVIDENCE`, `HUMAN_REVIEW`) with `carry_forward` boolean flags.
    - `shadow_erp.bank_reconciliation_state_invalidations` & `shadow_erp.reconciliation_match_groups`: Append-only audit trail and match relationship evidence.
  - **Go Domain Service ([bank_reconciliation_service.go](file:///Users/Yankz/programming/usetoro/go/internal/services/accounting/bank_reconciliation_service.go))**:
    - `CreateMigratedBaseline()`: Sets up the initial closed baseline state (`MIGRATED_BASELINE`) for accounts with prior unmanaged history.
    - `PreparePeriod()`: Automatically queries the previous closed state for period $M-1$, validates strict continuity ($Solde_{open}(M) == Solde_{close}(M-1)$), pulls forward previous un-reconciled items (`membershipModelsToCarryForward()`), computes period book movements from `shadow_erp.journal_lines`, and writes an `OPEN` state.
    - `ClosePeriod()` & `CorrectClosedPeriod()`: Validates zero-difference balance sealing and immutable state invalidations.
  - **Lifecycle NATS Worker ([bank_reconciliation_lifecycle_worker.go](file:///Users/Yankz/programming/usetoro/go/internal/workers/bank_reconciliation_lifecycle_worker.go))**: Exposes endpoints for `baseline`, `prepare`, `revise`, `close`, and `correct`.
* **What Needs to Be Done (Plugging the Pipeline Together)**:
  1. **Intake-to-Routing Trigger**: Dispatch `worker.inbox.routing` when multiple bank accounts exist to partition open invoices across accounts before statement reconciliation.
  2. **Intake-to-Lifecycle Trigger**: In [pcm_statement_intake.go](file:///Users/Yankz/programming/usetoro/go/internal/workers/pcm_statement_intake.go), dispatch `PreparePeriod` via NATS (`workers.accounting.bank_reconciliation.prepare`) after statement lines and balances are parsed.
  3. **Reconciliation Solver Hydration**: In [reconciliation_worker.py](file:///Users/Yankz/programming/usetoro/python-worker/app/reconciliation_prod/reconciliation_worker.py), pass both routed current period journal lines and prior `CARRIED_FORWARD` items from `shadow_erp.bank_reconciliation_state_memberships` into the `BookItem` array for CP-SAT matching.

---

### Step 4. Perform the Pointage (*Matching / Lettrage*)
Compare the un-reconciled ledger entries in Account 5141 line-by-line against the bank statement lines for the specific bank account. Match 1:1, 1:N, N:1 items; flag uncleared cheques or uncredited deposits as temporary discrepancies (*suspens*).

* **Role**: Single-Account Scientific Mathematical Optimization & Invariant Verification.
* **Status**: `COMPLETED`
* **What is Done ([reconciliation_prod/reconciliation/](file:///Users/Yankz/programming/usetoro/python-worker/app/reconciliation_prod/reconciliation/))**:
  - Bipartite graph partitioning with BFS connected components in [candidate_generation.py](file:///Users/Yankz/programming/usetoro/python-worker/app/reconciliation_prod/reconciliation/candidate_generation.py).
  - Hardened 4-tier Lexicographic CP-SAT optimizer ($10^9 / 10^5 / -10^4 / 1$) in [model.py](file:///Users/Yankz/programming/usetoro/python-worker/app/reconciliation_prod/reconciliation/model.py) and [cp_sat.py](file:///Users/Yankz/programming/usetoro/python-worker/app/reconciliation_prod/reconciliation/cp_sat.py).
  - Single-shot LLM semantic scoring (0–1000) with counterfactual labeling in [semantic_engine.py](file:///Users/Yankz/programming/usetoro/python-worker/app/reconciliation_prod/reconciliation/semantic_engine.py).
  - 8-point deterministic invariant verifier in [validation.py](file:///Users/Yankz/programming/usetoro/python-worker/app/reconciliation_prod/reconciliation/validation.py).
  - Reconciles batches via NATS worker [reconciliation_worker.py](file:///Users/Yankz/programming/usetoro/python-worker/app/reconciliation_prod/reconciliation_worker.py).
* **What is Needed**: N/A (Complete).

---

### Step 5. Record Missing Bank Transactions (*Traitement des Écarts / Imputations d'Office*)
Bank statements frequently contain lines not yet recorded in the books (bank fees, interest/agios, direct debits, unexpected customer payments). Book these missing entries to the treasury journal (e.g. Account **6147 - Services bancaires** and **34552 - TVA récupérable sur les charges**).

* **Role**: Auto-Posting Orphan Bank Activity Discovered by Reconciliation.
* **Status**: `HALF-COMPLETED`
* **What is Done**:
  - Inflow/outflow classifier rules for orphan bank movements in [pcm_bank_cash_accounting_dag.yml](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/dags/pcm_bank_cash_accounting_dag.yml).
  - Automated bank fee splitting and 10% banking VAT extraction down to 0.01 MAD in [bank_fee_splitter.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/domain_tools/pcm_cash/bank_fee_splitter.go) and [cash_vat_extractor.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/domain_tools/pcm_cash/cash_vat_extractor.go).
* **What is Needed to Complete**:
  - Automated closed-loop trigger routing unresolved bank lines from `reconciliation_prod` back into the ASE DAG to auto-generate missing candidate journal entries.

---

### Step 6. Validate, Generate ERB Certificate, and Archive (*Clôture & Export Sage*)
The reconciliation is successful when the adjusted accounting balance matches the bank statement balance down to the exact centime (0.01 MAD). Seal the state in the database, generate the official ERB report, and export balanced entries to Sage 100.

* **Role**: State Sealing, Audit Report Generation & Sage PNM File Export.
* **Status**: `HALF-COMPLETED`
* **What is Done**:
  - Standard Sage 100 Paramétrable (`.PNM`) file exporter with debit/credit balance checks in [exporter.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/sage/pnm/exporter.go).
  - Rejection diagnostics & counterfactual explanations in [diagnostics.py](file:///Users/Yankz/programming/usetoro/python-worker/app/reconciliation_prod/reconciliation/diagnostics.py).
* **What is Needed to Complete**:
  - Auto-close handshake: Upon `reconciliation_prod` achieving `difference == 0`, trigger `workers.accounting.bank_reconciliation.close` with the match group IDs and verified `state_hash`.
  - Printable/PDF *État de Rapprochement Bancaire* (ERB) summary certificate generator showing:
    $$\text{Solde Relevé Bancaire} \pm \text{Opérations en Suspens} = \text{Solde Comptable Rectifié}$$




