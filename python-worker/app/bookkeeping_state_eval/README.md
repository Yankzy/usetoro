# Bookkeeping State Eval

Deterministic evaluation harness and developer lab for the Toro `BookkeepingState` runtime architecture.

> [!IMPORTANT]
> ### 100% Deterministic Code — No LLM Dependency
> All work in this commit and throughout this runtime architecture is **strictly deterministic, rule-based, and mathematically verifiable**.
> - **Zero LLMs**: **No Large Language Models (LLMs)**, prompt-based heuristics, or stochastic neural inference are used anywhere in the core runtime, transition engine, reconciliation optimizer, evidence semantics, or evaluation challenge suites.
> - **Combinatorial Optimization**: Uses Google OR-Tools CP-SAT discrete constraint programming with exact, lexicographically ordered integer objectives.
> - **State Machine & OCC**: Deterministic state transitions governed by an authoritative `TransitionEngine` with optimistic concurrency control (OCC), revision preconditions, and atomic batch rollback.
> - **Discrete Formal Logic**: Allocation support (`AllocationSupport`), identity evidence validation, and routing decisions are computed through closed-form set logic and discrete rule trees.
> - **Exact Reproducibility**: Hydrating from durable storage revision $P_k$ produces the exact same in-memory state $S_0$ on every machine, runner, and execution without temperature or non-deterministic variance.

---

## Architectural Commit: `feat(bookkeeping-state-eval): build durable temporal bookkeeping state runtime`

- **Commit Hash**: `9800ea2223bb321f360f395c8ebd3b01a567ed23`
- **Scope**: 109 files, +46,628 lines, 0 regressions, 339 passing tests.
- **Architectural Milestone**: Full, frozen durable temporal bookkeeping runtime, authoritative transition engine, CP-SAT reconciliation solver, multi-dimensional evidence ledger, adversarial challenge suites, and interactive developer workbench (`lab.py`).

### What This Commit Delivers

1. **Authoritative Temporal Runtime & Transition Engine (`state/`, `transitions/`, `hydration/`)**:
   - **Strict Durable vs. Ephemeral Separation**: Durable bookkeeping records remain authoritative in persistence, while `BookkeepingState` is an ephemeral working view hydrated temporarily into memory.
   - **Monotonic Revision Lifecycles**: Durable persistence revisions increment monotonically ($P_0 \to P_1 \to P_2 \dots$), while in-memory state revisions always reset to $S_0$ upon fresh hydration and advance per applied transition ($S_0 \to S_1 \dots$).
   - **Atomic Batch Mutation Boundary**: Accepted changes flow strictly through `TransitionEngine.apply_batch()`. Direct in-memory dictionary mutation is prohibited.
   - **Optimistic Concurrency Control (OCC)**: Commands specify preconditions (`expected_state_revision`, `expected_persistence_revision`). Stale commands reject cleanly without mutating state.
   - **Catastrophic Failure Isolation**: If an unhandled post-commit failure occurs during in-memory delta application, the engine marks the state closed (`is_closed = True`), forcing callers to discard the corrupted memory view and rehydrate fresh from durable persistence.

2. **Deterministic Reconciliation Engine (`reconciliation/`, `domain/hypotheses.py`)**:
   - **Google OR-Tools CP-SAT Constraint Programming**: Solves combinatorial multi-to-multi bank/book reconciliations using a three-tier lexical objective hierarchy:
     1. **Tier 1**: Maximize total reconciled currency amount (exact conservation of money).
     2. **Tier 2**: Maximize unique fully-cleared items (minimize lingering fragmented balances).
     3. **Tier 3**: Maximize amount-weighted cardinal semantic utility ($[0, 1000]$ integer utility scale).
   - **Deterministic Candidate Generation**: Generates exact subsets based on sign compatibility, currency matching, date tolerance windows, and counterparty compatibility.
   - **Review-Only Counterfactual Preservation**: Hypotheses marked as `COUNTERFACTUAL_ONLY` (such as ambiguous allocations requiring human review) are retained in detached session summaries without contaminating durable ledgers.

3. **Identity vs. Allocation & Policy Boundaries (`reconciliation/allocation_support.py`, `transitions/handlers/reconciliation.py`)**:
   - **Four-State Allocation Support Model**:
     - `EXPLICIT_EVIDENCE`: Direct line-item document or counterparty linkage.
     - `UNIQUE_INFERENCE`: Mathematically unique partition among candidate items.
     - `INSUFFICIENT_EVIDENCE`: Multiple plausible candidate allocations exist.
     - `CONTRADICTION`: Allocation conflicts with known evidence assertions.
   - **Dual-Boundary Policy Enforcement**: The policy `auto_reconcile_unique_inferred_allocation` is enforced both during reconciliation planning *and* at the authoritative `TransitionEngine` handler boundary to prevent bypass from rogue or out-of-order command producers.

4. **Multi-Dimensional Evidence Ledger & Non-Resurrection Semantics (`domain/evidence.py`, `state/derived.py`)**:
   - **Dimension-Isolated Assertions**: Supports granular evidence assertions across isolated dimensions (`COUNTERPARTY`, `DOCUMENT_LINK`, `REFERENCE`, `DESCRIPTION_OVERRIDE`).
   - **Strict Non-Resurrection Invariant**: Invalidating a superseding evidence assertion returns the item's effective truth to its raw baseline or remaining active assertions—it never revives previously superseded historical assertions.
   - **Derived Query Projections**: `BookkeepingQueries` computes effective accounting properties on demand through pure functional projections over active assertions.

5. **Sequential Session Orchestration (`session/bookkeeping_session.py`, `session/result.py`)**:
   - Sequences execution across **Routing** $\to$ **DAG Classification (ASE)** $\to$ **Reconciliation**.
   - Preserves ephemeral diagnostic summaries (`HOLD_INSUFFICIENT_EVIDENCE`, `PROVIDER_UNAVAILABLE`, review-only hypotheses) in detached `SessionResult` records without persisting diagnostic noise into the double-entry accounting ledger.
   - Guarantees idempotent no-op execution when run against an already reconciled world.

6. **Comprehensive Adversarial & Temporal Evaluation Suites (`scenarios/`, `tests/`)**:
   - **19 Catalog Scenarios** (`scenarios/catalog.py`): Real-world accounting patterns (one-to-one, one-to-many, many-to-one, partial payments, cross-period adjustments).
   - **7 Adversarial Challenges** (`scenarios/challenges.py`): Stress-testing optimizer tie-breaking, semantic ambiguity, and greedy-trap avoidance.
   - **8 Temporal Falsification Challenges** (`scenarios/temporal.py`, Challenges 08–14): Validating arrival ordering invariance, late evidence arrival, evidence correction/invalidation, and multi-session convergence over successive persistence revisions.
   - **339 Passing Tests**: Zero failures across unit, integration, scenario, and temporal suites.

7. **Interactive Developer Workbench (`lab.py`)**:
   - Interactive REPL and programmatic workbench enabling developers to modify durable facts (`add-bank`, `add-book`, `add-counterparty`, `assert-evidence`), execute sessions, inspect state projections, and step through temporal timelines.

---

## Why Deterministic Code (Zero LLMs) for Bookkeeping?

Financial accounting requires absolute mathematical rigor, auditability, and legal compliance. Using Large Language Models or stochastic heuristics in core bookkeeping state transitions introduces fatal risks:

| Requirement | Deterministic Runtime (This Commit) | Stochastic / LLM Approach |
| :--- | :--- | :--- |
| **Conservation of Money** | **Guaranteed**: Enforced by CP-SAT integer constraints ($\sum \text{Bank} = \sum \text{Book}$) and post-commit invariants. | **Unreliable**: Prone to floating-point drift, rounding hallucination, and balance mismatch. |
| **Auditability & Traceability** | **Complete**: Every state delta is backed by an immutable command, authoritative event, and explicit provenance. | **Opaque**: Cannot prove why an LLM selected one partition over another without post-hoc rationalization. |
| **Concurrency & Idempotence** | **Deterministic**: Re-running a session with no new facts produces zero mutations; stale commands reject via OCC. | **Flaky**: Different temperatures, sampling seeds, or token limits yield divergent results across runs. |
| **Adversarial Resilience** | **Rigid**: Hard mathematical and semantic constraints reject contradictory allocations regardless of input wording. | **Vulnerable**: Susceptible to prompt injections, deceptive memo text, or ambiguous descriptions. |
| **Execution Latency & Cost** | **Sub-second & Free**: Local CP-SAT solve runs in milliseconds on CPU with zero token cost or external API latency. | **High Latency & Expensive**: External LLM round-trips add seconds per transaction and continuous API expenses. |

---

## Validated Architecture Principles

The runtime validates the following core principles across all 339 tests:

1. **Deterministic Execution**: All transitions, classifications, and optimizations are 100% reproducible and verifiable.
2. **Durable vs. Ephemeral Separation**: Durable artifacts remain authoritative in persistence, while working states are ephemeral projections.
3. **Deterministic Rehydration**: A fresh hydration from persistence reconstructs identical working accounting truth without depending on memory caches.
4. **Disposable State**: `BookkeepingState` can be closed or garbage collected at any time without data loss.
5. **Single Mutation Authority**: Transitions must execute via `TransitionEngine.apply_batch()`. Direct mutation is prohibited.
6. **Optimistic Concurrency Control**: Commands are bound to specific revisions. Stale decisions reject cleanly.
7. **Non-Resurrection Semantics**: Invalidating a superseding decision never resurrects obsolete superseded truth.
8. **Bounded Projections**: Stage processors (Routing, DAG classification, Reconciliation) operate exclusively on bounded immutable views.
9. **Lexical Optimization**: CP-SAT optimization strictly prioritizes: Maximize Amount $\succ$ Maximize Cleared Items $\succ$ Maximize Semantic Utility.
10. **Detached Diagnostics**: Ephemeral review hypotheses and engine holds are retained in detached session summaries and never persisted to the ledger.

---

## Developer Lab (`lab.py`)

`lab.py` is an interactive CLI workbench and programmatic API for exploring, mutating, executing, and verifying `BookkeepingState`.

### Launching the Lab

From the repository root:

```bash
# Launch with default in-memory demo company
./.venv/bin/python python-worker/app/bookkeeping_state_eval/lab.py

# Launch directly with a specific scenario from the catalog
./.venv/bin/python python-worker/app/bookkeeping_state_eval/lab.py --scenario scenario_c_many_to_one

# Launch with natural-language Operator LLM assistant enabled
./.venv/bin/python python-worker/app/bookkeeping_state_eval/lab.py --operator

# Launch with custom Operator model and real LLM inner semantic scorers
./.venv/bin/python python-worker/app/bookkeeping_state_eval/lab.py --operator --operator-model gpt-5.6-luna --semantic-provider llm

# Execute one session run immediately and exit (non-interactive mode)
./.venv/bin/python python-worker/app/bookkeeping_state_eval/lab.py --scenario scenario_b_one_to_many --run

# Launch with full debug tracebacks enabled
./.venv/bin/python python-worker/app/bookkeeping_state_eval/lab.py --debug
```

---

### Natural-Language Operator Mode

When launched with `--operator` (or toggled on via `operator on`), `lab.py` pairs the explicit command REPL with an autonomous **Operator LLM**. 

Any input not matching an explicit developer command is dispatched to the Operator agent, which plans and executes operations over **22 typed, detached workbench tools**:

```text
(lab)> state
Company: demo-company | Session: workbench-inspection-1 | State: S0 | Persistence: P1
Bank Items: 5 | Book Items: 5 | Reconciliations: 0

(lab)> how much is still outstanding for payroll?
Toro:
Based on current remaining balances, the payroll entry 'book-salary' has 40,000 MAD
outstanding, corresponding to 'bank-salary' (40,000 MAD) on 2026-09-01.

(lab)> run
Executed session workbench-session-1. Result: APPLIED. Reconciled: 4/5 items.

(lab)> why didn't the remaining item reconcile?
Toro:
Item 'book-mystery' (7,500 MAD) and bank transaction 'bank-mystery' (8,200 MAD) have
an amount discrepancy and conflicting references ('VIR-828192' vs 'VIR-DIFF').
No explicit allocation evidence exists, so the item remains open.
```

---

### Command Reference

The workbench provides commands across six operational categories:

#### 1. State Inspection
| Command | Description |
| :--- | :--- |
| `state` | Displays compact state summary: company, session ID, local revision ($S_k$), persistence revision ($P_k$), item counts, and diagnostic hold counts. |
| `bank` | Lists all `BankItem` records with original amount, remaining balance, direction, and metadata. |
| `book` | Lists all `BookItem` records with original amount, remaining balance, active account classification, and routing. |
| `classifications` | Lists all active account classifications applied to book items. |
| `routes` | Lists active bank account routing bindings for book items. |
| `reconciliations` | Lists active reconciliations with allocated bank/book item amounts and semantic scores. |
| `remaining` | Displays detailed breakdown of remaining capacities vs original amounts. |
| `holds` | Shows detached `HOLD` summaries from the most recent session run. |
| `provider-issues` | Shows provider issues (missing classification/routing) from the most recent session run. |
| `unresolved` | Lists all bank and book items that currently have remaining unallocated balances. |
| `review` | Displays review-only counterfactual reconciliation hypotheses retained from the last session. |
| `history` | Displays the complete chronological append-only event log across persistence revisions. |
| `evidence [item_id]` | Inspects active and superseded evidence assertions for a book item or across all items. |
| `policy` | Displays the active accounting policy configuration for the current context. |
| `fingerprint` | Displays SHA-256 hashes of durable artifacts and the canonical semantic state projection. |
| `revisions` | Displays the current local state revision ($S_k$) and durable persistence revision ($P_k$). |

#### 2. Natural-Language Operator
| Command | Description |
| :--- | :--- |
| `<any natural language query>` | When operator mode is active, any non-command text is handled by the Operator LLM. |
| `operator [on\|off]` | Enables or disables natural-language query routing in the active REPL session. |
| `clear-chat` | Resets the Operator's conversational memory while preserving current workbench state. |
| `operator-trace [on\|off]` | Toggles real-time streaming traces of tool calls and arguments invoked by the Operator. |

#### 3. Execution & Lifecycle
| Command | Description |
| :--- | :--- |
| `run` | Executes one complete `BookkeepingSession` (Routing $\to$ DAG $\to$ Reconciliation), commits transitions, closes session state, and hydrates a fresh inspection state. |
| `close` | Closes the active live inspection state (`state.close()`), preventing further queries until rehydration. |
| `rehydrate` | Reconstructs a fresh `BookkeepingState` from durable persistence, resetting local revision to $S_0$. |
| `reset` | Wipes in-memory repository artifacts and resets to a clean initial state. |

#### 4. Durable World Mutation (Exogenous Facts)
| Command | Description |
| :--- | :--- |
| `add-bank <id> <amount> [currency] [dir] [date] [desc] [ref]` | Inserts a new `BankItem` into durable persistence through authoritative repository APIs. |
| `add-book <id> <amount> [currency] [dir] [date] [desc] [ref]` | Inserts a new `BookItem` into durable persistence through authoritative repository APIs. |
| `add-counterparty <id> <name> <type> [tax_id] [email] [phone]` | Inserts a new `Counterparty` record into durable persistence. |
| `assert-evidence <book_id> <type> <val> [conf] [source]` | Appends a new `BookItemEvidenceAssertion` (e.g. `COUNTERPARTY`, `DOCUMENT_LINK`). |
| `invalidate-evidence <assertion_id> [reason]` | Invalidates an existing evidence assertion without resurrecting older superseded truth. |
| `invalidate-reconciliation <rec_id> [reason]` | Invalidates an active reconciliation, releasing allocated capacities for re-reconciliation. |
| `set-policy <key> <value>` | Updates accounting policy flags (e.g. `auto_reconcile_unique_inferred_allocation = true\|false`). |

#### 5. Scenarios & Temporal Challenges
| Command | Description |
| :--- | :--- |
| `scenario [name]` | Lists available catalog scenarios or resets the workbench with a selected scenario. |
| `challenges` | Lists all 7 adversarial static challenge scenarios. |
| `challenge <id>` | Seeds and loads an adversarial challenge scenario. |
| `temporal-challenges` | Lists all 8 multi-step temporal falsification challenges. |
| `temporal <id>` | Loads a temporal challenge and initializes step tracking. |
| `step` | Executes the next chronological step in an active temporal challenge. |
| `timeline` | Displays all historical and upcoming steps in an active temporal challenge. |
| `verify` | Verifies whether the current durable state matches the expected ground truth of the challenge. |

#### 6. General & Debugging
| Command | Description |
| :--- | :--- |
| `debug [on\|off]` | Toggles verbose exception tracebacks. |
| `help [command]` | Displays help information for all commands or detailed usage for a specific command. |
| `quit` / `exit` | Closes active state and exits the lab. |

---

### Programmatic Python API

The lab and workbench can also be imported and scripted in Python tests or notebooks:

```python
from bookkeeping_state_eval.operator.workbench import BookkeepingWorkbench
from bookkeeping_state_eval.operator.agent import BookkeepingOperator

# 1. Initialize workbench with a catalog scenario
wb = BookkeepingWorkbench()
wb.load_scenario("scenario_b_one_to_many")

# 2. Inspect initial state via typed tools
state_info = wb.get_state()
assert state_info["bank_items_count"] == 1
assert state_info["book_items_count"] == 2

# 3. Ask natural language questions via BookkeepingOperator
operator = BookkeepingOperator(workbench=wb, model_name="gpt-5.6-luna")
reply = operator.handle_message("How much is still outstanding for payroll?")
print(reply)

# 4. Execute a complete session through the workbench
result = wb.run_bookkeeping()
assert result["success"] is True
assert result["reconciliations_count"] == 1

# 5. Mutate durable state via fail-closed workbench tools
wb.add_bank_item(
    bank_item_id="bank-adjustment",
    amount="1500.00",
    currency="USD",
    date="2026-09-10",
    description="Wire adjustment",
)
```

---

## Running the Evaluation Test Suites

### 1. Offline Unit & Regression Suite (Zero API Keys)
Run all 367 offline tests across the runtime, transition engine, solver, evidence ledger, and operator layer:

```bash
make run_python_test test=python-worker/app/bookkeeping_state_eval/tests
```

All 367 tests execute completely offline without external network access or API dependencies.

### 2. Standalone Inner Semantic Reasoning Benchmark (`llm_eval.py`)
Evaluates 6 catalog scenarios and 5 adversarial challenge worlds comparing deterministic baselines against real LLM routing and reconciliation scoring:

```bash
# Offline deterministic baseline
PYTHONPATH=python-worker/app ./.venv/bin/python python-worker/app/bookkeeping_state_eval/llm_eval.py --mode deterministic

# Real LLM semantic scoring (gpt-5.6-luna)
PYTHONPATH=python-worker/app ./.venv/bin/python python-worker/app/bookkeeping_state_eval/llm_eval.py --mode llm --model gpt-5.6-luna

# Side-by-side comparative benchmark
PYTHONPATH=python-worker/app ./.venv/bin/python python-worker/app/bookkeeping_state_eval/llm_eval.py --mode both --model gpt-5.6-luna
```

### 3. Standalone Outer Operator Multi-Turn Benchmark (`operator_eval.py`)
Evaluates the natural-language conversational operator against multi-turn workflows:

```bash
PYTHONPATH=python-worker/app ./.venv/bin/python python-worker/app/bookkeeping_state_eval/operator_eval.py --model gpt-5.6-luna
```
