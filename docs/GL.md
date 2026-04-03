Operational PRD: Toro General Ledger (Toro GL)

Epic: The Transition from AI Wrapper to System of Record
Objective: Architect a phased migration from a read/write analytics layer (shadow_erp) to a fully native, multi-entity, GAAP-compliant General Ledger, ultimately replacing QuickBooks and NetSuite.

CORE ARCHITECTURAL PRINCIPLES (Phase 3 Targets)

To be a true General Ledger, the final state of the database must enforce these non-negotiable rules:

Double-Entry Mandate: Every transaction is a JournalEntry containing at least two JournalLines. The sum of Debits must exactly equal the sum of Credits (SUM(debit) - SUM(credit) = 0).

Absolute Immutability: Once a JournalEntry is posted, UPDATE and DELETE SQL commands are strictly forbidden at the database level. Corrections must be made via a new Reversing Journal Entry.

Period Locking: The gl_accounting_periods table tracks months/quarters. Once a period status is CLOSED, the database rejects any new JournalEntry with a date falling within that period.

Multi-Entity Hierarchy: The Chart of Accounts (CoA) and Ledgers must support a parent_company_id and subsidiary_id structure for instant consolidation.

PHASE 1: THE AI WRAPPER (Shadow ERP)

Status: Current State
Objective: Win the CPA's loyalty through AI automation (Fignode App) while offloading GAAP liability to Intuit/Xero.

1.1 Engineering Architecture

The Database: We maintain a shadow_erp schema. This is a flattened, single-entry table optimized for fast analytics, AI categorization, and mobile swiping.

shadow_transactions: id, amount, category, vendor, qbo_transaction_id

The Sync Engine: A bi-directional background worker that constantly polls Intuit's OAuth API.

The UX: The Junior Accountant swipes right on Fignode. The Toro Orchestration Engine updates the shadow_erp instantly for UI speed, and queues an API call to update the real QBO ledger in the background.

1.2 Go-To-Market (GTM)

Pitch: "We make your QuickBooks 10x faster using AI."

Adoption: Zero friction. The CPA connects the client in 30 seconds. If they cancel Toro, their QBO data remains perfectly intact.

PHASE 2: THE TREASURY WEDGE (Execution Layer)

Status: Next 12 Months
Objective: Capture the actual movement of money and consolidate liquidity for mid-market clients, making Toro the "Source of Truth" for cash, but still syncing to QBO for tax reporting.

2.1 Engineering Architecture

The Database: We introduce the toro_treasury schema.

master_accounts: Holds the consolidated funds for the Parent Company.

virtual_accounts: FBO (For Benefit Of) accounts mapped to OpCo, PropCo, HoldCo.

treasury_transfers: Immutable log of actual money movement.

The Integration: When OpCo pays a vendor via Toro Treasury, the treasury_transfers table records it. Toro's AI auto-categorizes it, and the Sync Engine pushes a completed Journal Entry down into QuickBooks.

Multi-Entity Handling: Toro handles the cash. QBO handles the accounting. The CEO gets a consolidated dashboard on Toro.

2.2 Go-To-Market (GTM)

Pitch: "Stop logging into 5 different bank accounts. Move your cash to Toro Treasury for instant Zero-Balance Account (ZBA) consolidation and free AP/AR automation."

Adoption: The CPA mandates the switch because Toro Treasury automatically categorizes 100% of transactions, eliminating the remaining bookkeeping work.

PHASE 3: THE SYSTEM OF RECORD (The Kill Shot)

Status: Long-Term Vision (Months 18-36)
Objective: Sever the Intuit/Xero APIs. Toro becomes the immutable, GAAP-compliant General Ledger.

3.1 Engineering Architecture: The Native GL

We deploy the toro_gl schema, enforcing strict double-entry accounting.

Table: gl_accounts (Chart of Accounts)

id (UUID)

tenant_id (UUID) - Ties to the Parent Company

subsidiary_id (UUID, Nullable) - For multi-entity isolation

account_type (Enum: Asset, Liability, Equity, Revenue, Expense)

currency (String)

Table: gl_journal_entries (The Header)

id (UUID)

tenant_id (UUID)

posted_date (Date)

status (Enum: DRAFT, POSTED, REVERSED)

audit_hash (String) - Cryptographic seal of the entry.

Table: gl_journal_lines (The Legs)

id (UUID)

journal_entry_id (UUID, Foreign Key)

account_id (UUID, Foreign Key)

debit_amount (Decimal)

credit_amount (Decimal)

Constraint: CHECK (debit_amount >= 0 AND credit_amount >= 0)

Trigger: Before insert on gl_journal_entries status change to POSTED, verify SUM(debit_amount) = SUM(credit_amount).

3.2 The Migration UX (The "Sever" Button)

We cannot force a migration; we must make it irresistible.

The Parallel Run: For 3 months, Toro runs the shadow_erp alongside the silent, background toro_gl. The system continually audits itself to ensure Toro's GL perfectly matches QBO's GL.

The Prompt: The CPA receives a notification: "Toro Native GL has matched QuickBooks with 100% accuracy for 90 days. Intuit is charging you $2,400/year for this client. Click here to migrate to Toro Native GL for free and cancel QuickBooks."

The Execution:

Toro imports all historical QBO data into toro_gl as permanent Journal Entries.

Toro revokes the QBO OAuth token.

The shadow_erp table is deprecated. The Fignode UI now reads/writes directly to the toro_gl via the Agentic Router.

3.3 Go-To-Market (GTM)

Pitch: "You don't need QuickBooks anymore. Toro is your Bank, your Ledger, and your Fractional CFO. One platform. One source of truth."

Adoption: By this point, the CPA and CEO live entirely inside the Fignode app and Toro Treasury. QuickBooks is just a ghost database costing them money. Severing it is a financial relief.