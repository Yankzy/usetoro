# Moroccan Bookkeeping Pipeline Reference

## Purpose

This document defines the expected end-to-end bookkeeping pipeline for Toro's Moroccan accounting workflow.

The purpose is not to prescribe the exact implementation. It is to define the functional stages, the responsibility of each stage, the information that enters it, the information that should come out of it, and the conditions that should hold before moving to the next stage.

The implementation should be compared against this pipeline to identify:

* missing stages
* stages implemented in the wrong order
* responsibilities that are mixed together
* missing state transitions
* missing validation
* missing exception handling
* missing accounting outputs
* stages that exist conceptually but are not represented explicitly in code or persisted state

The canonical pipeline is:

`Documents + Bank Statements -> Extraction -> Accounting Entries -> PCGE Categorization -> Customer/Supplier Matching -> Bank Reconciliation -> Exception Review -> Tax/Accounting Controls -> Closing State`

---

# 1. Source Document Intake

## Responsibility

Collect all accounting evidence required to reconstruct the company's financial activity for a bookkeeping period.

The system should treat documents as evidence, not accounting entries.

Typical source documents include:

* bank statements
* customer invoices
* supplier invoices
* receipts
* expense documents
* credit notes
* debit notes
* payroll documents
* tax documents
* cash records
* loan statements
* lease documents
* payment confirmations

## Input

Raw files received through channels such as:

* email
* upload
* API
* connected accounting system
* connected bank
* manual submission

## Output

A normalized document inventory.

Each document should have at minimum:

```text
document_id
tenant_id
period
document_type
source
original_file
received_at
processing_status
```

Where possible:

```text
issuer
recipient
document_date
currency
document_number
related_entity
```

## Important Rule

At this stage, the system must not assume that a document corresponds to a valid accounting transaction.

A document is evidence.

---

# 2. Bank Statement Intake

Bank statements should be treated as a special source because they represent the external financial record of movements through a bank account.

## Input

Examples:

* PDF bank statement
* CSV export
* XLS/XLSX export
* API transaction feed

## Output

A normalized bank statement containing immutable bank lines.

Example:

```text
bank_statement
    statement_id
    bank_account_id
    period_start
    period_end
    opening_balance
    closing_balance
    currency

bank_lines[]
    bank_line_id
    transaction_date
    value_date
    description
    debit
    credit
    amount
    currency
    reference
    raw_description
```

## Required Validation

The system should verify, where possible:

```text
opening_balance
+ total_credits
- total_debits
= closing_balance
```

If this relationship does not hold, the statement should be flagged before reconciliation.

Bank lines should remain immutable after extraction.

---

# 3. Extraction

## Responsibility

Convert unstructured accounting evidence into structured facts.

Extraction is not accounting classification.

For example:

A supplier invoice may say:

```text
Supplier: Maroc Telecom
Invoice Number: MT-92812
Date: 2026-07-10
HT: 1,000 MAD
VAT: 200 MAD
TTC: 1,200 MAD
```

Extraction should produce these facts without deciding the accounting account.

## Inputs

* invoices
* receipts
* statements
* payroll documents
* other accounting evidence

## Outputs

Structured document representations.

Example invoice:

```json
{
  "document_type": "supplier_invoice",
  "supplier": "Maroc Telecom",
  "invoice_number": "MT-92812",
  "invoice_date": "2026-07-10",
  "currency": "MAD",
  "amount_excl_tax": 1000,
  "vat_amount": 200,
  "amount_incl_tax": 1200
}
```

## Extraction Responsibilities

The system may perform:

* OCR
* table extraction
* entity extraction
* date normalization
* currency normalization
* amount normalization
* invoice number extraction
* VAT extraction
* counterparty identification

## Validation

Extraction should distinguish:

```text
extracted fact
inferred fact
missing fact
uncertain fact
```

The downstream accounting engine should know which information came directly from evidence and which information was inferred.

---

# 4. Accounting Entry Construction

## Responsibility

Convert extracted business events into candidate accounting journal entries.

This is the point where evidence becomes bookkeeping representation.

Examples:

### Supplier invoice

Economic event:

```text
Company received a supplier invoice for 1,200 MAD.
```

Candidate accounting representation:

```text
Expense account         Debit
Recoverable VAT         Debit
Supplier account        Credit
```

### Customer invoice

```text
Customer account        Debit
Revenue account         Credit
VAT collected           Credit
```

### Bank transaction without invoice

A bank line may itself generate a candidate accounting entry if sufficient evidence exists.

Examples:

* bank fees
* interest
* loan repayment
* direct debit
* tax payment
* transfer

## Input

Structured evidence from extraction.

## Output

One or more candidate journal entries.

Example:

```text
journal_entry_id
source_document_id
journal_type
entry_date
description

lines[]
    account_candidate
    debit
    credit
    currency
    counterparty
    evidence_reference
```

## Accounting Invariant

Every completed journal entry must satisfy:

```text
Σ Debit = Σ Credit
```

An entry that does not balance cannot progress as a valid accounting entry.

---

# 5. PCGE Categorization

## Responsibility

Assign the appropriate Moroccan PCGE accounts to the accounting lines.

This stage answers:

> What does this economic event represent in the Moroccan accounting system?

For example:

```text
Maroc Telecom invoice
```

may be classified as a telecommunications expense rather than simply "supplier payment."

The system may use:

* document type
* supplier identity
* customer identity
* bank description
* historical accounting treatment
* company activity
* previous journal entries
* VAT rules
* PCGE account structure
* accountant instructions

## Input

Candidate accounting entries.

## Output

Categorized accounting entries containing explicit PCGE accounts.

Example:

```text
journal_entry
    debit: telecommunications expense account
    debit: recoverable VAT
    credit: supplier Maroc Telecom
```

## Classification State

Each classification should expose confidence or review state.

For example:

```text
CONFIRMED
HIGH_CONFIDENCE
NEEDS_REVIEW
UNKNOWN
CONFLICT
```

## Important Separation

PCGE classification and reconciliation are different problems.

Classification asks:

```text
What is this transaction?
```

Reconciliation asks:

```text
Which real-world events correspond to each other?
```

These should not be collapsed into the same operation.

---

# 6. Customer and Supplier Matching

## Responsibility

Match accounting documents against open customer and supplier obligations.

This is essentially auxiliary-ledger reconciliation.

---

## 6.1 Customer Matching

Example:

Customer invoice:

```text
Invoice INV-100
Customer: ABC SARL
Amount: 12,000 MAD
```

Later bank transaction:

```text
Virement ABC SARL
12,000 MAD
```

The system should identify that the bank receipt probably settles that invoice.

Possible relationships include:

```text
one payment -> one invoice
one payment -> multiple invoices
multiple payments -> one invoice
partial payment -> invoice
overpayment
credit note -> invoice
```

---

## 6.2 Supplier Matching

Example:

Supplier invoice:

```text
Supplier: Maroc Telecom
Amount: 1,200 MAD
```

Bank line:

```text
PRLV MAROC TELECOM
1,200 MAD
```

The system should establish a candidate settlement relationship.

---

## Output

A matching state such as:

```text
MATCHED
PARTIALLY_MATCHED
UNMATCHED
AMBIGUOUS
OVERPAID
UNDERPAID
CONFLICT
```

And a relationship graph:

```text
invoice
payment
credit_note
bank_line
journal_entry
```

The matching layer should preserve the actual relationships rather than merely writing a boolean `matched=true`.

---

# 7. Bank Reconciliation

## Responsibility

Prove that the accounting representation of the bank account corresponds to the actual bank statement.

The system now has two sets:

```text
B = bank statement lines
L = accounting ledger lines affecting the bank account
```

The reconciliation problem is to establish relationships between elements of `B` and `L`.

Relationships may be:

```text
1 bank line <-> 1 ledger line

1 bank line <-> many ledger lines

many bank lines <-> 1 ledger line

many bank lines <-> many ledger lines
```

Examples include:

* grouped deposits
* batched card settlements
* bank fees
* partial payments
* transfer combinations
* split journal entries

## Matching Evidence

Matching may use:

```text
amount
date
value date
reference
counterparty
invoice reference
description
transaction direction
document evidence
semantic similarity
historical behavior
```

## Mathematical Constraint

For a reconciliation group:

```text
Σ bank amounts ≈ Σ ledger amounts
```

subject to the applicable tolerance rules.

In the ideal exact case:

```text
Σ bank amounts = Σ ledger amounts
```

## Output

Reconciliation groups containing explicit memberships.

Example:

```text
reconciliation_group_id

bank_members[]
ledger_members[]

total_bank_amount
total_ledger_amount
difference

status
confidence
evidence
reasoning
```

Possible states:

```text
RECONCILED
PARTIALLY_RECONCILED
NO_MATCH
AMBIGUOUS
CONFLICT
```

## Important Requirement

Reconciliation should be global where necessary.

The system should avoid independently selecting locally attractive matches that make the remaining transaction set impossible to reconcile.

---

# 8. Exception Review

## Responsibility

Collect anything the automated pipeline could not safely resolve.

The accountant should not have to inspect every transaction.

They should inspect exceptions.

Exceptions may include:

```text
missing document
unknown counterparty
unknown PCGE account
ambiguous classification
unmatched bank transaction
unmatched invoice
multiple possible reconciliation matches
amount mismatch
date mismatch
duplicate document
duplicate transaction
VAT ambiguity
balance inconsistency
contradictory evidence
```

## Exception Object

Each exception should explain:

```text
what is wrong
why the system could not resolve it
what evidence exists
what evidence is missing
what candidate resolutions exist
what decision the accountant needs to make
```

Example:

```text
Exception:
Bank transaction of 8,500 MAD from CLIENT XYZ.

Possible matches:
- Invoice 403: 8,500 MAD
- Invoice 417: 8,500 MAD

Both invoices are unpaid and within the reconciliation window.

Required decision:
Select the invoice settled by this payment.
```

The system should avoid presenting exceptions as generic "low confidence" events where a more descriptive diagnosis is possible.

---

# 9. Tax Controls

## Responsibility

Validate the bookkeeping state against applicable tax treatment before the period is considered complete.

For the bookkeeping pipeline this primarily includes validating information required for downstream tax reporting.

Examples include:

* collected VAT
* recoverable VAT
* non-recoverable VAT
* invoice tax amounts
* taxable bases
* supplier/customer tax information where required
* withholding-related entries where applicable

## VAT Relationship

For a standard taxable invoice:

```text
TTC = HT + VAT
```

The system should verify that extracted and recorded amounts are internally consistent.

At period level:

```text
VAT payable
=
VAT collected
-
eligible recoverable VAT
```

subject to applicable Moroccan tax rules.

The bookkeeping system does not necessarily need to file the tax declaration at this stage.

It must ensure the accounting state contains the information required to produce it correctly.

---

# 10. Accounting Controls

## Responsibility

Run deterministic and semantic checks over the resulting bookkeeping state.

These checks should happen before closing.

## Core Deterministic Controls

### Balanced journal entries

For every journal entry:

```text
Σ debit = Σ credit
```

### Bank balance

The accounting bank balance should reconcile to the bank statement balance after accounting for legitimate outstanding items.

### Opening/Closing continuity

For account `a`:

```text
closing_balance(a, period_t)
=
opening_balance(a, period_t+1)
```

unless an explicit opening adjustment exists.

### Customer controls

Look for:

* abnormal credit balances
* old unpaid invoices
* duplicate invoices
* unapplied receipts

### Supplier controls

Look for:

* abnormal debit balances
* duplicate invoices
* unpaid supplier obligations
* unapplied payments

### VAT controls

Compare:

* invoice VAT
* journal VAT
* VAT account balances
* VAT reporting totals

### Duplicate controls

Detect potentially duplicated:

* invoices
* receipts
* bank lines
* journal entries
* payments

---

# 11. Closing State

## Responsibility

Produce a coherent representation of the company accounting state after all activity for the period has been processed.

The closing state should be derivable from:

```text
Opening State
+
Period Transactions
+
Adjustments
=
Closing State
```

For every account `a`:

```text
C_a = O_a + D_a - K_a
```

where:

```text
C_a = closing balance
O_a = opening balance
D_a = debit movements
K_a = credit movements
```

The exact sign interpretation depends on account type, but the ledger arithmetic must remain consistent.

## Closing State Should Contain

At minimum:

```text
account balances
bank balances
customer balances
supplier balances
VAT balances
cash balances
loan balances
fixed asset balances
expense balances
revenue balances
unresolved exceptions
reconciliation state
supporting evidence references
```

## Closing Status

The period should have an explicit state.

For example:

```text
OPEN
PROCESSING
REVIEW_REQUIRED
READY_TO_CLOSE
CLOSED
```

A period should only become `CLOSED` after required controls have passed or authorized exceptions have been explicitly accepted.

---

# 12. Resulting Accounting Outputs

The closing state should be sufficient to generate standard accounting outputs without reconstructing information from the original documents again.

Expected outputs include:

```text
General Ledger
Trial Balance
Accounting Journals
Customer Ledger
Supplier Ledger
Bank Ledger
VAT State
Balance Sheet
Income Statement
Closing Balances
Opening State for Next Period
```

For Toro specifically, export adapters may additionally generate formats required by external accounting systems such as Sage 100.

---

# 13. Canonical State Flow

The implementation should conceptually support the following state progression:

```text
RAW_DOCUMENT
    |
    v
EXTRACTED_EVIDENCE
    |
    v
ACCOUNTING_EVENT
    |
    v
CANDIDATE_JOURNAL_ENTRY
    |
    v
PCGE_CATEGORIZED_ENTRY
    |
    v
CUSTOMER_SUPPLIER_MATCHING
    |
    v
BANK_RECONCILIATION
    |
    v
EXCEPTION_RESOLUTION
    |
    v
ACCOUNTING_AND_TAX_CONTROLS
    |
    v
CLOSING_STATE
```

Not every transaction needs every intermediate operation.

For example, a bank fee may not have a supplier invoice.

However, all material economic activity should eventually converge into the same accounting closing state.

---

# 14. Important Architectural Separation

The implementation should preserve the following conceptual boundaries:

### Evidence layer

What actually happened according to documents and external sources.

Examples:

```text
bank statement
invoice
receipt
payroll record
```

### Accounting representation layer

How those events are represented according to accounting rules.

Examples:

```text
journal entries
PCGE accounts
debit
credit
VAT
```

### Relationship layer

How economic events relate to one another.

Examples:

```text
invoice paid by bank transaction
credit note applied to invoice
multiple invoices paid by one transfer
```

### Reconciliation layer

Whether independent representations agree.

Examples:

```text
bank ledger vs bank statement
customer ledger vs customer payment
supplier ledger vs supplier payment
```

### State layer

The resulting state of the company after processing all accepted events.

These layers should not be collapsed into one LLM classification step.

---

# 15. Comparison Instructions for the Existing Codebase

Inspect the current implementation and map every existing component to the stages defined above.

Produce a gap analysis with the following structure:

```text
Stage
Current implementation
Status
Missing functionality
Incorrect assumptions
Relevant files/packages
Recommended change
```

Use these statuses:

```text
COMPLETE
PARTIAL
MISSING
IMPLEMENTED_DIFFERENTLY
UNCLEAR
```

Specifically determine whether the implementation currently supports:

1. immutable raw evidence
2. normalized source documents
3. normalized bank lines
4. extraction separated from accounting judgment
5. accounting-event construction
6. balanced journal-entry generation
7. explicit PCGE categorization
8. customer invoice matching
9. supplier invoice matching
10. partial-payment relationships
11. one-to-many and many-to-one matching
12. bank-to-ledger reconciliation
13. global reconciliation optimization
14. persistent reconciliation groups
15. descriptive reconciliation failures
16. explicit exception states
17. human exception resolution
18. VAT controls
19. accounting consistency controls
20. opening-state representation
21. deterministic closing-state calculation
22. explicit accounting-period lifecycle
23. closing balances
24. audit trail from closing-state values back to original evidence
25. generation/export of final accounting outputs

Do not assume a capability exists because a similarly named function, table, worker, or package exists.

Trace the actual data flow through the code and determine whether the stage is functionally implemented end to end.
