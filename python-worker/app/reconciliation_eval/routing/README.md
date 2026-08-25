Here is the updated **Developer & Architecture Guide** with direct file pointers and mathematical explanations instead of inline code snippets.

---

# Developer & Architecture Guide: Multi-Account Routing Module (`routing/`)

This module implements a **Two-Stage Solver Architecture** designed to pre-route uncoded general ledger entries (`BookItem` objects) to their target bank accounts (`BankItem` streams) before running low-level reconciliation.

---

## 🎯 Problem Statement & Rationale

When reconciling large enterprises with multiple bank accounts (e.g., Main Checking, Payroll, Tax Reserve, Petty Cash), evaluating all transactions in a single global space causes three failure modes:

1. **Context Window Explosion:** Feeding transactions from all accounts into an LLM context window causes hallucinated matches and high token costs.
2. **Cross-Account Leakage (The Swapping Trap):** Mathematical ambiguity allows an invoice for 10,000 MAD paid out of Account A to be incorrectly matched against an identical 10,000 MAD payment in Account B.
3. **Combinatorial Explosion:** Running linear programming or integer solvers over an unpartitioned cross-account matrix causes $O(2^N)$ solver degradation.

### The Solution

The pre-routing engine mathematically and semantically partitions the incoming dataset into isolated, single-account sub-problems. Downstream reconciliation solvers operate strictly on guaranteed single-account buckets.

---

## 📐 Mathematical Framework & Pipeline Phases

The routing engine uses a combined combinatorial and integer optimization model operating on scaled integer currency units ($10^{-4}$).

### Phase 1: Feasibility Bound ($F_{i,a}$)

For every invoice $i \in I$ and bank account $a \in A$, Phase 1 generates a binary feasibility matrix $F_{i,a} \in \{0, 1\}$.

$$F_{i,a} = \begin{cases}  1 & \text{if } \exists b \in B_a : \text{Amount}(b) = \text{Amount}(i) \quad \text{(Direct 1:1 Match)} \\ 1 & \text{if } \exists S \subseteq B_a : \sum_{b \in S} \text{Amount}(b) = \text{Amount}(i) \quad \text{(Grouped Many-to-1 Match)} \\ 1 & \text{if } \exists b \in B_a : \text{Amount}(b) > \text{Amount}(i) \quad \text{(Consolidated Batch Payment)} \\ 0 & \text{otherwise} \end{cases}$$

* **Implementation File:** `routing/feasibility.py`
* **Key Functions:**
* `build_feasibility_matrix()`: Iterates through unassigned book items across all available accounts to populate the global matrix $F_{i,a}$.
* `_find_plausible_bank_ids()`: Evaluates single-line matches, subset combinations, and consolidated transfer bounds ($B_a > \text{Amount}_i$).
* `_can_form_subset_sum()`: Runs a $O(N \cdot T)$ dynamic programming set-update loop to verify if any subset of bank lines sums to an invoice target.



---

### Phase 2: Semantic Score Matrix ($S_{i,a}$)

For pairs where $F_{i,a} = 1$, an asynchronous LLM call evaluates evidence text, vendor names, and reference strings to generate a utility score $S_{i,a} \in [0, 1000]$. If $F_{i,a} = 0$, $S_{i,a}$ is automatically set to $0$.

* **Implementation Files:** `routing/prompt.py` and `routing/scorer.py`
* **Key Functions:**
* `score_all_invoices()`: Orchestrates batch scoring calls to OpenAI using structured JSON output (`RoutingScoreResponse`).
* `score_invoice_routing()`: Applies system-level optimization shortcuts:
* **Zero-Feasible Guard:** If $F_{i,a} = 0 \ \forall a$, skips the LLM call and flags the item as unroutable.
* **Single-Feasible Bypass:** If exactly one account is feasible ($\sum_a F_{i,a} = 1$), skips the LLM call and sets $S_{i,a} = 1000$ to save API latency.





---

### Phase 3: Global Integer Program (CP-SAT)

The global partitioning model solves the following optimization problem using Google OR-Tools CP-SAT:

$$\max \sum_{i \in I} \sum_{a \in A} S_{i,a} \cdot y_{i,a}$$

Subject to:

* **Feasibility Constraint:** $y_{i,a} \le F_{i,a} \quad \forall i \in I, a \in A$
* **Account Exclusivity:** $\sum_{a \in A} y_{i,a} \le 1 \quad \forall i \in I$
* **Monetary Capacity Limit:** $\sum_{i \in I} \text{Amount}_i \cdot y_{i,a} \le \sum_{b \in B_a} \text{Amount}_b \quad \forall a \in A$

Where $y_{i,a} \in \{0, 1\}$ is the binary decision variable indicating whether invoice $i$ is assigned to account $a$.

* **Implementation File:** `routing/optimizer.py`
* **Key Functions:**
* `optimize_global_routing()`: Formulates the boolean decision variables `y[book_id, acc_id]`, applies hard constraints, enforces a 10-second solver timeout guard, and returns a `GlobalRoutingState`.



---

### Phase 4: Isolated Orchestration

Takes the global routing decisions $y_{i,a}$ from Phase 3, constructs isolated `BookItem` subsets for each account ID, and executes the low-level reconciliation solver per account.

* **Implementation File:** `routing/orchestrator.py`
* **Key Functions:**
* `run_multi_account_pipeline()`: Serves as the primary public interface. Glues together Phases 1–3, splits datasets by account partition, and spawns independent `run_reconciliation_agent()` processes.



---

## 📊 Diagnostic Benchmark & Scenarios

Integration test scenarios are maintained in `routing/run_routing_scenarios.py` to benchmark pipeline performance against accounting edge cases:

```bash
export OPENAI_API_KEY="your-api-key"
python3 -m routing.run_routing_scenarios

```

### Verified Test Scenarios

| Scenario | Edge Case Challenge | Pipeline Behavior |
| --- | --- | --- |
| **1. Date Proximity** | Two identical 10,000 MAD invoices (Jan vs. Feb) in different accounts. | LLM evaluates dates and scores `J_JAN -> MAIN` (960) and `J_FEB -> SECONDARY` (980). CP-SAT locks them into separate partitions, preventing a cross-account swap. |
| **2. Grouped Payment** | 2,000 MAD & 3,000 MAD invoices matching a single 5,000 MAD bank transfer. | Phase 1 $B > J$ condition marks both feasible in `OPS_ACCOUNT`. CP-SAT routes both to `OPS_ACCOUNT`, and Phase 4 matches them as a consolidated group (`H1_grouped`). |
| **3. Orphaned Invoice** | A 500,000 MAD invoice exceeding all available account capacities. | Phase 1 sets $F_{i,a} = 0$ everywhere. Bypasses Phase 2 LLM entirely. Phase 3 registers the item as `UNROUTABLE`. |