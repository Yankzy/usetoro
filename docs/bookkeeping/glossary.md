# Glossary of Terms

This glossary defines the canonical domain vocabulary, mathematical concepts, and system symbols utilized throughout the Toro **BookkeepingState** runtime.

---

## Quick Navigation

[A](#a) • [B](#b) • [C](#c) • [D](#d) • [E](#e) • [H](#h) • [I](#i) • [M](#m) • [N](#n) • [O](#o) • [P](#p) • [R](#r) • [S](#s) • [T](#t) • [U](#u)

---

## A

### AllocationSupport
A four-state discrete enum evaluating the evidentiary foundation of a candidate reconciliation match:
- `EXPLICIT_EVIDENCE`: Direct line-item document or counterparty linkage.
- `UNIQUE_INFERENCE`: Mathematically unique partition among candidate items.
- `INSUFFICIENT_EVIDENCE`: Multiple plausible candidate allocations exist.
- `CONTRADICTION`: Allocation conflicts with known evidence assertions.

### AmountUnits
The authoritative integer representation of monetary amounts in the runtime.
- **Scale Factor**: $10,000$ ($1\text{ Currency Unit} = 10,000\text{ AmountUnits}$).
- **Example**: $7,850.00\text{ MAD} = 78,500,000\text{ AmountUnits}$.
- **Invariant**: Solvers, database columns, and state deltas strictly prohibit floating-point values to eliminate rounding drift.

### ASE (Autonomous Semantic Engine)
The distributed AI semantic reasoning worker implemented in Go. It subscribes to NATS JetStream, evaluates bank statement lines against the Moroccan Chart of Accounts (`pcm_cash_accounting`), and returns sealed classification decisions (`CLASSIFIED` or `HOLD`).

---

## B

### BankItem
The domain entity representing a single transaction line on an imported bank statement. It carries an `amount_units`, `direction` (`DEBIT` or `CREDIT`), `transaction_date`, and raw bank narrative text.

### BookItem
The domain entity representing an internal accounting record or obligation. Can be an approved open invoice/bill (Stage 1) or an already-posted General Ledger cash movement (Stage 2).

### BookkeepingState
The in-memory, immutable-projection state model representing a company's financial and transactional world at a specific moment in time. Hydrated directly from PostgreSQL records, it is governed by the `TransitionEngine`.

---

## C

### Case A–F Selection
The strict six-case decision boundary governing residual bank item eligibility for AI categorization:
- **Case A (Fresh)**: No decision history $\to$ **Eligible for ASE**.
- **Case B (Active HOLD)**: Current decision is `HOLD` $\to$ **Ineligible** (leaves bank item unresolved).
- **Case C (Active CLASSIFIED Unposted)**: Decision is `CLASSIFIED` but not yet posted $\to$ **Ineligible for ASE; proceeds to Posting Loop**.
- **Case D (Invalidated Tip)**: Latest decision was invalidated $\to$ **Eligible for ASE** (new decision supersedes).
- **Case E (Residual with Posted History)**: Active posting exists but item still has residual balance $\to$ **Invariant Corruption** (`ResidualBankInvariantCorruptionError`).
- **Case F (Fully Reconciled)**: Item has zero residual balance $\to$ **Excluded**.

### Chart of Accounts (CoA) / PCGE
*Plan Comptable Général des Entreprises* — The standardized Moroccan accounting code framework mandated by law. Common accounts:
- `5141xxxx`: Bank asset accounts (*Banques*).
- `6147xxxx`: Bank service fees (*Services bancaires*).
- `4455xxxx`: State VAT payable/deductible (*TVA récupérable*).
- `7111xxxx`: Sales of goods (*Ventes de marchandises*).

### CLASSIFIED
A residual bank classification status indicating that the Autonomous Semantic Engine has determined the target Chart of Accounts code with confidence $\ge 0.98$.

### Contra Account
The offsetting General Ledger account paired against the primary bank cash account in a double-entry residual journal entry. For bank fees, the contra account is `61470000` (Debit) while the bank account `51410001` is Credit.

---

## D

### DAG (Directed Acyclic Graph)
The directed graph used for complex, multi-step semantic evaluation pipelines (such as `bookkeeping_bank_categorization_v1`).

### Direct Reconciliation
An atomic reconciliation link established between a bank item and a newly posted book item as part of a residual journal entry posting (`BookkeepingResidualBankPosting`). It preserves posting provenance and prevents double-matching.

### Double-Entry Invariant
The fundamental accounting principle requiring that for every journal entry, the sum of all debits must equal the sum of all credits:
$$\sum \text{Debit Units} = \sum \text{Credit Units}$$
Any violation results in immediate transaction rollback.

---

## E

### Economic Residual
A bank statement transaction line that remains unreconciled after both Stage 1 (Payment Application) and Stage 2 (Combinatorial Reconciliation) have completed. Represents cash movements that have no corresponding General Ledger transaction.

---

## H

### HOLD
A residual bank classification status indicating that the item cannot be automatically posted. Occurs when:
1. ASE confidence is below the $0.98$ policy threshold.
2. The item requires missing evidence (such as an invoice, contract, or customs slip).
3. The transaction is ambiguous or requires certified accountant intervention.

### Hydration
The deterministic process of loading PostgreSQL records into an open in-memory `BookkeepingState` snapshot ($P_k \to S_0$).

---

## I

### Idempotency Lease
A distributed distributed-lock mechanism implemented via NATS JetStream Key-Value stores. Prevents multiple workers from concurrently categorizing the identical semantic digest within a 60-second TTL window.

### Invalidation
The process of marking an evidence assertion, classification decision, or reconciliation as inactive (`is_active = False`) without deleting rows from PostgreSQL. Preserves a complete audit trail.

---

## M

### Monetary Scale Factor
The integer scaling factor $10,000$ applied to all currency amounts in the system. Prevents floating-point rounding errors during multi-item combinatorial splits.

---

## N

### NATS JetStream
The low-latency message broker and distributed streaming system facilitating RPC communication between the Python bookkeeping orchestrator and the Go ASE semantic worker.

---

## O

### OCC (Optimistic Concurrency Control)
A concurrency control strategy ensuring database integrity without long-lived row locks. Commands assert an `expected_persistence_revision` ($P_k$). If the database revision has advanced ($P_{k+1}$), the command is rejected with `OptimisticConcurrencyError`.

### Operator (`BookkeepingOperator`)
The natural-language AI agent in `ledger/bookkeeping_state/operator/` that enables accountants to interrogate hydrated `BookkeepingState` via typed, read-only tools without hallucinations.

---

## P

### Payment Application (Stage 1)
The process of matching unreconciled bank movements against approved, unpaid invoices and bills. Produces real double-entry payment transactions and settles open debt.

### PCM Cash Accounting
*Plan Comptable Marocain* cash accounting tool in Go. Inspects transaction direction, amounts, and Moroccan business keywords to recommend PCGE account codes.

### Persistence Revision ($P_k$)
The monotonically increasing revision integer stored in the `BookkeepingRevision` table. Increments by $+1$ each time a durable database transaction commits.

---

## R

### Reconciliation (Stage 2)
The combinatorial matching of bank statement transactions against already-posted General Ledger cash book items using Google OR-Tools CP-SAT discrete constraint programming.

### Rehydration
Re-executing the hydration pipeline to build a new `BookkeepingState` after a durable transaction has committed ($P_k \to P_{k+1} \to S_0$).

### Residual Bank Posting
The deterministic transition that creates a balanced double-entry Journal Entry for an approved `CLASSIFIED` residual bank item and simultaneously links it via a direct reconciliation.

---

## S

### Sealed Decision
An immutable, signed classification decision record (`BookkeepingResidualBankClassificationDecision`) stored in PostgreSQL.

### Semantic Digest
A canonical SHA-256 hash computed from a bank item's normalized text description, amount, and currency. Used for deduplication and lease locking.

### State Revision ($S_k$)
The local, in-memory revision integer of a `BookkeepingState` instance. Initializes to $S_0$ upon hydration and increments by $+1$ for each transition applied in memory.

### Supersession
The mechanism by which a newer evidence assertion or classification decision replaces an older one. The older record is marked superseded, maintaining a tamper-evident audit history.

---

## T

### TransitionEngine
The authoritative state machine component that validates preconditions, checks OCC revisions, executes command handlers, and applies state deltas atomically.

---

## U

### Unresolved Bank Item
A bank statement item that remains unreconciled and unclassified (or parked on `HOLD`). These items surface prominently in the Operator REPL for accountant review.
