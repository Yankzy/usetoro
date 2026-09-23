# PRD: ASE Bank Interpretation and Bookkeeping Execution Integration

**Status:** Revised after implementation audit; partially implemented  
**Audit date:** 15 September 2026  
**Priority:** High  
**Owners:** Bookkeeping State, ASE, Django Ledger, NATS integration  
**Implementation audience:** Gemini or another implementation agent  

## 1. Decision summary

Toro must use the generic Go Autonomous Semantic Engine (ASE) for two distinct jobs:

1. **Book-item categorization:** determine the entity-valid ledger account for a `BookItem`.
2. **Bank-item interpretation:** determine the economic intent of a `BankItem` so unmatched bank activity can enter the correct evidence, payment-application, book-creation, or hold workflow.

These jobs must use different DAGs, typed contracts, providers, plans, adapters, and workflows. They may reuse the generic ASE runtime and transport infrastructure.

The book path is now substantially implemented. The production Python NATS client, versioned transport, Go worker, scoped DAG, Moroccan PCGE classifier, distributed idempotency, readiness probe, and parity harness exist. Do not recreate them.

The session now also has a concrete two-stage settlement architecture:

```text
Posted-authority preplan (Stage 2 read-only)
    -> identifies BankItems already matchable to POSTED_BOOK_ITEM movements
    -> excludes those BankItems from Stage 1

Payment application (Stage 1)
    -> matches remaining BankItems to approved Invoice/Bill obligations
    -> posts real cash transactions through the Django accounting kernel
    -> persists immutable payment-application provenance
    -> rehydrates BookkeepingState

Final bank reconciliation (Stage 2)
    -> reconciles BankItems only to POSTED_BOOK_ITEM cash movements
```

The previous PRD's universal `JournalDraft -> Approval -> Posting` subsystem is therefore not the immediate requirement for ordinary Invoice/Bill settlement. The authoritative Invoice/Bill models and Django accounting kernel already construct and post those journals.

The remaining main gap is **bank interpretation and bank-originated accounting** for movements that match neither an existing posted cash movement nor an approved open obligation: bank fees, interest, transfers, taxes, payroll, direct revenue/expense, cash movements, and ambiguous statement lines.

## 2. Audit evidence

This revision was derived from the current code, particularly:

- `ledger/bookkeeping_state/session/bookkeeping_session.py`
- `ledger/bookkeeping_state/session/factory.py`
- `ledger/bookkeeping_state/dag/`
- `ledger/bookkeeping_state/payment_application/`
- `ledger/bookkeeping_state/reconciliation/`
- `ledger/bookkeeping_state/transitions/handlers/payment_application.py`
- `ledger/bookkeeping_state/persistence/writer.py`
- `ledger/bookkeeping_state/persistence/reader.py`
- `ledger/bookkeeping_state/hydration/hydrator.py`
- `ledger/bookkeeping_state_eval/dag/`
- `ledger/tests/test_accounting_stage_separation.py`
- `ledger/tests/test_stage1_accounting_execution.py`
- `go/internal/erp/ase/dags/bookkeeping_account_categorization_v1.yml`
- `go/internal/erp/ase/domain_tools/pcm_cash/pcm_classifier.go`
- `go/internal/erp/ase/domain_tools/pcm_cash/pcge_catalog.go`
- `go/internal/workers/bookkeeping_ase_book_categorizer_worker.go`
- `go/internal/workers/bookkeeping_ase_architecture_test.go`

### Verification limitation

The selected Python tests could not collect in the audit shell because `ortools` is absent (`ModuleNotFoundError`). This is an environment dependency failure, not evidence that the tests fail. Gemini must test in the configured repository environment with all declared dependencies.

## 3. Current authoritative architecture

### 3.1 Artifact meanings

| Artifact | Meaning | Authority |
| --- | --- | --- |
| `BankAccount` | A real company bank account | Durable input |
| `BankItem` | An observed statement movement already owned by a BankAccount | Durable bank observation |
| `STAGING_BOOK_ITEM` | An open obligation or other unposted book-side artifact | Book-side source projection |
| `POSTED_BOOK_ITEM` | A posted cash movement backed by a Django `TransactionModel` | Reconcilable ledger movement |
| `RoutingDecision` | Assigns a BookItem to a bank-account search space | Bookkeeping State |
| `ClassificationDecision` | Accepted account categorization for a BookItem | Bookkeeping State |
| `ExecutedPaymentApplication` | Links a BankItem, settled obligations, and resulting posted cash transactions | Bookkeeping State and Django persistence |
| `Reconciliation` | Exact value-conserving BankItem-to-posted-BookItem allocation | Bookkeeping State |

### 3.2 Implemented session order

The baseline order in `bookkeeping_session.py` is:

```text
1. Routing
2. Book categorization through AseClassifier
3. Bounded payment-application loop
   a. calculate Stage-2 posted-authority preplan
   b. exclude posted-authority BankItems from Stage 1
   c. plan Stage 1 against remaining BankItems and STAGING obligations
   d. mint HMAC capability bound to exact intent and state fingerprint
   e. apply one canonical proposal
   f. commit real accounting execution and provenance atomically
   g. rehydrate and repeat
4. Final Stage-2 reconciliation
5. State validation and closure
```

Preserve this architecture. Do not replace it with the old generic journal sequence.

### 3.3 Stage-1 accounting execution

`PaymentApplicationService` produces a read-only `PaymentPostingIntent`. `plan_two_stage_bookkeeping()` runs Stage 2 first and excludes BankItems with selectable posted authority.

`ApplyPaymentCommand` is protected by `Stage1ExecutionCapability`, binding the BankItem, BankAccount, direction, currency, exact amount, date, obligation allocations, session, state revision, and state fingerprint.

The transition handler revalidates these facts. The Django writer then:

1. resolves the bank source and actual BankAccount;
2. requires a configured cash `AccountModel` with `ASSET_CA_CASH` role;
3. rejects existing Stage-2 or prior Stage-1 ownership;
4. locks approved Invoice/Bill obligations;
5. rechecks remaining amount and currency;
6. invokes `make_payment_with_result(..., commit=True)`;
7. requires a posted journal and the correct cash account;
8. persists payment-application/allocation provenance;
9. increments the persistence revision once in the atomic commit.

This is the authoritative Invoice/Bill settlement path. Do not wrap it in a duplicate journal builder.

### 3.4 Stage-2 authority

Stage 2 accepts only `POSTED_BOOK_ITEM`, enforced in the reconciliation view, validation, and transition handler. Stage 1 accepts only staging Invoice/Bill obligations. A BankItem with selectable posted authority must never be executed by Stage 1.

### 3.5 Implemented book categorization

The following already exist:

- schema `bookkeeping.ase.book_categorize.v1`;
- DAG ID `bookkeeping_account_categorization_v1`;
- subject `worker.inbox.bookkeeping_ase_book_categorizer`;
- Python `NatsAseBookCategorizer` implementing `AseClassifier`;
- bounded view with evidence references and safe summaries;
- correlation, stale-revision, missing/duplicate/unknown outcome checks;
- evidence authorization and readiness preflight;
- Go worker using JetStream KV idempotency, digest, lease, heartbeat, CAS takeover, and stored-response replay;
- book-only Go DAG ending at `account_code` or hold;
- Moroccan PCGE classifier/catalog;
- simulator-versus-Go parity corpus, taxonomy, runner, and reports.

Harden these components; do not add parallel replacements.

## 4. Correct semantic boundaries

### 4.1 ASE handles both subject types

```text
BankItem interpretation:
    What does this observed cash movement represent?

BookItem categorization:
    Which entity-valid account represents this book-side artifact?
```

### 4.2 Separate DAGs are mandatory

| Workflow | Input | DAG | Terminal success |
| --- | --- | --- | --- |
| Book categorization | `BookItem` | existing `bookkeeping_account_categorization_v1` | entity-valid `account_code` |
| Bank interpretation | `BankItem` | new `bank_transaction_interpretation_v1` | normalized economic intent |

Do not send BankItems to the book DAG. Do not create a universal DTO with mostly optional bank/book fields.

### 4.3 Orchestration is not authority

ASE may recommend a downstream action. It may not directly establish a routing decision, executed payment, journal posting, reconciliation, or durable bookkeeping mutation. The owning deterministic subsystem must revalidate and accept the action.

### 4.4 Bank intent is not an account code

`BANK_FEE_COMMISSION`, `SUPPLIER_PAYMENT`, and `INTERNAL_TRANSFER` are intents, not entity accounts. A bank result may include non-authoritative account hints, but those hints must never be copied directly into `ClassificationDecision.account_code`.

## 5. Revised workflows

### 5.1 Existing posted movement

```text
BankItem -> Stage-2 preplan finds POSTED_BOOK_ITEM
         -> posted authority excludes it from Stage 1
         -> final Stage 2 accepts reconciliation
```

Bank interpretation is optional evidence here and must not override feasibility or posted authority.

### 5.2 Existing open obligation

```text
BankItem -> no posted match
         -> Stage 1 finds approved Invoice/Bill obligation(s)
         -> capability-authorized ApplyPaymentCommand
         -> Django kernel posts cash transaction(s)
         -> provenance persists -> rehydrate
         -> final Stage 2 reconciles new POSTED_BOOK_ITEM(s)
```

Bank interpretation may improve candidate evidence but does not replace Stage 1.

### 5.3 Unmatched bank-originated movement

```text
BankItem unresolved by posted authority and open-obligation matching
    -> BankInterpretationView
    -> AseBankInterpreter
    -> bank_transaction_interpretation_v1
       -> INTERPRETED: route to typed downstream policy
       -> HOLD: evidence/review workflow
       -> provider failure: operational failure/retry, never semantic hold
```

Examples:

| Intent | Required downstream behavior |
| --- | --- |
| `BANK_FEE_COMMISSION` | Configured fee/tax accounting policy |
| `BANK_INTEREST_AGIOS_EXPENSE` | Interest/agios and tax policy |
| `CUSTOMER_RECEIPT` | Search customer/open-invoice evidence, then Stage 1 if supported |
| `SUPPLIER_PAYMENT` | Search supplier/open-bill evidence, then Stage 1 if supported |
| `INTERNAL_TRANSFER` | Transfer pairing/accounting workflow, never income/expense |
| `CASH_WITHDRAWAL` | Cash/petty-cash workflow |
| `STATE_TAX_PAYMENT` | Search tax liability and require tax-specific posting |
| `PAYROLL_PAYMENT` | Search approved payroll liability |
| `DIRECT_REVENUE` | Require revenue evidence and entity tax/account policy |
| ambiguous | Hold with explicit required evidence |

### 5.4 Book-first movement

```text
Invoice/Bill/document -> STAGING_BOOK_ITEM or authoritative domain model
    -> routing when settlement account must be selected
    -> book categorization only when an account decision is required
    -> domain approval/accounting workflow
    -> later BankItem -> Stage 2 or Stage-1-to-Stage-2 flow
```

Do not assume every approved Invoice/Bill needs generic categorization. It may already contain authoritative receivable/payable, item, revenue/expense, tax, and counterparty configuration.

## 6. Audit findings and directives

### 6.1 Retire the universal journal requirement

The old PRD's new journal subsystem would duplicate Invoice/Bill accounting execution.

**Directive:** Preserve `PaymentPostingIntent -> ApplyPaymentCommand -> make_payment_with_result()` for existing obligations. A proposal/review artifact may be designed later for bank-originated events, but must not replace domain posting methods.

### 6.2 Bank interpretation is not implemented

No typed Python bank view/protocol/plan/adapter, bank NATS client, Go bank worker, or scoped bank DAG was found.

**Directive:** This is the primary new implementation scope.

### 6.3 Production account fallback is unsafe

The Go PCGE catalog falls back to a baseline catalog if company accounts are unavailable, and the classifier can use offline heuristic behavior when runtime execution is absent. These are useful locally but unsafe as silent production behavior.

**Directive:**

- Production requires an authoritative entity chart and real semantic runtime.
- Baseline/offline fallbacks must be explicit `LOCAL` or `TEST` modes.
- Chart loaded with no mapping -> `HOLD_ACCOUNT_UNMAPPED`.
- Chart/database unavailable in production -> provider/configuration failure.
- Python acceptance must also verify the returned account belongs to the entity chart.

### 6.4 Categorization eligibility is too broad

`build_dag_view()` generally selects unclassified BookItems. That can create redundant authority for approved invoices/bills or reinterpret posted transactions.

**Directive:** Add `source_type` and source-domain semantics to the view and a `book_items_requiring_categorization()` policy:

1. Never categorize a posted movement as if it can rewrite its journal.
2. Skip approved Invoice/Bill obligations whose authoritative accounting is complete and where classification is not consumed.
3. Categorize generic staging and bank-originated proposed book events lacking authoritative treatment.
4. Make reclassification explicit, with supersession and downstream invalidation rules.

### 6.5 Session documentation and names are stale

The class docstring still describes only routing, DAG, and reconciliation.

**Directive:** Document and name the implemented stages as:

- `posted_authority_preplan`;
- `payment_application_execution`;
- `final_bank_reconciliation`.

Update operator tool descriptions and session result documentation. The preplan is read-only and is not reconciliation truth.

### 6.6 Payment application must remain narrow

The writer intentionally resolves Invoice/Bill obligations only.

**Directive:** Do not expand `ApplyPaymentCommand` into a catch-all for fees, transfers, taxes, payroll, or direct revenue. Use typed accounting policies/services for bank-originated families.

### 6.7 Persist bank interpretation only when justified

Some interpretations may be runtime evidence only.

**Directive:** Implement the typed runtime plan and one concrete consumer first. Persist immutable `BankInterpretationDecision` only if replay, hold recovery, audit, or downstream reuse requires it. If persisted, implement supersession/invalidation, commands, handlers, state indexes, persistence, validation, and fingerprinting consistently.

## 7. Phase A — harden the implemented book path

### 7.1 Execution modes

Add explicit `PRODUCTION`, `LOCAL`, and `TEST` modes. Production forbids baseline account and offline semantic fallback. Test permits injected classifier and memory idempotency. Local fallback must be observable. Include mode and fallback status in telemetry and parity reports.

### 7.2 Typed chart lookup

Refactor `pcge_catalog.go` to distinguish:

- chart loaded with candidates;
- chart loaded but no compatible account;
- chart missing/unconfigured;
- database/query unavailable.

Do not collapse them into the baseline catalog. Update `pcm_classifier.go` and worker behavior accordingly.

### 7.3 Source-aware view

Extend the book view/transport with stable `source_type` and source-domain discriminator only as needed. Preserve backward compatibility while callers migrate. Implement explicit eligibility in queries rather than scattering source checks through providers.

### 7.4 Parity release gate

Use the existing parity harness. Release requires:

- zero tenant/evidence violations;
- zero stale/correlation acceptance errors;
- zero invalid entity accounts accepted;
- zero production fallback;
- critical/high-risk cases correct or explicitly held;
- retained disagreement reports.

## 8. Phase B — bank interpretation boundary

### 8.1 Python modules

Add:

```text
ledger/bookkeeping_state/bank_interpretation/__init__.py
ledger/bookkeeping_state/bank_interpretation/view.py
ledger/bookkeeping_state/bank_interpretation/protocol.py
ledger/bookkeeping_state/bank_interpretation/models.py
ledger/bookkeeping_state/bank_interpretation/adapter.py
ledger/bookkeeping_state/bank_interpretation/errors.py
ledger/bookkeeping_state/bank_interpretation/nats_interpreter.py
ledger/bookkeeping_state_eval/bank_interpretation/simulated_ase.py
```

Do not mix bank outcomes into the existing book `DagBatchPlan`.

### 8.2 Bounded view and protocol

The bank view item must include BankItem ID, actual BankAccount ID, date, exact amount units, currency, bank direction, description, reference, statement provenance, and authorized evidence/summaries. It may include bounded candidate-obligation summaries but not mutable state or accepted reconciliation.

```python
class AseBankInterpreter(Protocol):
    def interpret_view(
        self,
        view: BankInterpretationView,
        *,
        only_uninterpreted: bool = True,
    ) -> BankInterpretationPlan: ...
```

Provide a separate deterministic simulator.

### 8.3 Outcomes

Successful example:

```json
{
  "bank_item_id": "bank:123",
  "status": "INTERPRETED",
  "economic_intent": "BANK_FEE_COMMISSION",
  "confidence": 0.99,
  "rationale": "Narrative identifies a bank service commission.",
  "counterparty_hints": ["BANK"],
  "evidence_search_hints": [],
  "account_hints": ["6147"],
  "evidence_refs": ["statement:2026-08"],
  "ase_node_id": "bank_fee_terminal"
}
```

Hold example:

```json
{
  "bank_item_id": "bank:456",
  "status": "HOLD",
  "hold_reason": "HOLD_AMBIGUOUS_BANK_LINE",
  "rationale": "The abbreviation cannot identify the economic event.",
  "required_evidence": ["bank advice or counterparty confirmation"],
  "evidence_refs": ["statement:2026-08"],
  "ase_node_id": "hold_ambiguous_bank_line"
}
```

Missing output is a provider issue. Transport failure is never a semantic hold.

### 8.4 NATS contract

Use:

- schema `bookkeeping.ase.bank_interpret.v1`;
- DAG ID `bank_transaction_interpretation_v1`;
- subject `worker.inbox.bookkeeping_ase_bank_interpreter`;
- queue `bookkeeping_ase_bank_interpreter_group`;
- collection `bank_items`;
- idempotency prefix `ase-bank-interpret`.

Mirror the book transport's digest, tenant-safe KV key, distributed idempotency, lease/heartbeat, CAS takeover, response replay, payload mismatch rejection, readiness, correlation, evidence authorization, timeout, and fail-closed behavior.

Do not prematurely generalize the book worker. Small transport duplication is safer until typed parity tests prove a shared abstraction.

### 8.5 Go DAG and worker

Add:

```text
go/internal/erp/ase/dags/bank_transaction_interpretation_v1.yml
go/internal/workers/bookkeeping_ase_bank_interpreter_worker.go
```

The DAG may reuse bank-oriented nodes from `pcm_bank_cash_accounting_dag.yml` but must stop before reconciliation, journal construction, or commit.

Initial stable intent vocabulary:

- `CUSTOMER_RECEIPT`
- `SUPPLIER_PAYMENT`
- `BANK_FEE_COMMISSION`
- `BANK_INTEREST_AGIOS_EXPENSE`
- `INTERNAL_TRANSFER_IN`
- `INTERNAL_TRANSFER_OUT`
- `CASH_DEPOSIT`
- `CASH_WITHDRAWAL`
- `PAYROLL_PAYMENT`
- `SOCIAL_CONTRIBUTION_PAYMENT`
- `STATE_TAX_PAYMENT`
- `LOAN_DRAWDOWN`
- `LOAN_REPAYMENT`
- `CAPITAL_CONTRIBUTION`
- `OWNER_DISTRIBUTION`
- `DIRECT_REVENUE`
- `DIRECT_EXPENSE`
- `REVERSAL_RETURN_CHARGEBACK`
- `HOLD_AMBIGUOUS_BANK_LINE`

Values must be stable, versioned, and mapped to explicit downstream policies.

## 9. Phase C — safe session integration

### 9.1 Interpret unresolved items, not every item blindly

The current engine can resolve many BankItems cheaply and deterministically. Preferred order:

```text
Routing and source-aware book categorization
    -> posted-authority preplan
    -> Stage-1 candidate generation
    -> collect BankItems unresolved by both
    -> bank interpretation for that bounded set
    -> enrich/replan or invoke typed accounting policy
    -> execute authorized action -> rehydrate
    -> final Stage-2 reconciliation
```

Pre-interpretation may be evaluated, but production must justify its cost and latency with measured accuracy gains.

### 9.2 First consumer: bank fee

Implement `BANK_FEE_COMMISSION` first:

```text
unmatched BankItem -> bank ASE intent
    -> deterministic policy resolves actual bank cash account,
       configured fee account, VAT/tax treatment, date, and period
    -> explicit risk-policy approval
    -> Django accounting kernel posts balanced journal
    -> hydrate POSTED_BOOK_ITEM
    -> Stage 2 reconciles normally
```

Do not use `ApplyPaymentCommand`; no Invoice/Bill obligation is being settled.

After posting, persistence must advance, old state must be discarded, state must rehydrate, and Stage 2 must independently select the match. ASE initiation is not reconciliation proof.

## 10. Implementation sequence

Gemini must work in this order:

1. **Baseline:** install declared dependencies; run focused Python and Go suites; record failures.
2. **Book hardening:** production mode, entity-chart enforcement, source eligibility, docs, parity gate.
3. **Bank core:** typed view/protocol/models/errors/simulator and unit tests.
4. **Bank transport:** Python NATS client, Go worker, idempotency/readiness, scoped DAG, parity corpus.
5. **Coordinator integration:** expose unresolved-by-both IDs, interpret bounded set, keep holds/provider failures separate.
6. **Bank-fee slice:** deterministic policy, approval, Django posting, rehydration, Stage-2 reconciliation.
7. **Rollout:** shadow interpretation, tenant flag, metrics, then expand one accounting family at a time.

Do not begin by rewriting the existing book transport or Stage-1 executor.

## 11. Required tests

### 11.1 Book path

- Production uses `NatsAseBookCategorizer` with no eval imports.
- Readiness fails before hydration/mutation.
- Wrong schema, DAG, request, session, revision, or idempotency is rejected.
- Missing, duplicate, unknown, and unauthorized-evidence outcomes are rejected.
- Holds create no classification command.
- Entity-invalid accounts are held/rejected.
- Production cannot use baseline/offline fallback.
- Posted items are excluded from categorization.
- Authoritative Invoice/Bill treatment is not overwritten redundantly.

### 11.2 Stage separation

- Selectable posted authority excludes BankItem from Stage 1.
- Stage 1 accepts only staging Invoice/Bill obligations.
- Stage 2 accepts only posted movements.
- Capability rejects any altered bound field.
- Duplicate BankItem execution is rejected.
- Obligation capacity is locked and rechecked.
- Payment uses the BankAccount-linked cash account.
- Posting, payment provenance, and revision commit atomically.
- Rehydration exposes new posted cash movement.
- Final Stage 2 reconciles normally.
- Loop cannot execute one BankItem twice.

### 11.3 Bank boundary

- BankItem retains its actual account and is never routed as a BookItem.
- Each requested BankItem has one interpreted/held outcome or provider issue.
- Bank and book schemas/DAG IDs reject each other.
- Unknown, duplicate, missing, and unauthorized evidence outcomes are rejected.
- Account hints cannot create `ClassificationDecision` directly.
- Provider failure is not a semantic hold.
- Identical retry returns identical normalized response.
- Changed payload under one key is rejected.
- Lease heartbeat and CAS takeover prevent duplicate execution.
- Production dependency failure closes the path.

### 11.4 Bank-fee acceptance

Test a real BankAccount and unmatched bank-fee BankItem through interpretation, configured fee/tax policy, approval, balanced Django posting, hydration, and final Stage-2 reconciliation. Prove immutable lineage and idempotent replay.

Negative cases: ambiguity, missing fee account, missing tax policy, closed period, invalid cash role, posting failure, stale state, duplicate delivery, and reconciliation ambiguity.

## 12. Observability and controls

Emit company/session/revisions, subject type, schema/DAG/run/request IDs, hashed idempotency identity, execution mode, fallback flag, outcome counts, intent/account, posted-authority count, Stage-1 count, rehydrations, payment/posting/reconciliation IDs, latency, retries, failure stage, and error code.

Alert on any production fallback, idempotency outage, repeated takeover, provider omission, invalid account proposal, Stage-1/Stage-2 conflict, committed accounting followed by hydration failure, unchanged fingerprint after commit, or bank-originated posting unreconciled beyond policy window.

Security/accounting requirements:

- tenant-scope every item, account, evidence reference, and request;
- never put PII/narratives in idempotency keys;
- use fixed-precision integer units for equality;
- require authenticated or deterministic approved authorization for bank-originated posting;
- keep bank ASE action providers unable to write journals/reconciliations;
- never use confidence as authorization;
- preserve immutable history and correct posted accounting by reversal/new entry.

## 13. File directive map

| File/area | Directive |
| --- | --- |
| `session/bookkeeping_session.py` | Preserve loop; update terminology; later interpret bounded unresolved BankItems. |
| `payment_application/` | Preserve Invoice/Bill-only Stage 1 and posted-authority exclusion. |
| `handlers/payment_application.py` | Preserve capability/live validation; do not generalize to fees. |
| `persistence/writer.py` | Preserve atomic kernel execution; add separate typed bank-originated action only when designed. |
| `reconciliation/` | Preserve posted-only Stage 2 and value conservation. |
| `dag/` | Treat as book boundary; harden eligibility and entity-account validation. |
| `dag/nats_book_categorizer.py` | Preserve correlation/readiness; enforce production fail-closed behavior. |
| `bookkeeping_state_eval/dag/` | Preserve book parity; add distinct bank parity. |
| New `bank_interpretation/` | Implement typed bank semantic boundary. |
| Go book worker | Preserve scope/idempotency; eliminate silent production fallbacks. |
| Book categorization DAG | Preserve book-only terminal contract. |
| `pcge_catalog.go` | Return typed lookup outcomes; fallback only in explicit local/test. |
| `pcm_classifier.go` | Fail closed in production when chart/runtime unavailable. |
| New Go bank worker/DAG | Return bank intent only; stop before accounting/reconciliation. |
| Full PCM bank-cash DAG | Keep separate; never expose as either narrow contract. |
| `session/result.py` | Add bank stage/hold/provider reporting when integrated. |
| Operator tools | Report routing, book categorization, preplan, Stage 1, bank interpretation, and final Stage 2 distinctly. |

## 14. Prohibitions for Gemini

Gemini must not:

- recreate the implemented book NATS path under new names;
- delete the book simulator/parity harness;
- replace Stage 1 with a generic journal subsystem;
- wrap `make_payment_with_result()` in duplicate journal truth;
- mix BankItems and BookItems in one DAG/DTO;
- copy account hints into book truth;
- treat bank interpretation as reconciliation;
- let Stage 1 consume posted-authority BankItems;
- let Stage 2 consume staging obligations;
- generalize `ApplyPaymentCommand` to fees/transfers/tax/payroll;
- silently use baseline accounts or offline heuristics in production;
- hard-code `5141` globally;
- mutate posted journals from later semantics;
- persist every ASE intermediate candidate;
- convert provider failures to holds;
- wait for NATS inside a database transaction;
- skip rehydration after committed accounting;
- declare completion without existing-obligation and bank-first acceptance tests.

## 15. Acceptance criteria

1. Current book NATS integration remains operational.
2. Production book categorization cannot silently fall back.
3. Returned accounts are entity-chart valid.
4. Book categorization is source-aware.
5. Stage-1/Stage-2 ownership tests remain green.
6. Invoice/Bill payment continues through the Django kernel without duplicate journal artifacts.
7. A separate typed `AseBankInterpreter` exists.
8. A deterministic bank simulator exists.
9. A versioned bank Python NATS adapter and Go worker exist.
10. `bank_transaction_interpretation_v1` exists and stops before posting/reconciliation.
11. Bank/book contracts reject each other.
12. Bank idempotency/readiness matches the book path.
13. The coordinator exposes BankItems unresolved by posted authority and Stage 1.
14. Bank interpretation operates on that bounded set.
15. Holds and provider failures remain distinct.
16. Account hints never become book truth directly.
17. At least one bank-originated intent has a deterministic accounting policy.
18. Bank-fee posting uses the Django kernel and actual configured accounts.
19. The resulting cash movement hydrates as `POSTED_BOOK_ITEM`.
20. Final Stage 2 reconciles it normally.
21. Lineage is immutable and complete.
22. Replay cannot duplicate accounting or reconciliation.
23. Focused Python tests pass in the configured environment.
24. Relevant Go tests pass.
25. Separate book and bank parity reports meet approved risk thresholds.

## 16. Final rule

> ASE may interpret BankItems and categorize BookItems through separate typed DAGs. The existing book path and two-stage settlement engine are baseline implementation, not greenfield work. Posted authority wins before payment application. Invoice/Bill settlement remains owned by the Django accounting kernel. Bank ASE is applied where posted movements and approved obligations do not already explain a BankItem. Any bank-initiated accounting action must be independently validated, posted through the authoritative ledger, rehydrated, and reconciled by normal Stage 2.
