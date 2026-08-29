# PRD: Production Reconciliation Engine Architecture & Alignment

> **Document Version**: 2.0.0  
> **Status**: APPROVED FOR IMPLEMENTATION  
> **Author**: Antigravity Engineering  
> **Target Module**: `python-worker/app/reconciliation_prod/`  
> **Source Baseline**: `python-worker/app/reconciliation_eval/`

---

## 1. Executive Summary & Context

The initial extraction of `reconciliation_prod` from `reconciliation_eval` suffered from critical architectural gaps and regressions that compromised scalability and mathematical immunity in production:
1. **Combinatorial Explosion in Candidate Generation**: The extracted production code ran global, unconstrained CP-SAT subset-sum searches across the entire ledger ($O(2^{N+M})$), leading to potential memory exhaustion and token bloat on medium-to-large transaction volumes.
2. **Defective Lexicographic Objective Weights**: `reconciliation_prod/reconciliation/model.py` used compromised weights (`W_MONEY = 10_000_000`, `W_ITEMS = 1_000_000`), allowing 10 tiny clutter items to overpower $1.00 of real reconciled cash.
3. **Dead / Unreachable Code in Solver**: `reconciliation_prod/reconciliation/cp_sat.py` had an early return statement that rendered deep diagnostics, stability analysis, and alternative hypothesis evaluation completely unreachable.
4. **Circular Self-Imports**: `reconciliation_prod/reconciliation/validation.py` contained circular imports (`from reconciliation_prod.reconciliation.validation import DIRECTION_COMPATIBILITY_MAP`).
5. **Lack of Counterfactual Eligibility Tagging**: Missing `COUNTERFACTUAL_ONLY` hypothesis labeling in the semantic engine, increasing solver search overhead on contradictory candidates.

The evaluation suite has since proven a **100% Exact Match rate** across all benchmark scenarios by introducing **Bipartite Graph Partitioning** and **Hardened Lexicographic Optimization**.

This PRD specifies the complete technical overhaul required to bring `python-worker/app/reconciliation_prod/` into exact architectural parity with the proven evaluation baseline.

---

## 2. Core Architectural Principles

```
┌──────────────────────────────────────────────────────────────────────────────────┐
│                             THE TRUE BOUNDARY                                    │
│                                                                                  │
│   ┌─────────────────────────────┐        ┌───────────────────────────────────┐   │
│   │     Semantic Engine         │        │      Mathematical Authority       │   │
│   │         (LLM)               │        │        (CP-SAT Solver)            │   │
│   │                             │        │                                   │   │
│   │ • Contextual interpretation │        │ • Strict value conservation       │   │
│   │ • Narrative evidence match  │ ───►   │ • Mutual exclusivity              │   │
│   │ • Subjective utility (0-1k) │        │ • Lexicographic multi-objective   │   │
│   │ • Counterfactual labeling   │        │ • Deterministic invariant checks  │   │
│   └─────────────────────────────┘        └───────────────────────────────────┘   │
└──────────────────────────────────────────────────────────────────────────────────┘
```

1. **The True Boundary (Semantic vs. Math)**: The LLM is strictly a semantic scoring engine. It reads text, references, and counterparties to assign preference utility scores (0–1000). It is **never** trusted to perform arithmetic, balance checking, or global mutual exclusivity enforcement.
2. **Search Space Tractability via Graph Partitioning**: The system constructs a bipartite graph between Bank and Book items and isolates connected components before running CP-SAT subset-sums.
3. **Multi-Tiered Lexicographic Supremacy**: The optimizer uses strict hierarchical tiers separated by orders of magnitude, ensuring that money cleared always dominates clutter reduction, which dominates simplicity, which dominates LLM preference.
4. **Self-Contained Production Isolation**: `reconciliation_prod` must be 100% standalone, importing only standard libraries, OR-Tools, Pydantic, OpenAI, and NATS. It must never import from `reconciliation_eval/`.

---

## 3. The "Edge Heuristics Limit Discovery" Trade-off

A foundational design decision in the candidate generation layer is the deliberate trade-off between **combinatorial safety** and **unconstrained discovery**.

### 3.1 Graph Formulation
The candidate generator builds a bipartite graph $G = (U, V, E)$:
* **$U$ (Left Nodes)**: Canonical `BankItem` records.
* **$V$ (Right Nodes)**: Open `BookItem` records with remaining reconcilable capacity.
* **$E$ (Contextual Edges)**: An edge $(u, v)$ is drawn if and only if at least one heuristic is satisfied:
  1. **Exact Amount Heuristic**: $u.\text{amount\_int} == v.\text{remaining\_amount\_int}$
  2. **Explicit Reference Heuristic**: $u.\text{reference} \neq \emptyset \land v.\text{reference} \neq \emptyset \land u.\text{reference.lower}() == v.\text{reference.lower}()$
  3. **Textual / Counterparty / Provenance Overlap**: 
     - $v.\text{counterparty\_id.lower}() \subseteq u.\text{description.lower}()$, or
     - $v.\text{reference.lower}() \subseteq u.\text{description.lower}()$, or
     - $\exists p \in v.\text{provenance\_refs} \text{ s.t. } p.\text{lower}() \subseteq u.\text{description.lower}()$

### 3.2 Connected Component Decomposition
Using Breadth-First Search (BFS), the graph is partitioned into disjoint clusters $\{C_1, C_2, \dots, C_k\}$. CP-SAT subset-sum searches are executed independently per cluster:
$$\text{Worst-case Complexity}: \quad O(2^{|U| + |V|}) \quad \Longrightarrow \quad \sum_{i=1}^k O(2^{|U_i| + |V_i|}) \quad \text{where } |U_i|, |V_i| \ll |U|, |V|$$

### 3.3 The Trade-off & Human-in-the-Loop Safety Net
* **The Limit**: If a grouped payment (e.g., a 15,000 MAD deposit settling a 10,000 MAD invoice and a 5,000 MAD invoice) has no textual overlap, no shared reference, and no exact 1:1 amount match, it will not generate an edge in the graph. Such items are routed to a fallback "General Unmatched Cluster".
* **The Rationale**: Chasing unconstrained mathematical combinations across unlinked items would cause an $O(2^N)$ explosion, generating thousands of mathematically possible but semantically absurd matches.
* **The Operational Reality**: Completely unreferenced multi-invoice grouped payments lacking any textual, counterparty, or amount link represent **less than 1% of transactions** in production accounting.
* **The Safety Net**: These rare items remain cleanly in `unresolved_bank_ids` with `HoldAccuracy = 1.0`. Human reviewers (accountants) in the human-in-the-loop workflow catch and resolve these with external context.

---

## 4. Detailed Gap Analysis & Technical Delta

| Component | `reconciliation_prod` (Current State) | `reconciliation_eval` (Target Baseline) | Required Fix |
| :--- | :--- | :--- | :--- |
| **`candidate_generation.py`** | Global unpartitioned CP-SAT scans across all books/banks. | Bipartite Graph Partitioning with BFS connected components + fallback general cluster. | Replace with component-partitioned generator. |
| **`model.py` Objective Weights** | `W_MONEY = 10_000_000`<br>`W_ITEMS = 1_000_000`<br>`HYP_PENALTY = -10_000` | `W_MONEY = 1_000_000_000`<br>`W_ITEMS = 100_000`<br>`HYP_PENALTY = -10_000`<br>`W_UTILITY = 1` | Update weights to $10^9 / 10^5 / -10^4 / 1$. |
| **`cp_sat.py` Control Flow** | Dead code: early return at line 106 hides diagnostics & alternatives. | Clean single-exit return structure returning full response with diagnostics. | Remove dead code and unify return object. |
| **`validation.py` Imports** | Circular import `from reconciliation_prod.reconciliation.validation import ...` | Clean imports from `reconciliation_prod.domain.*` and internal constant definitions. | Fix import path and define compatibility map cleanly. |
| **`semantic_engine.py`** | Basic prompt without few-shot examples or counterfactual awareness. | Context-rich prompt with few-shot guidance, overlapping candidate handling, and counterfactual tags. | Update prompt and schema parsing logic. |
| **`domain/hypothesis.py`** | `ReconciliationHypothesis` lacks default eligibility handling in some paths. | Full Pydantic v2 validation with `Eligibility.SELECTABLE` and `COUNTERFACTUAL_ONLY`. | Ensure strict typing and integer accessors. |

---

## 5. Target Module Architecture & File Specifications

```text
python-worker/app/reconciliation_prod/
├── __init__.py
├── reconciliation_worker.py         # NATS Activity Worker for Account Reconciliation
├── routing_worker.py                # NATS Activity Worker for Multi-Account Routing
├── domain/
│   ├── __init__.py
│   ├── base.py                      # Direction, Eligibility, AmountUnits, Typed Enums
│   ├── bank.py                      # BankItem domain model with integer sub-unit accessors
│   ├── book.py                      # BookItem domain model with capacity tracking
│   ├── hypothesis.py                # ReconciliationHypothesis & Allocation models
│   └── state.py                     # ProposedState & ProposedMatchGroup models
├── reconciliation/
│   ├── __init__.py
│   ├── candidate_generation.py      # Bipartite Graph Partitioning + Component CP-SAT
│   ├── model.py                     # Hardened Lexicographic CP-SAT Model ($10^9 / 10^5 / -10^4 / 1$)
│   ├── cp_sat.py                    # Solver execution, diagnostics, & residual extraction
│   ├── semantic_engine.py           # Single-shot LLM scoring with counterfactual awareness
│   ├── protocol.py                  # OptimizerRequest, OptimizerResponse, SolverOptions
│   ├── optimizer_validation.py      # Pre-solve request validator
│   ├── alternatives.py              # Secondary solution stability & alternative search
│   ├── diagnostics.py               # Deep counterfactual rejection analysis
│   └── validation.py                # Independent 8-point deterministic invariant verifier
└── routing/
    ├── __init__.py
    ├── feasibility.py               # Dynamic Programming subset-sum feasibility bounds
    ├── scorer.py                    # LLM semantic routing scorer
    ├── optimizer.py                 # Lexicographic multi-account CP-SAT router
    ├── engine.py                    # Account routing orchestrator
    └── prompt.py                    # Routing prompt templates
```

---

## 6. Detailed Implementation Tasks

### 6.1 Task 1: Refactor Candidate Generation with Bipartite Graph Partitioning
* **File**: `python-worker/app/reconciliation_prod/reconciliation/candidate_generation.py`
* **Changes**:
  1. Build adjacency list `adj` using the 3 edge heuristics (Exact Amount, Reference Match, Substring Overlap).
  2. Implement BFS traversal to discover connected components `(comp_banks, comp_books)`.
  3. Aggregate degree-0 items into `(general_banks, general_books)` with restricted solver bounds (`max_solutions=3`, `subset_size <= 3`).
  4. Execute CP-SAT subset-sum model (`GroupedSolutionCallback`) per component.
  5. Deduplicate and format candidate output strings.

### 6.2 Task 2: Correct Lexicographic Objective Weights in CP-SAT Model
* **File**: `python-worker/app/reconciliation_prod/reconciliation/model.py`
* **Changes**:
  1. Set exact hierarchical constants:
     ```python
     W_MONEY = 1_000_000_000       # Tier 1: 1 Billion (Dominates all else)
     W_ITEMS = 100_000             # Tier 2: 100k (Clutter reduction)
     HYPOTHESIS_PENALTY = -10_000  # Tier 3: -10k (Occam's Razor / Anti-Farming)
     W_UTILITY = 1                 # Tier 4: 1 (Semantic tie-breaker)
     ```
  2. Filter hypotheses by `hyp.eligibility == Eligibility.SELECTABLE` in the objective expression.

### 6.3 Task 3: Fix Control Flow & Dead Code in CP-SAT Solver
* **File**: `python-worker/app/reconciliation_prod/reconciliation/cp_sat.py`
* **Changes**:
  1. Remove the premature return at line 106.
  2. Execute `analyze_rejected_hypotheses` and `find_alternatives_and_stability`.
  3. Return a single, unified `OptimizerResponse` containing selected matches, rejected matches with semantic reasons, held bank lines, exact residuals, and solver statistics.

### 6.4 Task 4: Fix Circular Imports & Enhance Deterministic Invariant Validation
* **File**: `python-worker/app/reconciliation_prod/reconciliation/validation.py`
* **Changes**:
  1. Define `DIRECTION_COMPATIBILITY_MAP = {("CREDIT", "DEBIT"), ("DEBIT", "CREDIT")}` directly within the module or import from `reconciliation_prod.domain.base`.
  2. Implement the 8 invariant checks:
     - Canonical ID referential integrity
     - Bank exclusivity (matched vs unresolved)
     - Full bank line consumption (no partial bank lines)
     - Monetary conservation ($\sum \text{bank} = \sum \text{book}$)
     - Direction compatibility
     - Currency uniformity
     - Book item remaining capacity non-overflow
     - Exact residual arithmetic verification

### 6.5 Task 5: Upgrade Semantic Engine Prompting & Counterfactual Tagging
* **File**: `python-worker/app/reconciliation_prod/reconciliation/semantic_engine.py`
* **Changes**:
  1. Upgrade `SYSTEM_PROMPT` to incorporate overlapping hypothesis requirements, FIFO guidance, and counterfactual tagging.
  2. Include clear few-shot examples for 1:1 matches, grouped settlements, and ambiguous competing options.
  3. Ensure Pydantic response parsing gracefully handles `ReconciliationHypothesis` lists.

### 6.6 Task 6: Audit Asynchronous NATS Workers
* **Files**: `python-worker/app/reconciliation_prod/reconciliation_worker.py`, `python-worker/app/reconciliation_prod/routing_worker.py`
* **Changes**:
  1. Verify JSON envelope deserialization and error boundary handling.
  2. Confirm responses publish clean `ProposedState` and `GlobalRoutingState` payloads back via NATS `msg.respond`.

---

## 7. Verification & Regression Plan

### 7.1 Automated Unit & Integration Tests
* **Test Suite**: Run unit tests inside the container environment:
  1. `test_bipartite_partitioning`: Verify that disjoint bank/book sets form isolated connected components and general fallback clusters.
  2. `test_lexicographic_objective`: Verify that high-money matches are strictly selected over high-utility fragmented matches (Anti-Utility Farming test).
  3. `test_deterministic_validation`: Verify that monetary imbalances, double-spends, and capacity overflows fail validation with precise error codes.
  4. `test_nats_worker_roundtrip`: Verify mock NATS request/reply processing.

### 7.2 Benchmark Comparison
* Execute production pipeline on the 5 canonical scenarios (`atlas_construction_sarl`, `atlas_station_sarl`, `maghreb_distribution_sarl`, `atlas_office_sarl`, `complex_settlements`).
* Target KPI: **100% Exact Global Match**, **0.0% FRR**, **<10s Average Latency**.

---

## 8. Rollout & Sign-off Checklist

- [ ] `candidate_generation.py` updated with Bipartite Graph Partitioning.
- [ ] `model.py` updated with $10^9 / 10^5 / -10^4 / 1$ Lexicographic tiers.
- [ ] `cp_sat.py` dead code removed and diagnostics restored.
- [ ] `validation.py` circular imports resolved.
- [ ] `semantic_engine.py` prompt upgraded with few-shot guidance.
- [ ] Standalone test suite passes 100% of invariant checks.
