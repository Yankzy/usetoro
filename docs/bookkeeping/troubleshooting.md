# Troubleshooting Guide

This guide provides an exhaustive reference for diagnosing, mitigating, and resolving operational and developmental errors within the Toro **BookkeepingState** runtime.

Every issue is structured according to the standard operational triage pattern:
**Symptom** $\to$ **Likely Cause** $\to$ **Safe Remediation** $\to$ **Prohibited Actions**.

---

## Triage Flowchart

```mermaid
flowchart TD
    Error[Encountered Error / Unexpected Behavior] --> CheckType{Error Category}
    
    CheckType -->|DB Connection / Auth| DB[Database & Auth Triage]
    CheckType -->|OCC / Revision Mismatch| OCC[Concurrency Triage]
    CheckType -->|NATS / Worker Timeout| NATS[ASE & Transport Triage]
    CheckType -->|Protection Violations| PROT[Posting Protection Triage]
    CheckType -->|Case E Invariant| CASE_E[Residual Integrity Triage]
    CheckType -->|REPL / Formatting| REPL[Operator & Units Triage]

    DB --> DBFix[Check .env, Docker ports, and user context]
    OCC --> OCCFix[Rehydrate state fresh from database; retry batch]
    NATS --> NATSFix[Verify NATS server & Go worker container health]
    PROT --> PROTFix[Do NOT call InvalidateReconciliation on postings]
    CASE_E --> CASE_EFix[Audit BookkeepingResidualBankPosting records]
    REPL --> REPLFix[Use format_money / format_currency, check API key]
```

---

## 1. Database Connectivity and Permissions

### 1.1 Connection Refused on PostgreSQL Port

- **Symptom**:
  ```text
  django.db.utils.OperationalError: could not connect to server: Connection refused
      Is the server running on host "localhost" (127.0.0.1) and accepting
      TCP/IP connections on port 5434?
  ```
- **Likely Cause**:
  1. The PostgreSQL container is stopped or has crashed.
  2. Running commands from host machine against port `5432` instead of host-mapped port `5434`.
  3. Running commands inside a Docker container without setting `USE_CONTAINER_GATEWAY=true` or pointing to `db:5432`.
- **Safe Remediation**:
  1. Verify the PostgreSQL container is running:
     ```bash
     docker ps --filter "name=postgres"
     ```
  2. If stopped, start the database service:
     ```bash
     docker compose up -d db
     ```
  3. Check connection settings in your `.env` or run environment:
     ```bash
     # When running directly on the host:
     export DATABASE_URL="postgresql://postgres:postgres@localhost:5434/toro"
     
     # When running inside Docker containers:
     export DATABASE_URL="postgresql://postgres:postgres@db:5432/toro"
     ```
- **Prohibited Actions**:
  > [!CAUTION]
  > Never alter the PostgreSQL configuration (`postgresql.conf`) or clear volumes (`docker volume rm`) to resolve a connection error. Doing so causes unrecoverable data loss for local test states.

---

### 1.2 User or Company Boundary Mismatch

- **Symptom**:
  ```text
  Company.DoesNotExist: Company matching query does not exist.
  # OR
  RuntimeError: Company toro-synthetic-bookkeeping is not owned by user yankz@fignode.com
  ```
- **Likely Cause**:
  The session or runner was executed with a non-existent company ID or without authenticating as the authorized company owner.
- **Safe Remediation**:
  1. Verify the target company ID matches the active database:
     ```bash
     ./.venv/bin/python -c "
     import django; django.setup()
     from core.models import Company
     print(list(Company.objects.values_list('id', flat=True)))
     "
     ```
  2. If using the synthetic test environment, re-run the idempotent seed script to create both user and company:
     ```bash
     ./.venv/bin/python ledger/bookkeeping_state_eval/seed_synthetic_production_state.py
     ```
  3. Ensure user credentials match `yankz@fignode.com` (user ID: `user-dev-local-yankz`).
- **Prohibited Actions**:
  > [!WARNING]
  > Never modify foreign keys directly in SQL (`UPDATE core_company SET user_id=...`) without updating associated auth tokens and session contexts.

---

## 2. Concurrency and State Revisions (OCC)

### 2.1 Optimistic Concurrency Failure (`OptimisticConcurrencyError`)

- **Symptom**:
  ```text
  OptimisticConcurrencyError: Precondition failed for command PostResidualBankClassificationCommand:
  expected persistence revision P3, but durable storage is currently at P4.
  ```
- **Likely Cause**:
  Another worker, an asynchronous task, or a previous step committed a database transaction, incrementing `BookkeepingRevision` from $P_3$ to $P_4$. The in-memory `BookkeepingState` was holding a stale revision precondition.
- **Safe Remediation**:
  1. Call `state.close()` on the obsolete in-memory state.
  2. Rehydrate a fresh `BookkeepingState` snapshot directly from persistence:
     ```python
     state = hydrator.hydrate(company_id=company_id)
     assert state.persistence_revision == 4
     assert state.state_revision == 0
     ```
  3. Re-evaluate and re-issue the command against the newly hydrated state.
- **Prohibited Actions**:
  > [!CAUTION]
  > Never manually overwrite `state.persistence_revision = 4` in memory without rehydrating! Doing so bypasses OCC, corrupts state tracking, and can post duplicate journal entries or create invalid reconciliations.

---

### 2.2 Attempting Mutations on a Closed State (`ClosedStateError`)

- **Symptom**:
  ```text
  ClosedStateError: Cannot apply command to a closed BookkeepingState.
  State was closed after commit or due to unhandled failure.
  ```
- **Likely Cause**:
  Once a command that performs durable persistence is applied (e.g. `RecordResidualPostingCommand` or `make_payment_with_result`), the engine marks `state.is_closed = True` to enforce the `APPLIED_REQUIRES_REHYDRATION` contract. Attempting to apply subsequent commands to that same Python object fails.
- **Safe Remediation**:
  Follow the standard rehydration lifecycle:
  ```python
  result = session.engine.apply_batch(state, [cmd])
  if result.rehydration_required:
      state.close()
      state = hydrator.hydrate(company_id=company_id)
  ```
- **Prohibited Actions**:
  Never set `state.is_closed = False` manually. A closed state has severed its link to database authority.

---

## 3. NATS Transport and Go ASE Worker

### 3.1 NATS Timeout During Residual Classification

- **Symptom**:
  ```text
  TimeoutError: NATS request timed out after 30.0s waiting on subject
  'worker.inbox.bookkeeping_ase_bank_categorizer'
  ```
- **Likely Cause**:
  1. The NATS broker (`nats:4222` or `localhost:4222`) is unreachable.
  2. The Go ASE worker process is not running or has not subscribed to `worker.inbox.bookkeeping_ase_bank_categorizer`.
  3. Worker pool is starved or locked on an external LLM request.
- **Safe Remediation**:
  1. Check NATS broker health:
     ```bash
     # Ping NATS port
     nc -zv localhost 4222
     ```
  2. Verify the Go worker container is running:
     ```bash
     docker ps --filter "name=core"
     ```
  3. Inspect Go worker container logs for subscription errors or unhandled panics:
     ```bash
     docker logs compose-core-1 | grep -i "ase_bank_categorizer"
     ```
  4. If NATS is temporarily down, the bookkeeping session safely catches the timeout and records `PROVIDER_UNAVAILABLE` in the ephemeral `SessionResult` without corrupting state.
- **Prohibited Actions**:
  > [!IMPORTANT]
  > Never fall back to unverified default account codes when NATS times out. Unreconciled bank items MUST remain unclassified or be safely marked `HOLD_INSUFFICIENT_EVIDENCE` until the semantic worker responds.

---

### 3.2 Duplicate Lease Collision in JetStream KV

- **Symptom**:
  ```text
  NatsAseLeaseError: Semantic digest a8f3b... is currently leased by another active worker.
  ```
- **Likely Cause**:
  Two concurrent sessions or workers attempted to categorize the identical bank item within the lease TTL window (default: 60s).
- **Safe Remediation**:
  Wait for the lease TTL to expire, or verify that multiple orchestrators are not processing the same `company_id` concurrently. JetStream KV leasing is working as designed to prevent split-brain categorization.

---

## 4. Posting Protection and Reconciliation Invariants

### 4.1 Invalidation Rejected on Residual Posting (`CANNOT_INVALIDATE_POSTING_RECONCILIATION`)

- **Symptom**:
  ```text
  CommandPreconditionError: CANNOT_INVALIDATE_POSTING_RECONCILIATION:
  Reconciliation rec-8f2a... was created directly by BookkeepingResidualBankPosting post-3b1c...
  and cannot be invalidated independently.
  ```
- **Likely Cause**:
  A caller issued `InvalidateReconciliationCommand` targeting a reconciliation record that was born out of a residual bank journal entry posting.
- **Why This Is Enforced**:
  A residual posting creates both a double-entry General Ledger transaction *and* an immediate direct reconciliation. If the reconciliation were invalidated alone, the General Ledger cash entry would become an un-reconciled book item, resulting in duplicate cash movements and balance drift.
- **Safe Remediation**:
  To undo a residual posting, an atomic unposting transition must be implemented that simultaneously:
  1. Creates an offsetting reversal journal entry in the General Ledger.
  2. Invalidates the direct reconciliation.
  3. Supersedes or invalidates the `BookkeepingResidualBankPosting` record.
- **Prohibited Actions**:
  > [!CAUTION]
  > Never delete the reconciliation row directly in PostgreSQL:
  > ```sql
  > -- PROHIBITED! WILL DESTROY LEDGER INTEGRITY
  > DELETE FROM core_bookkeepingreconciliation WHERE id = 'rec-8f2a...';
  > ```

---

### 4.2 Case E: Residual Bank Invariant Corruption (`ResidualBankInvariantCorruptionError`)

- **Symptom**:
  ```text
  ResidualBankInvariantCorruptionError:
  Bank item bank-item-004 has an unreconciled residual balance of 200,000 units (20.00 MAD),
  but has an active, non-superseded BookkeepingResidualBankPosting record (post-4d1a...).
  ```
- **Likely Cause**:
  The bank item already underwent residual posting, but its balance was later modified, partially un-reconciled, or corrupted via an out-of-band database update.
- **Safe Remediation**:
  1. Inspect the bank item and its posting provenance:
     ```python
     posting = BookkeepingResidualBankPosting.objects.filter(
         bank_item_id='bank-item-004',
         is_active=True,
     ).first()
     print(posting.journal_entry_id, posting.reconciliation_id)
     ```
  2. Check the reconciliation lines associated with the bank item:
     ```python
     recs = BookkeepingReconciliationLine.objects.filter(bank_item_id='bank-item-004')
     ```
  3. If manual testing introduced an orphan posting, clean it in a test environment by resetting the synthetic company via `seed_synthetic_production_state.py`.
- **Prohibited Actions**:
  Never ignore a Case E error. Case E indicates that money was posted to the General Ledger for this bank movement, yet the system believes it is still an unposted residual.

---

## 5. Accounting and Ledger Rules

### 5.1 Closed Accounting Period (`TransactionClosedPeriodError`)

- **Symptom**:
  ```text
  TransactionClosedPeriodError: Cannot post journal entry with date 2025-12-31:
  Accounting period 2025-Q4 is officially closed.
  ```
- **Likely Cause**:
  A residual bank item or invoice payment has a value date falling inside a closed fiscal period.
- **Safe Remediation**:
  1. In accordance with Moroccan accounting standards (PCGE), transactions discovered after period close must be posted in the **currently open fiscal period** with an adjustment note referencing the historical bank date.
  2. Pass an explicit `posting_date` override to the posting command if permitted by company accounting policy.
- **Prohibited Actions**:
  Never retroactively unlock a closed accounting period in production without written sign-off from the certified public accountant (Expert-Comptable).

---

### 5.2 Missing Bank General Ledger Account (`BankLedgerLookupError`)

- **Symptom**:
  ```text
  BankLedgerLookupError: No General Ledger cash account (5141xxx) mapped
  for BankAccount bank-acc-001.
  ```
- **Likely Cause**:
  The bank account statement line belongs to a bank account that does not have a linked General Ledger asset account in the Chart of Accounts.
- **Safe Remediation**:
  1. Ensure the `BankAccount` model has its `general_ledger_account_id` populated:
     ```python
     bank_acc = BankAccount.objects.get(id='bank-acc-001')
     bank_acc.general_ledger_account = Account.objects.get(code='51410001', company=company)
     bank_acc.save()
     ```
  2. Rehydrate state and re-run the posting loop.

---

## 6. Monetary Scaling & REPL Display

### 6.1 Raw `AmountUnits` Rendered Directly as Currency

- **Symptom**:
  The REPL or user interface displays:
  ```text
  Bank Item #4: Amount: 78,500,000 MAD  <-- BUG
  ```
  Instead of:
  ```text
  Bank Item #4: Amount: 7,850.00 MAD
  ```
- **Likely Cause**:
  A developer or template formatted `item.raw_amount_units` directly with currency suffix without applying the $10,000$ scale factor divisor.
- **Safe Remediation**:
  Use the canonical formatting utilities from `ledger.bookkeeping_state_eval.money`:
  ```python
  from ledger.bookkeeping_state_eval.money import format_money, amount_units_to_decimal

  # Correct:
  formatted = format_money(item.amount_units, currency="MAD")  # "7,850.00 MAD"
  
  # Or convert to Decimal:
  decimal_val = amount_units_to_decimal(item.amount_units)      # Decimal('7850.00')
  ```
- **Prohibited Actions**:
  Never perform floating-point division (`val / 10000.0`) in financial calculations. Always use integer arithmetic for solvers and `Decimal` for financial presentation.

---

### 6.2 Operator LLM Hallucinating Balances or Reconciliations

- **Symptom**:
  The `BookkeepingOperator` in the REPL gives contradictory or imaginary transaction details when responding to accountant queries.
- **Likely Cause**:
  1. The LLM prompt was not injected with the fresh `BookkeepingState` context.
  2. The query tool was bypassed, forcing the LLM to guess from training weights.
- **Safe Remediation**:
  1. Always verify the REPL initialized with `--production-state`.
  2. Check that the Operator's system prompt strictly bounds answers to the tools:
     - `get_company_state_summary()`
     - `list_unresolved_bank_items()`
     - `get_bank_item_details(bank_item_id)`
     - `list_active_holds()`
  3. Review the conversation transcript in `.system_generated/logs/transcript.jsonl` to ensure tool calls were executed prior to the final response.

---

## Quick Reference: Exit Codes & Diagnostic Commands

| Issue | Quick Diagnostic Command |
|---|---|
| Check Database | `docker compose ps db` |
| Check NATS Broker | `docker compose ps nats` |
| Verify Synthetic State | `./.venv/bin/python ledger/bookkeeping_state_eval/lab.py --production-state --company-id toro-synthetic-bookkeeping --command "status"` |
| Run Unit Test Suite | `make run_python_test test=python-worker/app/bookkeeping_state_eval/tests` |
| Inspect Journal Balance | `SELECT SUM(debit - credit) FROM core_journalentryline WHERE company_id = 'toro-synthetic-bookkeeping';` (Must equal `0.00`) |
