I would define a bookkeeping primitive as:

> **A bounded transformation of accounting state, with explicit inputs, outputs, rules, uncertainty, and human-review conditions.**

## 1. Document ingestion

**Purpose:** Bring accounting evidence into the system and associate it with the correct company and accounting period.

**Inputs:** invoices, receipts, bank statements, payroll files, contracts, cash sheets, credit notes, spreadsheets, ERP exports, emails.

**What it does:** identifies the company, source, document type, date received, duplicates, file validity and provenance.

**Output:** a registered piece of evidence ready for interpretation.

**Important boundary:** ingestion does not decide accounting treatment.

**Toro status:** likely mostly built.

---

# 2. Document extraction

**Purpose:** Convert unstructured evidence into structured facts.

An invoice might become:

* supplier
* invoice number
* invoice date
* due date
* subtotal
* VAT
* total
* currency
* line items
* payment details

A bank statement becomes individual bank transactions.

**Inputs:** registered evidence.

**Outputs:** structured factual observations with references back to the source document.

**Key challenge:** distinguish **what the document says** from **what accounting treatment it requires**.

Extraction should say:

> VAT amount = 200 MAD.

It should not yet say:

> Debit VAT recoverable account X.

That belongs downstream.

**Toro status:** likely substantially built.

---

# 3. Entity resolution

This primitive determines **who or what the evidence refers to**.

A transaction saying:

> VIREMENT STE ATLAS SARL

must resolve to:

> Supplier: Atlas SARL, supplier ID 482.

The primitive handles:

* suppliers
* customers
* employees
* shareholders
* banks
* government agencies
* payment processors
* merchants
* internal entities.

It must handle spelling variations, abbreviations, bank descriptions and duplicate entities.

**Inputs:** extracted names, identifiers, bank descriptions, ICE/IF numbers, previous history.

**Output:** canonical entity identity or unresolved entity.

This becomes extremely important because almost everything downstream becomes easier once counterparties are known.

**Toro status:** probably partial.

---

# 4. Transaction construction

This converts extracted economic facts into an **accounting event candidate**.

An invoice is not itself an accounting entry.

This primitive determines:

> Something economically happened that potentially requires bookkeeping.

Example:

Supplier invoice received:

* Supplier: Maroc Telecom
* Amount: 1,200 MAD
* Expense: 1,000
* VAT: 200
* liability created: 1,200

The primitive creates the economic event that later primitives classify and post.

It should also determine whether multiple pieces of evidence describe the **same event**.

For example:

Invoice + receipt + bank payment

are not necessarily three transactions.

They may be three pieces of evidence describing parts of one economic lifecycle.

**Toro status:** probably partial.

---

# 5. Categorization

This determines **which accounts represent the economic event**.

This is the primitive you already think about heavily.

It maps:

> economic meaning + company context + accounting policy + chart of accounts

to:

> accounting accounts.

For example:

AWS payment may become:

* cloud/software expense account
* supplier account
* VAT treatment
* bank account.

Categorization should operate against the **actual firm's chart of accounts**, not merely generic PCGE.

It should use:

* company history
* supplier history
* accountant conventions
* Moroccan PCGE structure
* evidence
* transaction semantics.

**Output:** proposed account assignments with confidence and rationale/evidence.

**Toro status:** already a major existing capability.

---

# 6. Tax treatment

This determines the **tax properties** of the transaction independently of basic categorization.

Examples:

* VAT applicable?
* VAT rate?
* VAT recoverable?
* VAT collected?
* exemption?
* withholding?
* deductible expense?
* special treatment?
* reverse charge where applicable?

This should be a separate primitive because:

> Account classification and tax treatment are related but not identical decisions.

A transaction may clearly be a professional service while its VAT treatment still requires separate reasoning.

**Output:** tax attributes attached to the accounting event.

**Toro status:** probably incomplete or embedded inside categorization today.

---

# 7. Customer allocation

This handles accounts receivable.

Suppose:

> Client ABC owes three invoices.

A payment of 14,000 MAD arrives.

This primitive determines:

* which customer made the payment,
* which invoice(s) it settles,
* whether it is partial,
* whether there is an overpayment,
* whether there is an advance,
* what remains outstanding.

It changes:

> customer subledger state.

This is different from bank reconciliation.

Bank reconciliation says:

> this bank line corresponds to this book event.

Customer allocation says:

> this money settles these customer obligations.

**Toro status:** probably partially present through reconciliation.

---

# 8. Supplier allocation

The accounts payable equivalent.

A company pays a supplier 42,000 MAD.

The primitive determines which:

* supplier,
* invoice,
* invoices,
* credit notes,
* partial liabilities

the payment settles.

It must support:

* partial payments,
* grouped payments,
* prepayments,
* supplier credits,
* withholding differences,
* disputed invoices.

Again, this changes the **supplier ledger**, not merely the bank reconciliation state.

**Toro status:** probably partial.

---

# 9. Bank reconciliation

This proves that movements recorded in the books correspond to movements occurring at the bank.

It handles:

* one bank line to one book line,
* one-to-many,
* many-to-one,
* partial relationships,
* fees,
* timing differences,
* duplicate candidates,
* missing book entries,
* missing bank entries.

Core question:

> Does the bank's external state agree with the accounting ledger?

Your optimizer + semantic hypothesis architecture already belongs here.

**Output:** reconciled match groups plus unresolved exceptions.

**Toro status:** strongly developed.

---

# 10. Petty-cash reconciliation

Conceptually similar to bank reconciliation, but the external source of truth is different.

You have:

> physical/internal cash state

versus:

> accounting cash ledger.

It needs to reason about:

* cash received,
* cash spent,
* employee advances,
* receipts,
* reimbursements,
* opening cash,
* closing counted cash,
* unexplained shortages/overages.

The primitive ultimately asks:

> Given opening cash + movements, should the recorded closing balance equal physically observed cash?

This deserves separate treatment because cash evidence is weaker and operational processes differ dramatically from banks.

**Toro status:** probably not built.

---

# 11. Inter-account reconciliation

This handles movements **inside the company**.

Examples:

* Bank A to Bank B.
* Bank to cash.
* Corporate card to bank.
* payment processor clearing account to bank.
* internal treasury movement.

Without this primitive, Toro can mistakenly treat both sides as independent income/expenses.

The primitive recognizes:

> These two apparently separate transactions are opposite sides of the same internal movement.

Its primary invariant is:

> Internal transfers do not create economic income or expense.

**Toro status:** may exist implicitly but likely not as a first-class primitive.

---

# 12. Settlement allocation

I would keep this separate from reconciliation because it is more general.

Settlement allocation answers:

> How does a monetary settlement distribute across underlying obligations?

Examples:

A 100,000 MAD customer payment covers:

* invoice A: 30k
* invoice B: 50k
* invoice C: 20k

Or:

A card processor deposits 97,000 MAD where:

* customer sales = 100,000
* processor fee = 3,000.

Or:

Supplier payment = invoice total minus withholding.

This primitive decomposes settlements mathematically and economically.

Your CP-SAT work can become extremely valuable here.

**Toro status:** some underlying machinery likely exists.

---

# 13. Accrual and deferral

This handles the difference between:

> when money moves

and:

> when revenue/expense economically belongs.

Examples:

* annual insurance paid upfront,
* rent paid quarterly,
* accrued utilities,
* prepaid subscriptions,
* revenue received in advance,
* invoices arriving after period end.

The primitive determines:

* recognition start/end,
* allocation across periods,
* remaining prepaid/accrued balance,
* adjusting entries.

This is essential once Toro moves from transaction processing toward proper period accounting.

**Toro status:** probably not yet built.

---

# 14. Fixed-asset accounting

Some purchases should not immediately become expenses.

This primitive identifies and manages assets.

Lifecycle:

**purchase**
→ capitalization decision
→ asset record
→ useful life
→ depreciation schedule
→ periodic depreciation
→ disposal/sale
→ gain/loss
→ impairment where appropriate.

It needs company policy because capitalization thresholds can differ.

The primitive therefore turns:

> "Company bought a machine for 150,000 MAD"

into an asset whose accounting consequences unfold over multiple periods.

**Toro status:** probably not built.

---

# 15. Payroll posting

Toro does not necessarily need to calculate payroll itself.

The primitive can initially consume payroll outputs.

It transforms:

> payroll register

into accounting consequences:

* gross wages,
* employer contributions,
* employee deductions,
* taxes,
* CNSS liabilities,
* net salaries,
* employee advances,
* payments.

Then later reconciliation confirms those obligations were actually paid.

The important distinction is:

> payroll calculation is an HR/payroll domain; payroll posting is bookkeeping.

**Toro status:** likely not built.

---

# 16. Inventory and cost-of-goods accounting

Only relevant for companies carrying inventory.

The bookkeeping system must represent:

* purchases,
* inventory receipts,
* inventory movements,
* sales,
* returns,
* shrinkage,
* adjustments,
* COGS.

This primitive reconciles economic inventory activity with accounting state.

For simple companies, Toro may receive inventory results from an external POS/ERP rather than operate inventory itself.

So V1 could be:

> ingest inventory valuation/movement output and correctly post its accounting consequences.

Rather than building an inventory management system.

**Toro status:** probably not built.

---

# 17. Currency treatment

Foreign-currency transactions introduce another state dimension.

The primitive handles:

* transaction currency,
* functional/accounting currency,
* exchange rate at recognition,
* exchange rate at settlement,
* unrealized differences,
* realized FX gain/loss,
* foreign-currency account balances.

Example:

Invoice created when EUR = 10.8 MAD.

Paid when EUR = 11.1 MAD.

That difference has accounting consequences.

This primitive isolates those consequences rather than contaminating basic reconciliation logic.

**Toro status:** likely partial or absent.

---

# 18. Correction and adjustment

Accounting systems need a formal mechanism for changing previously recorded conclusions.

Examples:

* wrong account used,
* wrong supplier,
* duplicated entry,
* invoice cancelled,
* credit note received,
* VAT corrected,
* previous-period mistake,
* bank transaction misclassified.

This primitive should distinguish:

**changing knowledge**

from

**rewriting history.**

Accounting generally requires traceable correction.

So Toro should understand operations like:

* reversal,
* replacement,
* reclassification,
* adjustment,
* correction entry.

Every correction should also become valuable feedback for the proprietary accounting intelligence.

**Toro status:** likely some workflow exists, but probably not yet formalized as a primitive.

---

# 19. Accounting control

This primitive asks:

> Even if individual transactions look correct, does the resulting accounting state make sense?

Think of it as accounting invariants.

Examples:

* debits = credits,
* duplicate invoice numbers,
* impossible negative balances,
* suspicious suspense balances,
* VAT inconsistencies,
* missing counterparties,
* missing supporting documents,
* duplicate payments,
* aging anomalies,
* unexplained account movements,
* impossible dates,
* unusual account combinations.

This is not reconciliation.

It is **validation of the resulting state**.

Some controls are universal mathematical constraints.

Others are accounting-policy rules.

Others require semantic anomaly detection.

**Toro status:** some validator architecture exists, but this can become much larger.

---

# 20. Period reconciliation

This is reconciliation at the **system level** rather than transaction level.

Before a month or year is considered clean, Toro asks whether all important accounting subsystems agree.

For example:

**Customer subledger total = customer control account**

**Supplier subledger total = supplier control account**

**Bank reconciled balance = bank GL balance**

**Cash count = cash ledger**

**VAT records = VAT accounts**

**Payroll liabilities = payroll control accounts**

**Fixed asset register = fixed asset GL accounts**

This is essentially:

> Does every relevant representation of the company's financial state converge to the same answer?

This primitive becomes extremely important for ESE because it gives you a verified state boundary.

**Toro status:** probably only partially represented today.

---

# 21. Closing

Closing is the primitive that converts:

> continuously changing bookkeeping state

into:

> an accepted accounting state for a defined period.

It does not necessarily mean statutory year-end closing.

You can have:

* monthly close,
* quarterly close,
* annual close.

Closing checks that:

* required evidence is processed,
* exceptions are resolved or explicitly accepted,
* reconciliations are complete,
* control accounts agree,
* accruals/deferrals are posted,
* depreciation is posted,
* payroll is reflected,
* taxes are reflected,
* unresolved uncertainties are known.

Then the system creates something conceptually very important:

> **Closing State(t)**

which becomes:

> **Opening State(t+1)**.

This is exactly where your bookkeeping architecture naturally connects to ESE.

**Toro status:** conceptually already part of your architecture, but the complete accounting close probably isn't built.

---

# The deeper structure

Looking at these 21, I don't actually think Toro needs **21 fundamental runtime primitives**.

They fall into about six families:

### Evidence primitives

**1. Ingestion**
**2. Extraction**
**3. Entity resolution**

Convert reality into trustworthy structured evidence.

### Interpretation primitives

**4. Transaction construction**
**5. Categorization**
**6. Tax treatment**

Determine what economic event occurred and how accounting should represent it.

### Allocation primitives

**7. Customer allocation**
**8. Supplier allocation**
**12. Settlement allocation**

Determine which obligations monetary activity belongs to.

### Reconciliation primitives

**9. Bank reconciliation**
**10. Petty cash reconciliation**
**11. Inter-account reconciliation**
**20. Period reconciliation**

Determine whether two representations of state agree.

### Accounting-state transformation primitives

**13. Accrual/deferral**
**14. Fixed assets**
**15. Payroll posting**
**16. Inventory/COGS**
**17. Currency treatment**
**18. Corrections**

Transform state according to specialized accounting rules.

### State integrity primitives

**19. Control**
**21. Closing**

Determine whether the state is valid enough to accept.

And that suggests something much deeper for Toro:

> **Bank reconciliation may not really be a fundamental primitive. Reconciliation itself is the fundamental primitive.**

Bank reconciliation is merely:

**bank state ↔ ledger state**

Petty cash is:

**physical cash state ↔ ledger state**

Customer reconciliation:

**invoice/receivable state ↔ payment state**

Supplier reconciliation:

**payable state ↔ settlement state**

Period reconciliation:

**subledger state ↔ general ledger state**

That abstraction could become very important when we eventually define the software architecture.
