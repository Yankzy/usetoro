# Stage 2: Bank Reconciliation & CP-SAT Optimization

This document details the mathematical optimization, candidate generation, allocation support semantics, and persistence models of **Stage 2: Bank Reconciliation** (`ledger/bookkeeping_state/reconciliation/`).

---

## 1. What is Stage 2?

Stage 2 is the bank reconciliation phase. It matches bank statement movements (`BankItem`) against **already-posted cash transactions in the General Ledger** (`POSTED_BOOK_ITEM` backed by `TransactionModel`).

### Critical Architectural Distinction:
> [!IMPORTANT]
> **Stage 2 Does Not Create Accounting Truth**:
> Stage 2 does not post journal entries, does not alter debit/credit balances, and does not touch chart of accounts balances. It simply establishes an audited, value-conserving matching link between an observed bank statement line and an existing ledger cash entry that was already posted by a cashier, payment gateway, payroll system, or Stage 1 settlement.

---

## 2. Combinatorial Optimization with Google OR-Tools CP-SAT

When multiple bank movements and ledger entries exist across a reconciliation window, simple greedy matching produces suboptimal or fragmented pairings.

Toro formulates bank reconciliation as a **discrete combinatorial optimization problem** solved via the Google OR-Tools Constraint Programming - Satisfiability (CP-SAT) solver (`ledger/bookkeeping_state/reconciliation/optimizer.py`).

### Three-Tier Lexicographical Objective

The CP-SAT model optimizes three hierarchical integer objectives in strict lexical order:

1. **Tier 1: Maximum Total Currency Value (Exact Monetary Conservation)**
   $$\max \sum_{h \in \text{Accepted}} \text{amount\_units}(h)$$
   Guarantees that the maximum possible monetary volume is reconciled.
2. **Tier 2: Maximum Fully Cleared Items (Fragment Minimization)**
   $$\max \sum_{i \in \text{Items}} \mathbb{I}(\text{remaining\_units}(i) = 0)$$
   Prefers clean 1:1, 1:N, or N:1 matches that drive item balances to exactly zero, avoiding lingering partial fractions.
3. **Tier 3: Maximum Semantic Utility Score**
   $$\max \sum_{h \in \text{Accepted}} \text{amount\_units}(h) \times \text{utility}(h)$$
   Uses the semantic scoring utility ($[0, 1000]$ integer scale) derived from date proximity, reference similarity, and counterparty compatibility to break ties between equal-value candidates.

---

## 3. Allocation Support & Policy Boundaries

Every proposed candidate reconciliation hypothesis is assigned an `AllocationSupport` classification (`ledger/bookkeeping_state/domain/enums.py`):

| Support Type | Definition | Automatic Reconcile? |
|---|---|---|
| **`EXPLICIT_EVIDENCE`** | Direct documentary evidence exists (e.g. check number, bank reference, invoice remittance token). | **Yes** (always eligible) |
| **`UNIQUE_INFERENCE`** | No explicit token, but mathematically unique partition of amounts and dates within tolerance window. | **Conditional** (controlled by policy flag `auto_reconcile_unique_inferred_allocation`) |
| **`INSUFFICIENT_EVIDENCE`** | Multiple conflicting allocations could satisfy the amount constraint; identity cannot be proven. | **No** (held for human review) |
| **`CONTRADICTION`** | Allocation directly contradicts known evidence (e.g. mismatched counterparties). | **No** (rejected) |

### Dual-Boundary Policy Protection
The flag `auto_reconcile_unique_inferred_allocation` is enforced at **two independent layers**:
1. During candidate generation in `ReconciliationService.run()`.
2. Inside `_handle_reconciliation_transition` in the `TransitionEngine`. If an unverified unique inference command reaches the handler while the policy is `False`, the handler rejects it.

---

## 4. Worked Example: Pre-Posted Cash GL Reconciliation (Case 3)

### Scenario:
A company cashier receives 2,500.00 MAD cash for a counter sale and deposits it at the bank counter on April 12, 2026. The cashier immediately enters the deposit in the software:
- **Posted Journal Entry**: `FIT-SYNTH-CASE3-CASHGL`
  - Debit: `5141` Attijariwafa Bank $\to 2,500.00$ MAD
  - Credit: `7111` Vente de marchandises $\to 2,500.00$ MAD
- **Bank Statement Line**: Arrives 3 days later:
  - Deposit of $+2,500.00$ MAD ($25,000,000$ units)
  - Narrative: `"VERSEMENT ESPECES AGENCE BANCAIRE REF DEP-4412"`

### Execution:
1. **Stage 1 Coordinator**: Checks if the bank deposit matches open invoices. None exist. However, Stage 2 preplan detects the matching posted cash GL leg on account `5141`.
2. **Stage 2 Solver**: Generates a 1:1 hypothesis pairing the statement line with `TransactionModel` leg `FIT-SYNTH-CASE3-CASHGL`.
3. **CP-SAT Solution**: Reconciles the pair with utility 950 (perfect amount match, date delta 3 days).
4. **Committed Records**:
   - `BookkeepingReconciliation`: ID `rec:synth-session-1:1`.
   - `BookkeepingReconciliationBankAllocation`: Allocates $25,000,000$ units on `staged:9aadecc0-...`.
   - `BookkeepingReconciliationBookAllocation`: Allocates $25,000,000$ units on `tx:c817f2e5-...`.
5. **Net State**: Both bank item and book transaction have `remaining_units = 0`. No new journal entry was created.

---

## 5. Reconciliation Invalidation & Posting Protection

### Ordinary Invalidation
If an accountant discovers an error in an ordinary Stage 2 reconciliation:
1. Issue `InvalidateReconciliationCommand(reconciliation_id=..., reason=...)`.
2. The engine verifies the reconciliation exists and has no prior invalidations.
3. Inserts `BookkeepingReconciliationInvalidation`.
4. Allocated capacities on the bank item and book item are immediately released for future re-reconciliation.

### Posting-Owned Reconciliation Protection
Direct reconciliations created by the **Residual Bank Posting loop** (`BookkeepingResidualBankPosting`) link an external bank item to its dedicated contra-clearing journal entry.

> [!CAUTION]
> **Standalone Invalidation Forbidden**:
> If an accountant attempts to invalidate a posting-owned reconciliation:
> ```python
> posting = state.get_residual_bank_posting_for_reconciliation(reconciliation_id)
> if posting is not None:
>     return TransitionRejection(
>         code=RejectionCode.CANNOT_INVALIDATE_POSTING_RECONCILIATION,
>         message="Reconciliation is owned by authoritative residual-bank posting and requires formal posting reversal.",
>     )
> ```
> This prevents an invalidation from peeling the bank item back into residual state while its posted Journal Entry remains in the General Ledger (preventing Case E corruption).
