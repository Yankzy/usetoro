# Bookkeeping State Eval

Deterministic evaluation harness and developer lab for the Toro `BookkeepingState` runtime architecture.

---

## Validated Architecture Principles

The `BookkeepingState` evaluation suite validates the following fundamental runtime properties across 273+ unit and scenario tests:

1. **Durable vs Ephemeral Separation**: Durable bookkeeping artifacts remain authoritative in persistence while `BookkeepingState` is hydrated temporarily into memory for session execution or inspection.
2. **Deterministic Rehydration**: A fresh hydration reconstructs working accounting truth without depending on hidden in-memory state.
3. **Disposable Runtime**: `BookkeepingState` can be destroyed after a session and recreated later without loss of committed truth.
4. **Single Mutation Boundary**: Accepted accounting changes flow strictly through `TransitionEngine` via atomic batches. Direct mutation of `BookkeepingState` dictionaries is forbidden.
5. **Atomic Batches & Revision Binding**: Commands are bound to specific state revisions. Stale decisions reject cleanly, sibling commands apply atomically, and failed batches leave accounting truth untouched.
6. **Supersession & Invalidation Invariants**: Superseding decisions replace active truth without rewriting history. Invalidating a superseding decision does not resurrect superseded truth.
7. **Bounded Immutable Projections**: Routing, DAG classification (ASE), and reconciliation operate exclusively on bounded, immutable views (`RoutingView`, `DagView`, `ReconciliationView`).
8. **Reconciliation Optimization Contract**: 
   - **Phase 1**: Maximize total reconciled money.
   - **Phase 2**: Maximize unique fully-cleared items.
   - **Phase 3**: Maximize amount-weighted cardinal semantic utility (`[0, 1000]`).
9. **Detached Observability**: Ephemeral diagnostics (such as semantic `HOLD`, missing provider output, or provider issues) are preserved in detached `SessionResult` records and are never persisted into durable double-entry bookkeeping state.
10. **Revision Contract**: In-memory state revision always resets to `S0` on hydration, while `persistence_revision` monotonically tracks durable commits.

---

## Developer Lab (`lab.py`)

`lab.py` is a thin, interactive CLI playground and programmatic API allowing developers to manually instantiate, hydrate, inspect, execute, close, and rehydrate `BookkeepingState` without bypassing any architectural boundary.

### Launching the Lab

From the repository root:

```bash
# Launch default in-memory demo company
./.venv/bin/python python-worker/app/bookkeeping_state_eval/lab.py

# Launch directly with a specific scenario from the catalog
./.venv/bin/python python-worker/app/bookkeeping_state_eval/lab.py --scenario scenario_c_many_to_one

# Launch with full tracebacks enabled
./.venv/bin/python python-worker/app/bookkeeping_state_eval/lab.py --debug
```

Or from the `python-worker/app/bookkeeping_state_eval` directory:

```bash
python lab.py
python lab.py --scenario scenario_q_optimizer_competition
```

---

### Interactive REPL Commands

The CLI implements a standard `cmd.Cmd` interface. Below are all supported commands:

| Command | Description |
| :--- | :--- |
| `help` | Lists available commands or displays help for a specific command (`help <command>`). |
| `state` | Displays compact state summary: company, session ID, local revision (`S<n>` or `closed`), persistence revision (`P<n>`), item counts, active decision counts, unresolved counts, and detached diagnostic counts. |
| `bank` | Lists all `BankItem` records with original amount, description, and remaining capacity. |
| `book` | Lists all `BookItem` records with original amount, description, active account classification, and remaining capacity. |
| `classifications` | Lists all active classification decisions from `BookkeepingQueries.derived.active_classification_by_book_item`. |
| `routes` | Lists active bank account bindings for all routed book items. |
| `reconciliations` | Lists active reconciliations with allocated bank/book item amounts and semantic scores. |
| `remaining` | Displays detailed breakdown of remaining amounts vs original amounts for both bank and book items. |
| `holds` | Shows detached `HOLD` summaries (`DagHoldSummary`) from the most recent session run. |
| `provider-issues` | Shows detached provider issues (`DagProviderIssueSummary`) from the most recent session run. |
| `run` | Executes one complete `BookkeepingSession` (Routing -> DAG -> Reconciliation) against the durable repository, closes the session state, retains detached results, and rehydrates a fresh inspection state. |
| `close` | Closes the active live inspection state (`state.close()`), rendering queries inactive until rehydration. |
| `rehydrate` | Reconstructs a fresh `BookkeepingState` from durable persistence, resetting local revision to `S0`. |
| `scenario [name]` | With no arguments, lists all available catalog scenarios. With a scenario name, seeds its initial snapshot into a fresh repository and hydrates state `S0`. |
| `fingerprint` | Displays the durable artifact SHA-256 fingerprint (`artifact_fingerprint`) and the canonical semantic state projection SHA-256 fingerprint (`state_fingerprint`). |
| `revisions` | Displays the current local state revision (`S<n>` or `closed`) and durable persistence revision (`P<n>`). |
| `debug [on\|off]` | Toggles exception traceback printing. |
| `quit` / `exit` | Closes any open inspection state and exits the lab REPL. |

#### Intentionally Omitted Commands

To preserve architectural correctness:
- **`run-routing`**, **`run-dag`**, **`run-reconciliation`**: Omitted because `BookkeepingSession` is the single authoritative orchestration boundary managing atomic batch transitions, revision checks, view construction, and state closing. Invoking stages individually outside the session would duplicate orchestration internals and risk invalid state mutations.
- **`add-evidence`**: Omitted because durable repository artifacts are append-only without public in-place modification APIs.

If any of these commands are typed in the CLI, an informative explanation is printed.

---

### Programmatic Python API

The lab can also be imported and scripted in Python tests or notebooks:

```python
from lab import BookkeepingLab, create_demo_repository
from bookkeeping_state_eval.state.queries import BookkeepingQueries

# 1. Instantiate the lab with default demo or a catalog scenario
lab = BookkeepingLab()
# Or: lab = BookkeepingLab(scenario_name="scenario_c_many_to_one")

# 2. Inspect state via queries
queries = BookkeepingQueries(lab.state)
print("Book items:", len(lab.state.book_items))
print("Persistence revision:", lab.state.persistence_revision)

# 3. Execute a session via onecmd or direct execution
lab.onecmd("run")

# 4. Check detached session result
assert lab.last_result is not None
assert lab.last_result.is_success
print("Holds:", lab.last_result.hold_count)
for hold in lab.last_result.holds:
    print(f"Held item: {hold.book_item_id} -> {hold.reason}")

# 5. Lifecycle testing: close and rehydrate
lab.onecmd("close")
assert lab.state.is_closed

lab.onecmd("rehydrate")
assert not lab.state.is_closed
assert lab.state.revision == 0  # Local revision resets to S0
assert lab.state.persistence_revision == 4  # Durable history preserved

# Clean up
lab.onecmd("quit")
```

#### Key Class Attributes & Methods

- `lab.state: BookkeepingState | None`: The current live inspection state.
- `lab.repository: BookkeepingRepository`: The durable artifact repository.
- `lab.last_result: SessionResult | None`: Detached, immutable result of the most recent session run.
- `lab.company_id: str`: Active company identifier.
- `lab.onecmd(line: str) -> bool`: Dispatches any CLI command string programmatically.
- `create_demo_repository() -> tuple[BookkeepingRepository, str]`: Helper creating the demo repository with 5 bank items, 5 book items (including an ambiguous item), and 1 bank account.
- `seed_scenario_repository(scenario: ScenarioDefinition) -> BookkeepingRepository`: Helper seeding initial scenario artifacts into an in-memory repository without running the scenario.

---

### Example REPL Transcript

```text
$ ./.venv/bin/python python-worker/app/bookkeeping_state_eval/lab.py
==================================================
       BookkeepingState Developer Lab (REPL)      
==================================================
Type 'help' for available commands or 'quit' to exit.
Active scenario: default demo (demo-company)

(lab) > state
Company: demo-company
Session: lab-inspection-1
Local revision: S0
Persistence revision: P1

Bank items: 5
Book items: 5

Active routes: 0
Active classifications: 0
Active reconciliations: 0

Unresolved bank items: 5
Unresolved book items: 5

Last DAG holds: 0
Last provider issues: 0

(lab) > run
routing: APPLIED
dag: APPLIED
  classified: 4
  holds: 1
reconciliation: APPLIED

final persistence revision: P4

(lab) > holds
LAST SESSION HOLDS

book-mystery
  HOLD_INSUFFICIENT_EVIDENCE
  No deterministic semantic rule matched for book-mystery

(lab) > classifications
ACTIVE CLASSIFICATIONS

book-aws -> 6181 (source: ASE_DAG, confidence: 0.99)
book-rent -> 6131 (source: ASE_DAG, confidence: 0.99)
book-salary -> 6171 (source: ASE_DAG, confidence: 0.99)
book-supplier -> 4411 (source: ASE_DAG, confidence: 0.98)

(lab) > reconciliations
ACTIVE RECONCILIATIONS

rec:lab-session-2-recon:1
  bank: bank-salary (40,000)
  book: book-salary (40,000)
  score: 250

rec:lab-session-2-recon:2
  bank: bank-aws (1,200)
  book: book-aws (1,200)
  score: 250

rec:lab-session-2-recon:3
  bank: bank-rent (12,000)
  book: book-rent (12,000)
  score: 250

rec:lab-session-2-recon:4
  bank: bank-supplier (18,000)
  book: book-supplier (18,000)
  score: 250

(lab) > close
Inspection state closed. Use `rehydrate` to reopen.

(lab) > rehydrate
Hydrated fresh state S0 from persistence revision P4.

(lab) > revisions
local state_revision: S0
persistence_revision: P4

(lab) > quit
```

---

### Running Automated Tests

To run the complete evaluation suite including all lab lifecycle tests:

```bash
make run_python_test test=python-worker/app/bookkeeping_state_eval/tests
```
