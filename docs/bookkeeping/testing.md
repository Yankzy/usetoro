# Testing Architecture & Test Execution Guide

This document details the test suites, mock configurations, integration tests, and exact execution commands across the Python and Go codebases.

---

## 1. Test Suite Taxonomy & Environment Requirements

Toro's bookkeeping test suite is structured into clear tiers based on infrastructure dependencies:

| Test Tier | Scope | Target Technologies | Infrastructure Needed |
|---|---|---|---|
| **Unit & Transition Tests** | Domain models, transition handlers, CP-SAT optimizer, state indexing. | Python 3.12, `pytest` | In-memory only (zero DB, zero NATS). |
| **Persistence Tests** | Writer, reader, hydrator, OCC locking, check constraints. | Django ORM, PostgreSQL | Live PostgreSQL instance. |
| **Session Integration** | `BookkeepingSession` lifecycle, Stage 1 loop, Stage 2, residual loop. | Python, Django, Mocks | PostgreSQL (or in-memory mock repository). |
| **Parity & NATS Transport** | Go ASE worker, transport serialization, JetStream KV deduplication. | Go 1.22+, `nats.go` | Embedded NATS or running NATS server. |
| **Production E2E Regression** | Production blockers resolution, 7-case synthetic company. | Full stack | PostgreSQL, NATS, and Go ASE worker. |
| **REPL Surface Tests** | Money formatting scale (10,000), state breakdowns, Operator tools. | Python, `pytest` | In-memory fixture (fast, offline). |

---

## 2. Running Python Tests

All Python test suites are executed via the canonical Makefile target:

```bash
make run_python_test test=<path_to_test_file>
```

*(This activates `.venv/bin/pytest -v -s` within the configured Django test environment).*

---

### 2.1 Accountant REPL Surface Fixes Suite
Verifies canonical money formatting (scale 10,000), explicit state breakdown (legacy vs. residual), and Operator tool grounding for active holds:

- **File**: `ledger/bookkeeping_state_eval/tests/test_accountant_repl_surface_fixes.py`
- **Dependencies**: In-memory (Mock single snapshot repository)
- **Command**:
  ```bash
  make run_python_test test=ledger/bookkeeping_state_eval/tests/test_accountant_repl_surface_fixes.py
  ```
- **Key Tests**:
  - `test_money_formatting_canonical_cases`: Asserts `78_500_000` $\to$ `"7,850.00 MAD"`.
  - `test_get_state_exposes_residual_decisions_and_holds`: Verifies legacy vs. residual counters.
  - `test_list_classifications_exposes_residual_bank_decisions`: Checks `CLASSIFIED` and `HOLD` exposures.
  - `test_list_unresolved_grounds_classification_issues_in_active_hold`: Verifies active hold reason.
  - `test_lab_repl_commands_output`: Verifies stdout rendering of `state`, `bank`, `residuals`, `holds`.

---

### 2.2 Production Blockers Resolution Suite
Verifies resolution of adversarial audit blockers: posting-owned reconciliation invalidation guards (`CANNOT_INVALIDATE_POSTING_RECONCILIATION`), explicit categorizer injection, and Case E protection:

- **File**: `ledger/tests/test_production_blockers_resolved.py`
- **Dependencies**: PostgreSQL (`torodb`)
- **Command**:
  ```bash
  make run_python_test test=ledger/tests/test_production_blockers_resolved.py
  ```
- **Key Tests**:
  - `test_a_through_e_reconciliation_posting_invariants`: Verifies posting-owned reconciliation locks.
  - `test_f_ordinary_non_posting_reconciliation_invalidation_still_succeeds`: Verifies standard Stage 2 invalidation.
  - `test_g_through_k_explicit_dependency_injection`: Verifies `NatsAseBankCategorizer` injection.
  - `test_l_through_q_readiness_preflight_checks`: Verifies `check_readiness()` fail-closed behavior.

---

### 2.3 Stage 1 & Stage 2 Transition Tests
Verifies transition commands, invariant enforcement, and atomic write sets:

- **Transition Engine & Reconciliations**:
  ```bash
  make run_python_test test=ledger/bookkeeping_state_eval/tests/transitions/test_reconciliation_transition.py
  ```
- **Residual Classification Transitions**:
  ```bash
  make run_python_test test=ledger/tests/test_residual_bank_classification_transitions.py
  ```
- **Residual Bank Selection (Cases A–F)**:
  ```bash
  make run_python_test test=ledger/tests/test_residual_bank_selection.py
  ```

---

### 2.4 Persistence & Repository Tests
Verifies PostgreSQL database transactions, foreign key protections, and OCC revisions:

- **Durable Hydration**:
  ```bash
  make run_python_test test=ledger/tests/test_durable_hydration.py
  ```
- **Repository Commit & Optimistic Concurrency**:
  ```bash
  make run_python_test test=ledger/tests/test_bookkeeping_repository_commit.py
  ```
- **Residual Bank Posting Infrastructure**:
  ```bash
  make run_python_test test=ledger/tests/test_residual_bank_posting_infrastructure.py
  ```

---

## 3. Running Go Tests

Execute the Go ASE worker test suites using standard Go tooling:

```bash
# Test all worker boundaries and transport contracts
go test -v ./go/internal/workers/...
```

### Key Go Test Files:
- **`go/internal/workers/bank_categorize_contract_test.go`**:
  Verifies request/response JSON serialization, canonical bytes ordering, and SHA-256 digest computation.
- **`go/internal/workers/bookkeeping_ase_bank_categorizer_worker_test.go`**:
  Verifies JetStream KV idempotency, distributed leasing, CAS claim takeover, and response replay.
- **`go/internal/workers/bookkeeping_ase_architecture_test.go`**:
  Verifies architectural boundaries between domain tools and worker queues.

---

## 4. Running the Full E2E Integration Suite

To execute the authoritative end-to-end integration test against real database tables:

```bash
# 1. Reset and seed synthetic company
python manage.py seed_production_like_bookkeeping_company --reset

# 2. Run the production session
python manage.py run_synthetic_bookkeeping_e2e

# 3. Verify regression test file
make run_python_test test=ledger/tests/test_synthetic_e2e_bootstrap.py
```
