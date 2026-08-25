# Product Requirements Document: Immutable Bank Reconciliation State (V1)

## Status: Specification approved; implementation partially complete

## 1. Objective

Toro must reconcile each bank account month by month without treating reconciliation as a side effect of transaction classification.

The V1 system creates an immutable, auditable reconciliation state for each bank account and accounting period. Each month inherits only the unresolved boundary between the bank and the books from the preceding state. The system must therefore support a reliable chain:

```text
OPENING STATE
  -> transaction understanding
  -> reconciliation
  -> CLOSING STATE
  -> next month opening state
```

The objective is to make a bank reconciliation reproducible from canonical evidence, including the bank statement, the Shadow ERP ledger, and explicit reconciliation links.

## 2. Product boundary

### In scope

- One reconciliation chain per `realm_id` and physical bank account.
- A monthly state for each bank account, with immutable revisions.
- Migrated initial states supplied and approved by an accountant.
- Canonical bank-statement records, canonical Shadow ERP journal records, and reconciliation references between them.
- Deterministic closing calculations, carry-forward, audit history, and human review.
- Automated one-to-one matching and human-created grouped matching.

### Out of scope for V1

- Replacing Sage as the accountant-facing imported ledger.
- Automatic grouped matching.
- Automatically deciding whether incomplete evidence is economically correct.
- Changing historical closed states in place.
- Treating a classification confidence score as proof of reconciliation.

## 3. Accounting authority and source records

`shadow_erp` is Toro's authoritative accounting source. Sage is an external destination that receives derived export output from Shadow ERP; it is not the source used to calculate Toro's reconciliation state.

The reconciliation state is not a transaction store or a duplicate ledger. It points to immutable canonical records:

| Canonical record | Purpose |
| --- | --- |
| Source bank-statement document | `toro_core.documents` owns the imported file, OCR payload, statement metadata, and provenance. |
| Bank statement line | Normalized observed bank movement pointing to its source `toro_core.documents` record. |
| Shadow ERP journal entry and journal line | Represents a posted book movement and provides the book bank-account balance. |
| Reconciliation match | Records the evidenced relationship between one or more canonical bank lines and one or more canonical book lines. |
| Reconciliation state membership | Records only the reconciliation disposition of a referenced canonical record for one state; it does not copy amount, date, description, counterparty, or journal data. |

The system must create canonical Shadow ERP journal entries and lines before reconciliation relies on book movements. `fignode.staging_transactions` remains an ingestion and transaction-understanding record, not the authoritative book ledger.

## 4. Four-stage model

### 4.1 Opening state

Before processing a new statement, the reconciliation engine loads the latest usable state for the same bank account.

The opening state answers only:

- Does a prior state exist?
- What were the prior statement and book balances?
- Which canonical bank or book movements remained unreconciled?
- Is the prior state a valid opening for this period?

It does not load all invoices, historical transactions, classifications, or sales history.

The first state for a bank account must be a `MIGRATED_BASELINE`: an accountant-approved baseline containing the known opening bank and book balances and references to every known outstanding canonical record. A missing predecessor must never be represented as a reconciled zero-difference opening.

### 4.2 Transaction understanding

The PCM cash DAG understands a bank transaction's economic and accounting meaning:

```text
bank transaction
  -> inflow / outflow
  -> economic intent
  -> accounting treatment
  -> proposed journal / accounts
```

It may classify a transaction, propose an accounting treatment, request evidence, or hold an ambiguous transaction. It does not declare the transaction reconciled.

### 4.3 Reconciliation

The reconciliation engine connects a classified bank movement to accounting evidence: a journal line, invoice, receipt, customer or supplier balance, payroll record, tax record, loan schedule, or other canonical accounting evidence.

It determines whether the bank movement and book movement are accounted for, matched, unresolved, or eligible for carry-forward. Arithmetic, matching lifecycle, and closing validation are deterministic service behavior, not DAG or LLM decisions.

### 4.4 Closing state

Closing creates a new immutable state that identifies:

- reconciled movements;
- classified but unreconciled movements;
- missing evidence and human-review items;
- outstanding items carried forward;
- final bank and book positions;
- whether the reconciliation equation balances exactly.

The latest closed state is the next month’s opening state.

## 5. Immutable state model

Each state is a snapshot of a bank account's reconciliation at one specific point in time.

Required state properties:

- immutable UUID `id`;
- deterministic `state_hash` over the state header, referenced evidence IDs, dispositions, match IDs, calculated balances, and predecessor ID;
- `realm_id`, `bank_account_id`, `period_key`, currency, and revision number;
- `status`: `OPEN` or `CLOSED`;
- `previous_state_id`, pointing to the immediately preceding snapshot;
- creation metadata and optional supersession metadata.

The chain is linear. A first snapshot for a month points to the latest state from the prior month. Each further revision of that month points to the immediately prior revision. No state row is updated after creation:

```text
June CLOSED r3 -> July OPEN r1 -> July OPEN r2 -> July CLOSED r3 -> August OPEN r1
```

Closing is also a new snapshot: it creates a `CLOSED` revision whose predecessor is the latest `OPEN` revision.

If a closed state requires correction, Toro creates a superseding `OPEN` state rather than mutating the closed record. States derived from the superseded chain remain historically visible but are invalidated as reusable openings until they are rebuilt from the correction chain.

## 6. Balances, precision, and closure

All monetary calculations use signed `big.Int` values in fixed units of 0.0001 MAD. Inputs that cannot be represented at four decimal places are rejected or held for correction. Floating-point values must not be used in reconciliation state calculation or equality checks.

For ordinary book-side outstanding movements, the reconciliation engine calculates:

```text
expected bank closing balance
  = book bank-account closing balance
  + book inflows not yet visible at bank
  - book outflows not yet visible at bank
```

The engine must also include unresolved bank-side movements using the appropriate signed treatment. The exact calculation is derived from linked canonical records and state memberships, not from duplicated transaction amounts.

A state may close only when the statement closing balance equals the calculated expected bank balance exactly at the four-decimal precision.

A balanced state may close with valid unresolved items. Those items must be explicit state memberships, marked for carry-forward, and included in the next month’s opening state.

## 7. Matching and evidence

### 7.1 V1 automatic matching

V1 automatically creates only one-to-one match candidates. A candidate requires:

- equal amount at fixed precision;
- compatible direction;
- a date difference within the configured window for its item type.

Normalized reference and counterparty information rank otherwise valid candidates; neither may override an amount or direction mismatch. Ambiguous candidates require human review.

### 7.2 Date windows

Date tolerance is configuration owned by the realm and item type, with a conservative default. V1 must support separately configured rules for, at minimum:

- card/TPE settlements;
- cheques and bills;
- internal transfers;
- generic bank movements.

The engine records the dates used and the applicable rule when it creates or proposes a match.

### 7.3 Grouped matches

The schema supports one-to-many and many-to-one match groups from the outset. In V1 an accountant creates these groups. The system validates the signed totals, currency, and member uniqueness before accepting the group.

### 7.4 Required provenance

Every unresolved or carry-forward item must reference a canonical origin and expose, through that origin, an amount, direction, date, currency, and source reference. Counterparty and journal metadata are optional evidence, but provenance is mandatory.

## 8. Status model

The following concepts remain distinct:

| Status/concept | Meaning |
| --- | --- |
| Classified | The DAG identified economic intent. |
| Journal proposed | Toro proposed accounting entries. |
| Book posted | A canonical Shadow ERP journal line exists. |
| Matched | Evidence links a bank movement and a book movement. |
| Reconciled | The movement is included in a balanced closed reconciliation state. |
| Carried forward | The linked canonical record remains unresolved in a closed state and becomes opening context. |
| Missing evidence / human review | The system cannot complete a deterministic match or accounting conclusion. |

`OPEN` and `CLOSED` apply only to reconciliation states, not to the bank movement's accounting classification.

## 9. Account configuration

Bank reconciliation resolves the book bank account from `shadow_erp.bank_accounts.ledger_account_code`. It must not assume a hard-coded `514100` account.

Treasury and PCGM accounts such as 5115, 5111, and 5143 are resolved through a per-realm chart-of-accounts mapping. PCM helpers may identify a likely treasury mechanism, but the reconciliation engine validates it against configured accounts and canonical evidence.

## 10. Operational requirements

- State creation and closing must be idempotent.
- Concurrent creation of a new snapshot for the same bank-account/month chain must be serialized.
- Every state hash must be independently reproducible from immutable linked evidence and the state’s recorded calculation inputs.
- A closed state cannot be changed or deleted through normal application behavior.
- The system must retain enough linkage to explain every closing-balance difference and every carried-forward item to an accountant.

## 11. Acceptance criteria

1. An accountant can create an initial migrated baseline only with valid bank/book balances and linked outstanding records.
2. July can load June's unresolved records by reference, without copying their financial fields into July state storage.
3. Editing an OPEN reconciliation creates a new state with a new ID and hash; the earlier state remains readable.
4. Closing creates a new CLOSED snapshot and never changes the prior OPEN snapshot.
5. A reconciliation with a non-zero four-decimal difference cannot close.
6. A balanced reconciliation with valid outstanding cheques, transfers, or card settlements can close and carries those references into the next period.
7. A valid 1:1 exact amount/direction/date-window pair becomes an automatic match candidate; near matches and duplicate candidates require review.
8. A manually created grouped match is accepted only when its signed canonical totals reconcile exactly.
9. Correcting a closed month creates a superseding OPEN chain; later states are not rewritten and cannot be reused as openings until rebuilt.
10. The PCM DAG can propose classification and journal treatment, but cannot independently mark a bank movement reconciled.

## 12. Relationship to existing PRDs

This PRD supersedes the stateful bank-reconciliation model described in `docs/prds/rapprochement bancaire.md`. It does not supersede that document's broader Moroccan document-chain requirements for BC, BL, facture, and payment evidence. Those documents remain possible sources of reconciliation evidence under this model.

# Implementation handoff — 21 August 2026

## Status and deployment posture

Milestones 1-8 are **implemented and verified locally**. The Postmark-selected PCM workflow now creates canonical bank evidence, persists a reviewable Stage-2 proposal, and posts an explicitly approved Shadow ERP journal. Separate human-authorized workflows prepare/revise/close/correct states and manage matches/review. A migration-backed Atlas July acceptance test covers journal posting, exact matching, a June baseline, concurrent/idempotent July close, and the derived August opening.

`051_bank_reconciliation_state.sql` has **not been deployed to a shared environment and the new workflow blueprints have not been enabled there**. It applied successfully to isolated PostgreSQL 17 during acceptance. Do not add a corrective migration for changes to this file until it has been applied to a shared environment.

The source of truth remains Shadow ERP. Sage is an import/export destination; it is not queried as the reconciliation ledger.

## Target versus current runtime flow

Target flow:

```text
document/email -> document readiness -> PCM intake -> canonical bank lines
              -> Stage-2 DAG (classification + proposed journal)
              -> posted Shadow ERP journal lines -> reconciliation engine
              -> immutable closing state -> next month's opening state
```

Implemented flow:

```text
Postmark -> Dynamic Agent DB workflow selection -> document readiness
         -> idempotent PCM statement intake -> deterministic enrichment
         -> Stage-2 classification/proposed-treatment DAG -> human review
         -> canonical Shadow ERP journal posting

explicit lifecycle event -> human authorization -> prepare/revise/close/correct
                         -> matching/review -> immutable next opening
```

Inbound email remains incremental and cannot close a reconciliation period. Closing and correction require distinct accountant-authorized workflow topics.

The normal production entry route is `worker.inbox.email.postmark_inbound`, which registers documents and asks the Dynamic Agent to trigger a workflow. For deterministic testing, `tap/cmd/trigger_workflow/main.go` publishes directly to `events.accounting.1.pcm_workflow`. The active workflow definition is `tap/workflows/pcm_workflow.yml`.

## Implemented foundation

### Schema and generated database layer

The following files are present and generated code is current:

- `sql/schema/051_bank_reconciliation_state.sql`
- `sql/queries/bank_reconciliation.sql`
- `go/internal/database/bank_reconciliation.sql.go`
- changes to `go/internal/database/models.go`, `go/internal/database/querier.go`, and `tap/internal/database/models.go`

Migration 051 establishes these concepts:

| Concept | Current implementation | Important boundary |
| --- | --- | --- |
| Bank evidence | `shadow_erp.bank_statement_lines` | It references the canonical `toro_core.documents` statement via `source_document_id`; it does **not** duplicate a `bank_statements` document table. |
| Book evidence | `shadow_erp.journal_entries` and `journal_lines` | Accepted, hash-stable proposals post here with source and approval provenance. Sage export is deferred. |
| Reconciliation evidence | match groups and members | The matcher creates reviewable 1:1 candidates; authorized users confirm candidates or create exact grouped matches. |
| Snapshot state | states, memberships, invalidation events | Memberships reference canonical bank/journal lines instead of copying amounts, dates, or descriptions. |
| Configuration | per-type matching and ageing windows | Global V1 defaults are seeded and realm overrides take precedence. |

State headers and state memberships are protected from `UPDATE` and `DELETE` by database triggers. The model is append-only: a changed OPEN snapshot is represented by a new state, and a correction to CLOSED history creates a superseding chain plus invalidation events.

### Domain code

`go/internal/services/accounting/bank_reconciliation_state.go` provides pure domain primitives:

- `big.Int` money at a fixed scale of four decimal places;
- exact closing-difference calculation;
- validation for state snapshot inputs, linked evidence references, and carrying-forward;
- deterministic SHA-256 state hashing;
- eligibility checking for exact 1:1 matches by currency, direction, amount, and date window.

`go/internal/services/accounting/bank_reconciliation_state_test.go` covers the pure money, close, snapshot, hash, and 1:1 contracts. `bank_reconciliation_service.go` implements the transactional persistence lifecycle, and the Atlas integration test exercises it against PostgreSQL.

### PCM changes already made

The PCM tool was changed so it no longer claims that a transaction is reconciled:

- `transit_reconciler` now emits `transit_match_required` rather than `transit_reconciled`.
- pending cheque/instrument handling emits `instrument_match_required`.
- bank-account configuration is passed into the PCM DAG payload as account ID, ledger code, currency, and source document IDs.
- the enrichment tool can hold a transaction with `HOLD_BANK_ACCOUNT_CONFIGURATION` when there is no configured ledger code.

`PcmWorker` now attempts to resolve bank-account context and publishes that context in its proof to the orchestrator. This is plumbing only, not reconciliation.

## Historical pre-implementation gap analysis

The numbered gap analysis below is retained as design history and is superseded by the implementation update above and the completed checklist below.

These items are blockers, not optional polish.

### 1. Bank account resolution is unsafe for multi-bank realms

`PcmWorker.resolveBankAccountContext` currently accepts a realm only when it has exactly one configured bank account. It does not inspect OCR-extracted account number, RIB, IBAN, statement issuer, or document metadata to select the correct account. A realm with two accounts is put on `HOLD_BANK_ACCOUNT_RESOLUTION`.

Replace this temporary safeguard with a deterministic resolver that:

1. reads canonical document/statement metadata;
2. normalizes RIB/IBAN/account number values;
3. finds precisely one `shadow_erp.bank_accounts` row for the realm;
4. holds with explainable candidates on zero or multiple matches; and
5. persists the selected `bank_account_id` on every canonical bank line.

Never silently choose the first account.

### 2. No canonical bank-line ingestion exists

`PcmWorker` currently writes only `fignode.staging_transactions`. No worker inserts `shadow_erp.bank_statement_lines`, extracts statement period/opening/closing balance, validates currency, or links a staging row to a canonical line.

Implement a dedicated opening/intake activity (suggested name: `accounting.bank_reconciliation.opening_state_prepare`) that, for each statement document:

- validates document identity and statement metadata;
- resolves the bank account;
- normalizes and inserts lines idempotently using `(source_document_id, line_index)`;
- retains source line ordering and the canonical document reference;
- extracts/records statement opening and closing balances at a suitable canonical location; and
- emits a hold such as `HOLD_STATEMENT_METADATA` instead of creating a period state when identity, currency, balances, or coverage are unreliable.

The schema currently stores line evidence but does not yet define a canonical statement-header metadata record. Decide whether the document extraction payload is sufficient or whether 051 should add a narrow metadata table keyed by `source_document_id` before deployment. Do not recreate `shadow_erp.bank_statements`; `toro_core.documents` remains canonical for the document itself.

### 3. The ledger-posting boundary does not exist

The DAG can propose a treatment, but nothing converts accepted proposals into `shadow_erp.journal_entries` and `journal_lines`. Consequently `book_posted` cannot be true and no book balance can be independently reconstructed.

Implement a posting activity after the Stage-2 DAG that:

- consumes only a completed proposed accounting treatment;
- creates balanced canonical journal entries/lines idempotently;
- records provenance back to the source bank line/staging transaction/DAG result;
- uses configured bank and chart-of-account mappings, never PCM hard-coded account codes;
- permits a proposal to remain `journal_proposed` / reviewable rather than forcing a posting; and
- changes Sage/Excel export to read posted Shadow ERP journal lines, not staging transactions.

The provenance relation between a canonical bank line, staged input, DAG proposal, and posted journal must be designed before implementation. Existing columns alone are not enough to explain all of those links.

### 4. The state lifecycle service is missing

There is no transactional application service for migrated baselines, opening state loading, OPEN revisions, closing, invalidation, or carry-forward.

Implement it with explicit operations:

1. create a `MIGRATED` or `INITIALIZED_UNRECONCILED` baseline with linked evidence;
2. derive an OPEN state for a period from the valid latest closed predecessor;
3. append a replacement OPEN revision rather than mutating an existing state;
4. calculate all balances from referenced canonical evidence using four-decimal values;
5. create a new CLOSED snapshot only when the difference is exactly zero;
6. create carried-forward memberships for unresolved items; and
7. correct a closed period through a superseding OPEN chain and explicit invalidation of later dependent states.

It needs a transaction, an advisory lock or equivalent per `(realm_id, bank_account_id, period_key)`, idempotency keys, and deterministic retry behavior. The supplied SQL queries are only a starting point; they do not perform the lifecycle atomically.

### 5. Matching and reconciliation engine are missing

No code writes or confirms match groups. The initial scope is automated 1:1 only, plus explicit accountant-created grouped matches.

The engine must:

- query only canonical bank and journal movements for the matching bank account/currency;
- use exact four-decimal amounts, compatible direction, and configured date windows;
- score reference/counterparty evidence only after eligibility;
- create candidates without automatically resolving ambiguous duplicates;
- validate grouped-match signed totals exactly and require an explicit actor/action;
- distinguish `classified`, `journal_proposed`, `book_posted`, `matched`, `reconciled`, and `carried_forward`; and
- apply ageing/escalation policy rather than carrying forward indefinitely.

No ageing thresholds, policy defaults, or configuration seed data have been agreed or implemented. This remains a required product decision before closing can be automated.

### 6. The active DAG is not a valid Stage-2 terminal graph

`go/internal/erp/ase/dags/pcm_bank_cash_accounting_dag.yml` contains the old reconciliation-oriented routing and a partially commented graph:

- inflow categories still route toward nodes such as `customer_reconciler` that are commented out;
- outflow categories currently route to `debug_terminal` while intended treatment routes are commented out;
- most proposed-journal/accounting-treatment nodes are still commented;
- the file is already modified in the working tree, so do not overwrite it blindly.

Refactor it only after defining the stable proposed-treatment output contract. Its terminal result must be something like `PROPOSED_ACCOUNTING_TREATMENT`, `HOLD_*`, or `HUMAN_REVIEW`; it must not set a reconciled status, create a state, or make final evidence assertions. Every active edge must resolve to an active node, and DAG validation tests must prove that no intent category leads to a debug-only terminal in normal processing.

### 7. Context can still be lost in the existing workflow

The PCM worker now publishes bank context, but the contracts across `run_enrichment` and `ase_bridge` do not yet formally declare and preserve `bank_account_id`, currency, source document IDs, canonical bank-line IDs, statement period, or state ID. No canonical bank-line IDs exist yet.

Define a versioned workflow payload/proof contract and make every intermediary copy required reconciliation context. Test it end-to-end from Postmark input and the deterministic `trigger_workflow` command. Do not depend on an LLM Dynamic Agent decision to prove the core accounting pipeline.

### 8. Money/date boundaries remain inconsistent

The reconciliation domain uses `big.Int` with scale 4, but existing PCM helpers still use `float64`. Float values must never cross into the canonical bank, journal, match, or state boundary. Add explicit conversion/validation at ingestion and posting boundaries.

The enrichment logic still uses `time.Now()` for a transaction date in at least one path. It must use the canonical bank line's operation/value date according to the configured item type. Statement end date, document date, bank operation date, bank value date, and journal-entry date must remain separately available.

### 9. Schema integrity still needs review before deploying 051

The migration correctly prevents direct changes to state headers/memberships, but it does not yet enforce all cross-table invariants in the database:

- a previous/superseded state is not DB-constrained to the same realm, bank account, and valid chain;
- a membership reference is not DB-constrained to a bank/journal item of the same realm and bank account;
- bank lines and posted journal lines are not themselves trigger-enforced immutable;
- invalidating a predecessor does not automatically mark dependent states unusable; read queries merely exclude directly invalidated states;
- match groups/members are not yet locked against changes after inclusion in a CLOSED state;
- hash coverage must be reviewed: it should include every immutable semantic input needed to reproduce a snapshot, including revision/supersession policy if those are declared part of identity.

Choose which invariants belong in PostgreSQL triggers/constraints and which belong in the transactional lifecycle service, then write adversarial integration tests for both. Do not assume application code alone will preserve auditability.

## Completion plan for the next agent

Implement in this order. Each milestone should be independently testable and should leave the production workflow disabled until the next prerequisite is complete.

1. **Finish the canonical evidence design.** Resolve the statement-header decision, source/provenance links, exact bank-account identifiers, and configured account mappings. Amend undeployed migration 051 and regenerate SQLC. DONE
2. **Implement statement intake and account resolution.** DONE
3. **Define and implement the Stage-2 output contract.** DONE
4. **Implement canonical journal posting.** DONE. Sage export remains intentionally deferred; the legacy staging export cron is disabled by default and no new workflow invokes it.
5. **Implement the reconciliation persistence service.** DONE
6. **Implement matching and review.** DONE
7. **Add separate lifecycle workflows.** DONE locally; shared-environment blueprint registration remains a deployment action.
8. **Verify the whole path.** DONE in unit/contract tests and an isolated PostgreSQL 17 Atlas July acceptance run. Shared deployment of 051 and workflow enablement remain operator actions.

## Required tests and acceptance cases

At minimum add integration tests for:

- document-account matching by RIB/IBAN/account number, including ambiguity holds;
- idempotent bank-line ingestion from the same source document;
- metadata hold for missing/low-confidence statement coverage or balances;
- payload preservation through all workflow stages;
- every active DAG route reaching a proposed-treatment/review terminal;
- balanced and unbalanced journal posting, plus export reading canonical posted lines;
- migrated baseline, derived next-month opening, OPEN replacement revision, immutable CLOSED snapshot, and correction chain;
- concurrent state-create/close attempts and retry/idempotency behavior;
- exact four-decimal arithmetic (including zero-difference close failure/success);
- valid 1:1 match, duplicate ambiguity, date-window variants, and manual grouped total validation;
- carry-forward ageing and explicit human-review escalation; and
- a full June closing -> July opening scenario with outstanding card settlement, cheque, transfer, and customer receipt.

Useful current commands (the repository's vendor mode is not sufficient for these targeted packages):

```sh
cd go && GOWORK=off go test -mod=mod ./internal/services/accounting
cd go && GOWORK=off go test -mod=mod ./internal/erp/ase/domain_tools/pcm_cash
cd go && GOWORK=off go test -mod=mod ./internal/workers
```

The migration-backed acceptance test is `TestAtlasJulyPersistencePath`; set `BANK_RECONCILIATION_TEST_DATABASE_URL` to a database with migration 051 applied. It proves journal, matching, lifecycle persistence, concurrent idempotency, close, and next opening.

## Working-tree and ownership notes

Do not discard unrelated work. At the time of this handoff, the following existing user-owned or concurrent modifications were present: `docs/10x.md`, `go/internal/erp/ase/dags/pcm_bank_cash_accounting_dag.yml`, `go/internal/workers/document_readiness_worker_test.go`, `go/internal/workers/e2e_dag_test_workflow_test.go`, and `tap/cmd/dag_test/`. Reconcile the DAG edits intentionally with the Stage-2 redesign rather than replacing them wholesale.

The uncommitted schema, queries, generated SQLC code, accounting domain code, PCM changes, and this PRD are one partial unit of work. Review their API/schema names together before renaming or splitting them.
