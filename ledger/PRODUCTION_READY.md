# Bookkeeping Lifecycle Production-Readiness Report

**Status**: `PRODUCTION_READY`  
**Date**: 2026-09-19  
**Scope**: Final resolution of production blockers for Toro's end-to-end bookkeeping lifecycle.

---

## Executive Summary

An adversarial audit concluded that Toro's bookkeeping lifecycle was `NOT_PRODUCTION_READY` due to two specific blockers:
1. Posting-owned reconciliations could be invalidated independently of their residual-bank posting, threatening invariant corruption (Case E).
2. Production composition did not explicitly wire, configure, or preflight the real residual-bank categorizer (`NatsAseBankCategorizer`).

Both blockers have been resolved and verified with comprehensive regression suites. The bookkeeping lifecycle is now **`PRODUCTION_READY`**.

---

## Section A: Posting-Owned Reconciliation Guard

### 1. In-Memory State & Query Defense
- **State Indexing**: Extended `BookkeepingState` with `_residual_bank_postings_by_reconciliation_id: dict[str, ResidualBankPosting]` initialized and updated upon hydration and state transitions.
- **Lookup APIs**: Exposed `get_residual_bank_posting_for_reconciliation(reconciliation_id: str) -> ResidualBankPosting | None` on both `BookkeepingState` and `BookkeepingQueries`.
- **Handler Guard**: In `_handle_invalidate_reconciliation` (`ledger/bookkeeping_state/transitions/handlers/reconciliation.py`), prior to accepting `InvalidateReconciliationCommand`, the handler checks:
  ```python
  posting = state.get_residual_bank_posting_for_reconciliation(command.reconciliation_id)
  if posting is not None:
      return TransitionRejection(
          code=RejectionCode.CANNOT_INVALIDATE_POSTING_RECONCILIATION,
          message=(
              f"Cannot invalidate Reconciliation {command.reconciliation_id!r}: "
              "reconciliation is owned by authoritative residual-bank posting and "
              "requires future formal posting reversal. Standalone invalidation is forbidden."
          ),
          artifact_ids=(command.reconciliation_id,),
      )
  ```

---

## Section B: Handler Rejection Code & Invariant Semantics

- **Rejection Code**: `RejectionCode.CANNOT_INVALIDATE_POSTING_RECONCILIATION = "CANNOT_INVALIDATE_POSTING_RECONCILIATION"`.
- **Invariant**: A residual posting JournalEntry, direct reconciliation, and posting provenance form one authoritative lifecycle unit.
- **Protection**: Rejects in-memory invalidation before creating an invalidation record or advancing the state revision, preventing bank items from reverting to residual state while their posted decision remains active (Case E prevention).

---

## Section C: Persistence Writer Under-Lock Defense

- **Atomic Transaction & Locking Order**:
  1. Acquire row lock on `EntityModel` (`select_for_update`)
  2. Acquire OCC row lock on `BookkeepingRevision` (`select_for_update`)
  3. Validate target `BookkeepingReconciliation`
  4. Query `BookkeepingResidualBankPosting` under lock:
     ```python
     if BookkeepingResidualBankPosting.objects.filter(
         reconciliation_id=ri.reconciliation_id
     ).exists():
         raise PersistenceError(
             f"CANNOT_INVALIDATE_POSTING_RECONCILIATION: Reconciliation '{ri.reconciliation_id}' "
             "is owned by an authoritative residual bank posting and requires formal posting reversal."
         )
     ```
  5. Insert `BookkeepingReconciliationInvalidation` only if invariant holds.
- **Deterministic Error Mapping**: `TransitionEngine._commit_write_set` intercepts `CANNOT_INVALIDATE_POSTING_RECONCILIATION` and returns `RejectionCode.CANNOT_INVALIDATE_POSTING_RECONCILIATION` without raising unhandled database errors.

---

## Section D: Ordinary Reconciliation Invalidation

Ordinary Stage 2 reconciliations not owned by `BookkeepingResidualBankPosting` continue to invalidate normally. Verified by:
- `test_f_ordinary_non_posting_reconciliation_invalidation_still_succeeds` in `ledger/tests/test_production_blockers_resolved.py`.
- Full pass in `ledger/bookkeeping_state_eval/tests/transitions/test_reconciliation_transition.py` (6/6 passing).

---

## Section E: Explicit Production Categorizer Dependency Injection

1. **`BookkeepingApplicationService`**:
   - Accepts and stores `residual_bank_categorizer: ResidualBankCategorizer | None`.
   - Explicitly injects `residual_bank_categorizer=self.residual_bank_categorizer` into `BookkeepingSession(...)`.
2. **`create_production_bookkeeping_application_service`**:
   - Explicitly instantiates `NatsAseBankCategorizer` with configured transport options and injects it into the service.
3. **`create_production_session`**:
   - Uses `create_production_residual_bank_categorizer` to instantiate `NatsAseBankCategorizer` and explicitly supplies it to `BookkeepingSession`.
4. **Neutralized Constructor Fallback**:
   - Removed implicit internal default instantiation of `NatsAseBankCategorizer()` inside `BookkeepingSession.__init__`.
   - If candidates exist and categorizer is missing: raises `MissingResidualBankCategorizerError` / fails at `FailureStage.RESIDUAL_CATEGORIZATION`.
   - If zero candidates exist: completes normally without requiring categorizer initialization.

---

## Section F: NATS Transport Configuration Propagation

- Unified transport settings across both DAG book categorization and residual bank categorization:
  - `nats_url`: Explicit parameter passed to both constructors; coherent fallback to shared environment/default configuration.
  - `timeout_seconds`: Coherently passed to both clients (default: 10.0s).
  - Subjects:
    - DAG: `worker.inbox.bookkeeping_ase_book_categorizer`
    - Residual Bank: `worker.inbox.bookkeeping_ase_bank_categorizer`

---

## Section G: Readiness & Preflight Preconditions

- Added `check_readiness(self) -> None` to `NatsAseBankCategorizer`.
- In `run_bookkeeping_session`:
  - Validates `app_service.residual_bank_categorizer.check_readiness()` alongside DAG classifier readiness before running any session hydration or mutations.
- In `BookkeepingApplicationService.run_session`:
  - Executes readiness check prior to hydrating state. Readiness failures terminate before state alterations and do not convert into HOLD decisions.

---

## Section H: Production Construction Site Audit

Every `BookkeepingSession(` construction site across the repository was audited:
1. `ledger/bookkeeping_state/session/service.py:112` — **PRODUCTION**: Explicitly passes `residual_bank_categorizer`.
2. `ledger/bookkeeping_state/session/factory.py:110` — **PRODUCTION**: Explicitly injects `residual_bank_categorizer` with coherent NATS config.
3. `ledger/bookkeeping_state_eval/lab.py:940, 1294` — **EVAL / LAB ONLY**: Evaluation harness.
4. `ledger/bookkeeping_state_eval/scenarios/temporal.py:468`, `runner.py:119` — **EVAL SCENARIOS ONLY**: Benchmark runner.
5. `ledger/bookkeeping_state_eval/tests/...` and `ledger/tests/...` — **TEST SUITES ONLY**: Explicit test mocks / fixtures.

**Audit Result**: Zero unconfigured or ambient production constructor call sites exist.

---

## Section I: Files Changed

| File | Nature of Change |
|---|---|
| `ledger/bookkeeping_state/transitions/result.py` | Added `CANNOT_INVALIDATE_POSTING_RECONCILIATION` rejection code |
| `ledger/bookkeeping_state/state/bookkeeping_state.py` | Added posting lookup index and query method |
| `ledger/bookkeeping_state/state/queries.py` | Exposed `get_residual_bank_posting_for_reconciliation` |
| `ledger/bookkeeping_state/transitions/handlers/reconciliation.py` | Added handler guard against invalidating posting reconciliations |
| `ledger/bookkeeping_state/persistence/writer.py` | Added under-lock defense against posting invalidation |
| `ledger/bookkeeping_state/transitions/engine.py` | Mapped persistence error to rejection code |
| `ledger/bookkeeping_state/bank_categorization/protocol.py` | Defined `MissingResidualBankCategorizerError` |
| `ledger/bookkeeping_state/bank_categorization/__init__.py` | Exported `MissingResidualBankCategorizerError` |
| `ledger/bookkeeping_state/bank_categorization/nats_bank_categorizer.py` | Added `check_readiness()` method |
| `ledger/bookkeeping_state/session/bookkeeping_session.py` | Neutralized implicit constructor default; added missing categorizer guard |
| `ledger/bookkeeping_state/session/service.py` | Injected categorizer into service and added preflight readiness checks |
| `ledger/bookkeeping_state/session/factory.py` | Added factory helper and injected categorizer into production session |
| `ledger/tests/test_production_blockers_resolved.py` | Dedicated test suite covering tests A through Q |

---

## Section J: Test Results

| Test Suite | Result | Details |
|---|---|---|
| `ledger/tests/test_production_blockers_resolved.py` | **16 / 16 PASSED** | Tests A through Q (all blocker behaviors & regressions) |
| `ledger/tests/test_bookkeeping_session_residual_bank_integration.py` | **22 / 22 PASSED** | End-to-end session integration & Case E defenses |
| `ledger/bookkeeping_state_eval/tests/transitions/test_reconciliation_transition.py` | **6 / 6 PASSED** | Reconciliation lifecycle & invalidation |
| `ledger/tests/test_residual_bank_posting_execution.py` | **29 / 29 PASSED** | Posting execution, concurrency, and rollback safety |
| `ledger/tests/test_residual_bank_categorization_transport.py` | **8 / 8 PASSED** | NATS transport envelope & validation |
| `ledger/tests/test_residual_bank_classification_transitions.py` | **26 / 26 PASSED** | Classification transitions & branching guards |
| `ledger/tests/test_residual_bank_selection.py` | **9 / 9 PASSED** | Residual candidate selection & filtering |

**Total Focused Regressions**: **116 / 116 tests passing**.

---

## Section K: Non-Blocking Follow-Ups Unchanged

1. `executed_residual_posting_ids` telemetry ID naming (contains command IDs instead of DB posting IDs).
2. Real-NATS container integration test for `BookkeepingSession` (session tests run against deterministic mock transport).

---

## Section L: Additional Blockers Discovered

None.

---

## Section M: Final Verdict

```
PRODUCTION_READY
```

1. No public or production transition path can independently invalidate a reconciliation owned by `BookkeepingResidualBankPosting`.
2. Every production `BookkeepingSession` construction path has an explicitly configured residual-bank categorizer, and production preflight validates its readiness/configuration.

---

## Section N: Next Task

Create a production-hardening and observability checklist covering telemetry counters, latency alerts, and dead-letter queue monitoring for residual categorizer workers.

BOOKKEEPING_PRODUCTION_BLOCKERS_RESOLVED


## WHAT IS NEXT?
The right next move is to ask: **what accounting work is still outside the automated lifecycle?**

We now have:

```text
bank movement
  -> Stage 1 obligation settlement
  -> Stage 2 existing-GL reconciliation
  -> residual semantic classification
  -> deterministic posting
  -> durable reconciliation
  -> HOLD when uncertainty remains
```

That is a real accounting engine.

So I would shift immediately to the next accounting primitive that materially increases automation coverage. The strongest candidate is **document-driven bookkeeping for residuals**, because HOLDs caused by missing invoices/receipts are now the obvious frontier.

Concretely, the next architecture problem should be:

```text
unresolved residual HOLD
    + newly supplied invoice / receipt / supporting document
        -> evidence ingestion
        -> re-evaluate existing HOLD
        -> supersede old decision
        -> classify
        -> post
```

That turns HOLD from a dead end into a proper asynchronous workflow and pushes the system closer to the 95% automation target.

After that, I’d move to the other large bookkeeping gaps: split transactions, VAT/document tax treatment, accrual/prepayment handling, payroll/loan/capital movements, and then period-close controls.

So: **no telemetry task next**. The next task should expand the bookkeeping engine’s economic coverage.
