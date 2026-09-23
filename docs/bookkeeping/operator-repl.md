# Accountant REPL & Bookkeeping Operator

This document describes the interactive developer evaluation lab and natural-language accountant workbench: `lab.py` (`ledger/bookkeeping_state_eval/lab.py`) and `BookkeepingOperator` (`ledger/bookkeeping_state/operator/agent.py`).

---

## 1. Overview & Launching

The Bookkeeping Lab provides a command-line interface (CLI) for inspecting, executing, and querying the bookkeeping runtime.

### Launch Modes

#### 1. Production Mode (Live PostgreSQL Database)
Inspects real database state for an active company or the synthetic benchmark company:

```bash
./.venv/bin/python ledger/bookkeeping_state_eval/lab.py \
  --production-state \
  --company-id toro-synthetic-bookkeeping
```

In production mode:
- State hydrates directly from PostgreSQL tables (`StagedTransactionModel`, `JournalEntryModel`, etc.).
- Natural language questions query the real database snapshot.
- Monetary values are formatted with the canonical 10,000-unit scale factor (`format_money()`).

#### 2. Evaluation Scenario Mode (In-Memory Benchmarks)
Loads an isolated in-memory challenge scenario from the benchmark catalog:

```bash
./.venv/bin/python ledger/bookkeeping_state_eval/lab.py \
  --scenario scenario_c_many_to_one
```

---

## 2. REPL Command Reference

Type commands directly at the `(lab) >` prompt:

### Inspection Commands (Read-Only)

| Command | Description | Output Details |
|---|---|---|
| `state` | Summarizes machine state and counts. | Session ID, $S_k$, $P_k$, bank/book counts, active routes, legacy book classifications, residual bank classifications, residual postings, active residual HOLDs, and active reconciliations. |
| `bank` | Lists all `BankItem` statement transactions. | ID, date, currency, description, original amount, and current remaining unreconciled amount (formatted with `format_money()`). |
| `book` | Lists all `BookItem`s (invoices, bills, cash legs). | ID, date, description, classification account code, remaining balance. |
| `residuals` | Lists post-reconciliation residual bank decisions. | Item ID, formatted amount, status (`[CLASSIFIED]` or `[HOLD]`), account code, confidence, hold reason, required evidence, and posting status. |
| `holds` | Lists all active holds across the system. | Surfaces residual bank classification `HOLD`s and legacy DAG holds with explicit reasons and required documentary evidence. |
| `classifications` | Lists legacy book classifications. | Labeled `ACTIVE LEGACY BOOK CLASSIFICATIONS` and points users to `residuals` when residual classifications exist. |
| `reconciliations` | Lists active durable reconciliations. | Reconciled IDs, bank allocations, book allocations, and semantic scores. |
| `unresolved` | Lists all items needing attention. | Unreconciled bank/book items, highlighting active `HOLD: <reason>` annotations and missing required evidence. |
| `remaining` | Lists remaining balances. | Remaining units vs. original units per bank and book item. |
| `fingerprint` | Displays current state fingerprint. | 64-hex SHA-256 state digest. |
| `revisions` | Displays revision markers. | Local revision $S_k$ and persistence revision $P_k$. |
| `operator_trace` | Toggles operator reasoning trace. | Prints raw tool invocations and LLM reasoning steps. |

### Session Execution & Mutation Commands

| Command | Description | Notes |
|---|---|---|
| `run` | Executes one complete `BookkeepingSession`. | Runs Routing $\to$ Stage 1 $\to$ Stage 2 $\to$ Residual ASE $\to$ Posting $\to$ Rehydration. |
| `rehydrate` | Reloads fresh state from database. | Resets local revision to $S_0$. |
| `close` | Closes active state (`state.close()`). | Prevents further queries until rehydration. |
| `add-bank` | Inserts a new bank item into persistence. | Requires amount, currency, direction, date. |
| `add-book` | Inserts a new book item into persistence. | Requires amount, currency, type, date. |
| `assert-evidence` | Asserts a durable evidence record. | Links document or counterparty evidence to a book item. |
| `invalidate-reconciliation` | Invalidates an ordinary Stage 2 reconciliation. | Releases allocated capacities. (Posting-owned reconciliations are rejected). |

---

## 3. The Natural-Language Bookkeeping Operator

The lab integrates an autonomous, conversational agent: `BookkeepingOperator`. When you enter text that is not a recognized CLI keyword, the REPL forwards the input to the Operator.

### Non-Hallucination & Tool-Grounded Principles

1. **Ground Truth First**: The Operator never guesses or invents transaction details. It invokes typed read-only tools against `BookkeepingWorkbench` (`get_state`, `list_bank_items`, `list_classifications`, `get_holds`, `list_unresolved`, `list_reconciliations`, `get_remaining_balances`) to ground its answers.
2. **Read-Only Safety**: Questions, forecasts, and explanations are strictly read-only. The Operator will never execute mutation tools (`run_bookkeeping`, `add_bank_item`, `set_policy`) unless explicitly commanded (e.g. `"run bookkeeping now"`).
3. **Bounded Tool Execution**: Operates in a bounded tool loop (maximum 8 rounds) to prevent runaways.
4. **Canonical Money Scale**: All tool payloads expose amounts scaled by 10,000 (e.g. $78,500,000 \to 7,850.00$ MAD).

---

## 4. Example Conversations (Real Production Transcripts)

### Conversation 1: "classification issues?"
```text
(lab) > classification issues?

Toro Operator is thinking...

Toro:
There is 1 classification issue currently on hold:

### On hold
- Bank item: staged:144668c8-772d-4717-8357-f5a2e179b992
- Amount: 7,850.00 MAD
- Description: "VIREMENT DIVERS REF 999888777 SANS OBJET PRECUS"
- Status: HOLD
- Reason: HOLD_AMBIGUOUS
- Engine rationale: Description is ambiguous or missing source context.
- Required evidence: None specified.
- Posting: No accounting posting exists.

### Successfully classified
Three other residual bank items were classified and posted:
- 150.00 MAD -> account 6147 (Bank fees)
- 1,200.00 MAD -> account 7127 (Accessory sales)
- 3,400.00 MAD -> account 4456 (DGI VAT)
```

### Conversation 2: "why is it unresolved?"
```text
(lab) > why is it unresolved?

Toro Operator is thinking...

Toro:
The unresolved item is bank transaction staged:144668c8-772d-4717-8357-f5a2e179b992 for 7,850.00 MAD.

It is on hold because:
- Status: HOLD
- Reason: HOLD_AMBIGUOUS
- Engine rationale: "Description is ambiguous or missing source context"
- Description: "VIREMENT DIVERS REF 999888777 SANS OBJET PRECUS"
- Reference: FIT-SYNTH-CASE7-HOLD
- No candidate hypotheses were identified
- No book items remain unreconciled

Additional source context—such as the transaction's purpose, counterparty, or supporting documentation—is needed before it can be classified or reconciled.
```

### Conversation 3: "how many were reconciled?"
```text
(lab) > how many were reconciled?

Toro Operator is thinking...

Toro:
6 reconciliations are currently active, covering all 6 book items and 6 bank transactions:
1. Customer Invoice settlement: 10,000.00 MAD
2. Supplier Bill settlement: 4,500.00 MAD
3. Cash GL counter deposit: 2,500.00 MAD
4. DGI VAT tax payment: 3,400.00 MAD (posted to account 4456)
5. Bank fee commission: 150.00 MAD (posted to account 6147)
6. Scrap material sale: 1,200.00 MAD (posted to account 7127)

Only 1 bank item remains unresolved (the 7,850.00 MAD transfer on HOLD).
```

### Conversation 4: "what happened to the DGI payment?"
```text
(lab) > what happened to the DGI payment?

Toro Operator is thinking...

Toro:
The 3,400.00 MAD bank outflow ("TELEPAIEMENT DIRECTION GENERALE DES IMPOTS TVA DGI REF 202604") was processed through the residual accounting pipeline:
- Semantic Evaluation: Classified to account 4456 (Etat, TVA due) with 0.99 confidence.
- Journal Entry: Posted to the General Ledger as entry je-103 (Debit: 4456 / Credit: 5141 Attijariwafa Bank).
- Direct Reconciliation: Fully reconciled against the bank cash transaction leg.
- Current Status: Reconciled with 0.00 MAD remaining balance.
```
