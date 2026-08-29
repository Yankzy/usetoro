# Reconciliation Evaluation Framework: White Paper

## 1. Purpose

`reconciliation_eval` is an evaluation harness for testing AI-assisted bank-to-ledger reconciliation under strict accounting constraints. Its purpose is not to let a language model alter financial records. Instead, it measures whether an agent can propose useful reconciliation groups while deterministic code verifies accounting invariants, bipartite graph clustering isolates search spaces, and a global constraint solver evaluates the proposals.

The framework is designed to answer a practical question:

> Given canonical bank movements, open book items, and supporting evidence, can an AI-assisted workflow produce a valid, auditable reconciliation state and how does its performance change when bipartite graph partitioning and global lexicographic optimization are available?

The package covers the full evaluation lifecycle:

1. Define typed reconciliation inputs and expected ground truth.
2. Route multi-account book items into account-scoped reconciliation sub-problems.
3. Pre-filter the ledger into isolated connected components via **Bipartite Graph Partitioning**.
4. Generate deterministic, mathematically exact candidate relationships via CP-SAT within each component.
5. Ask an LLM to reason over evidence, assign semantic utility scores, and tag counterfactual alternatives.
6. Use a **Lexicographic CP-SAT Optimizer** to select a globally compatible, clutter-minimizing set of hypotheses.
7. Independently validate the final proposed state for balance conservation and ledger consistency.
8. Compare routing and reconciliation outcomes to scenario ground truth and persist reproducible run artifacts.

---

## 2. System Boundary and Design Principles

The framework works with absolute, integer-denominated amount sub-units (e.g., 100 MAD = `10000000` sub-units) rather than floating-point values to eliminate rounding errors. A `BankItem` is a canonical bank statement movement; a `BookItem` is an open ledger item with remaining reconcilable capacity. Both carry identifiers, dates, direction, currency, and descriptive or reference information.

Three fundamental principles define the design:

1. **The True Boundary (Semantic vs. Math):** The LLM is the "Semantic Engine". It reads descriptions, counterparties, and operational evidence to assign subjective utility scores. It is *never* trusted to do arithmetic or enforce mutual exclusivity. The CP-SAT optimizer is the "Mathematical Authority" enforcing double-entry invariants.
2. **Search Space Tractability via Graph Partitioning:** Instead of running unconstrained subset-sum algorithms across the entire ledger, the system partitions items into bipartite connected components, keeping candidate generation computationally tractable.
3. **Optimization is Global and Lexicographic:** A locally plausible match can be globally sub-optimal if it consumes balances needed by a superior alternative. The global solver uses strict lexicographic tiers (Money $\to$ Items $\to$ Simplicity $\to$ Utility) to eliminate utility farming and hallucinations.

---

## 3. End-to-End Workflow

```text
Scenario inputs + evidence + ground truth
                 |
                 v
   [Multi-account routing / account partitioning]
                 |
                 v
   Bipartite Graph Partitioning (Connected Components)
                 |
                 v
   Partitioned CP-SAT Candidate Generation
                 |
                 v
   LLM Semantic Scoring Engine (Utility + Counterfactuals)
                 |
                 +--------------------+
                 |                    |
                 v                    v
      LLM-only proposed state    Lexicographic CP-SAT Solver
                 |                    |
                 +---------+----------+
                           v
             Deterministic proposed-state validation
                           |
                           v
             Ground-truth metrics and saved run artifact
```

The evaluation runner executes both configurations:
- **A — LLM only:** The agent returns its final proposed state based purely on prompt reasoning without solver intervention.
- **B — LLM + Optimizer:** The agent submits scored candidate hypotheses to the global solver, which outputs the optimal patch.

---

## 4. Bipartite Graph Partitioning & Candidate Generation

### Graph Formulation
Before mathematical candidate generation, `agent/candidate_generation.py` constructs a bipartite graph $G = (U, V, E)$:
- **$U$ (Left Nodes):** Canonical `BankItem` records.
- **$V$ (Right Nodes):** Open `BookItem` records.
- **$E$ (Contextual Edges):** Drawn between a bank item $u \in U$ and book item $v \in V$ if they satisfy at least one heuristic:
  1. *Exact Amount Match*: $u.\text{amount} = v.\text{remaining\_amount}$
  2. *Reference Match*: $u.\text{reference} = v.\text{reference}$ (non-null)
  3. *Textual / Counterparty Overlap*: $v.\text{counterparty\_id}$ or $v.\text{reference}$ is present as a substring in $u.\text{description}$.

### Connected Components & Partitioned CP-SAT
Using Breadth-First Search (BFS), the graph is decomposed into disjoint connected components $\{C_1, C_2, \dots, C_k\}$. 
The CP-SAT subset-sum model is executed independently within each sub-graph $C_i$:
- This collapses the worst-case search complexity from exponential over the entire ledger ($O(2^{|U| + |V|})$) to the sum of small, isolated sub-problems ($\sum_{i=1}^k O(2^{|U_i| + |V_i|})$).
- It suppresses thousands of mathematically possible but semantically irrelevant candidate combinations from ever reaching the LLM's context window.

---

## 5. The "Edge Heuristics Limit Discovery" Trade-off

A critical architectural decision in the design of `reconciliation_eval` is the deliberate trade-off between **combinatorial safety** and **unconstrained discovery**:

### The Limit of Edge Heuristics
If a multi-invoice grouped settlement (e.g., a single 15,000 MAD bank deposit paying off a 10,000 MAD invoice and a 5,000 MAD invoice) contains:
- No explicit reference in the bank statement,
- No matching counterparty substring in the description, and
- No exact 1:1 amount match with either individual invoice,

it will not generate an edge in the bipartite graph. These unlinked items fall into a fallback "General Unmatched Cluster".

### Why We Accept This Trade-off
If the system attempted to exhaustively compute all $N$-to-$M$ subset-sums across the unlinked general cluster without edge heuristics:
1. **Combinatorial Explosion:** The search space would explode exponentially, causing solver timeouts, massive memory footprints, and token bloat.
2. **Adversarial Distractors:** The LLM would be flooded with dozens of spurious mathematical ties (e.g., three random invoices totaling a utility bill), increasing the risk of false positive reconciliations.

### The Human-in-the-Loop Safety Net
Real-world enterprise accounting data demonstrates that completely unreferenced, multi-invoice grouped payments lacking any textual or amount link represent **less than 1% of transactions**.

Rather than compromising system throughput and stability for 99% of transactions to chase an unconstrained edge case, the system intentionally isolates these rare occurrences:
- Items that cannot be clustered or resolved with high semantic confidence remain in the `unresolved_bank_ids` set.
- Human reviewers (accountants) in the human-in-the-loop workflow easily identify and manually clear these rare items with domain context.

---

## 6. Global Constraint Optimization & Lexicographic Priorities

When the agent invokes the optimizer, it provides `ReconciliationHypothesis` objects. The solver uses **Lexicographic Multi-Objective Optimization** to ensure mathematical correctness and eliminate "Utility Farming".

### The Utility Farming Problem
A naive solver that maximizes $\sum \text{utility}$ can be tricked: if a 10,000 MAD payment can match a single 10,000 MAD invoice (Utility 950) or be fragmented into three spurious partial invoices (Utility 900 each), a sum-maximizing solver chooses the fragmented hallucination because $900 \times 3 = 2700 > 950$.

### The Multi-Tiered Lexicographic Objective
To prevent this, the solver enforces strict hierarchical priorities separated by orders of magnitude:

$$\max \quad W_1 \cdot (\text{Money Cleared}) + W_2 \cdot (\text{Items Cleared}) - W_3 \cdot (\text{Hypotheses Used}) + W_4 \cdot (\text{Semantic Utility})$$

Where weights are structured such that a lower tier can never overpower a higher tier:
1. **Tier 1 (Maximize Money Cleared - Weight $10^9$):** The primary accounting objective. Clearing $100,000 is strictly superior to clearing $99,000.
2. **Tier 2 (Maximize Items Cleared - Weight $10^5$):** Ledger clutter reduction. If money cleared is equal, closing 10 open invoices is superior to closing 1.
3. **Tier 3 (Consolidation Penalty / Occam's Razor - Weight $-10^4$):** Penalizes hypothesis count. Eliminates Utility Farming by favoring the simplest mathematical formulation.
4. **Tier 4 (Maximize LLM Semantic Utility - Weight $1$):** Semantic tie-breaker. Used only when monetary volume, item count, and structural simplicity are identical.

---

## 7. Independent Deterministic Validation

Every proposal is validated by `validation/deterministic.py` before state persistence:
- **Canonical ID Integrity:** Every referenced bank and book ID must exist in the input set.
- **Mutual Exclusivity:** A bank item must have exactly one disposition: matched or unresolved.
- **Value Conservation:** $\sum \text{bank allocations} = \sum \text{book allocations}$ within every match group.
- **Capacity Constraints:** No book item can be allocated beyond its remaining open capacity.
- **Residual Verification:** Claimed residuals must mathematically match computed unallocated balances.

---

## 8. Evaluation Metrics

| Metric | Definition |
| :--- | :--- |
| `Recall` | $\frac{|\text{True Positive Matches Recovered}|}{|\text{Ground Truth Match Groups}|}$ |
| `FRR` (False Reconciliation Rate) | $\frac{|\text{Incorrect Proposed Matches}|}{|\text{Total Proposed Matches}|}$ |
| `HoldAccuracy` | Fraction of expected unresolvable/orphaned bank lines correctly held. |
| `GLOBAL_STATE_EXACT` | Binary flag; true only if Recall = 1.0, FRR = 0.0, and HoldAccuracy = 1.0 simultaneously. |

---

## 9. Key Evaluation Findings

Across rigorous multi-run evaluations on production scenarios:
1. **Optimizer Superiority:** `B_OPTIMIZER` consistently achieved **100% Exact Global State recovery** across all scenarios, whereas `A_LLM_ONLY` suffered from arithmetic hallucinations, FIFO drift, and broken residual calculations.
2. **Execution Latency:** Graph partitioning combined with targeted CP-SAT solving reduced reconciliation latency by **~57%** (averaging 8–9s on `B_OPTIMIZER` vs 21–25s on `A_LLM_ONLY`).
3. **Auditability:** Hypotheses export structured semantic rationales and explicit counterfactual eligibility flags, providing transparent compliance logs.

---

## 10. The Architectural Takeaway

The `reconciliation_eval` architecture establishes a **True Boundary**:
- **Bipartite Graph Partitioning** establishes structural bounds on search complexity.
- **The LLM** acts as an intuitive, context-aware semantic interpreter.
- **The Lexicographic Optimizer** acts as an unyielding mathematical authority.

Together with a pragmatic human-in-the-loop design for long-tail unlinked settlements (<1%), the system delivers deterministic, scalable, and audit-compliant automated bookkeeping.