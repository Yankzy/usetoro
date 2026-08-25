# Reconciliation Evaluation Framework: White Paper

## 1. Purpose

`reconciliation_eval` is an evaluation harness for testing AI-assisted bank-to-ledger reconciliation under accounting constraints. Its purpose is not to let a language model alter financial records. Instead, it measures whether an agent can propose useful reconciliation groups while deterministic code verifies accounting invariants and a constraint solver evaluates the proposals globally.

The framework is designed to answer a practical question:

> Given canonical bank movements, open book items, and supporting evidence, can an AI-assisted workflow produce a valid, auditable reconciliation state and how does its performance change when global optimization is available?

The package covers the full evaluation lifecycle:

1. Define typed reconciliation inputs and expected outcomes.
2. Route multi-account book items into account-scoped reconciliation problems.
3. Generate deterministic, mathematically perfect candidate relationships via CP-SAT.
4. Ask an LLM to reason over evidence and assign semantic utility scores to hypotheses.
5. Use a Lexicographic CP-SAT optimizer to select a globally compatible set of hypotheses.
6. Independently validate the final proposed state.
7. Compare routing and reconciliation outcomes to scenario ground truth and persist reproducible run artifacts.

---

## 2. System Boundary and Design Principles

The framework works with absolute, integer-denominated amount units rather than floating-point money. A `BankItem` is a canonical bank statement movement; a `BookItem` is an open ledger item with remaining reconcilable capacity. Both carry identifiers, date, direction, currency, and descriptive or reference information. Book items can also retain counterparty and provenance references.

Three principles define the design:

1. **The True Boundary (Semantic vs. Math):** The LLM is the "Semantic Engine". It reads descriptions, references, and assigns subjective utility scores. It is *not* trusted to do math. The CP-SAT optimizer is the "Mathematical Authority". It enforces rigid accounting rules and objectively selects the best state. 
2. **The optimization is global.** A locally plausible match can still be wrong if it consumes a bank line or book balance needed by a stronger alternative. CP-SAT selects combinations, not isolated suggestions.
3. **Evaluation is reproducible.** Each run records scenario and ground-truth hashes, model/configuration, latency, final patch, validation result, and metrics.

---

## 3. End-to-End Workflow

```text
Scenario inputs + evidence + ground truth
                 |
                 v
       [Multi-account routing / account partitioning]
                 |
                 v
   Deterministic CP-SAT candidate generation
                 |
                 v
       LLM Semantic Scoring Engine
                 |
                 +--------------------+
                 |                    |
                 v                    v
      LLM-only proposed state    Lexicographic CP-SAT selection
                 |                    |
                 +---------+----------+
                           v
             Deterministic proposed-state validation
                           |
                           v
             Ground-truth metrics and saved run artifact
```

The evaluation runner executes both configurations where appropriate:

- **A — LLM only:** the agent returns its final `ProposedState` without access to the optimizer tool.
- **B — LLM + optimizer:** the agent can submit scored hypotheses to the global optimizer, which enforces the final state.

---

## 4. Reconciliation Data and State Model

### Inputs

- `BankItem`: immutable bank line with a canonical ID, integer amount, direction, currency, date, description, and optional reference.
- `BookItem`: open book-side item with a canonical ID, remaining integer amount, direction, currency, date/origin period, and optional counterparty, reference, and provenance.
- Scenario evidence: free-text operational context available to the agent.
- `GroundTruth`: expected bank/book groupings, expected unresolved bank IDs, and optional book residuals for evaluation.

### Proposal output

The final `ProposedState` has three components:

- `matches`: proposed match groups;
- `unresolved_bank_ids`: bank lines deliberately left unmatched; and
- `expected_book_residuals`: optional residual claims that are checked against calculated balances.

---

## 5. Candidate Generation and LLM Semantic Reasoning

Before the agent reasons, `agent/candidate_generation.py` produces a mathematically perfect list of plausible relationships using a local CP-SAT subset-sum model. It considers only direction-compatible items and emits combinations of any size (1:1, N:1, etc.) that sum perfectly.

`agent/reconciliation_agent.py` gives the LLM the canonical source records, scenario evidence, and generated candidates. The LLM's job is purely semantic: interpret dates, counterparties, references, and descriptions, and express a proposed state as a series of hypotheses.

Crucially, the LLM assigns a **Utility Score (0 - 1000)** to each hypothesis. This represents its confidence in the semantic evidence (e.g., 950 for an exact reference match, 500 for a plausible amount match with no reference).

---

## 6. Global Constraint Optimization and "Utility Farming"

When the agent invokes the optimizer, it supplies `ReconciliationHypothesis` objects. The solver uses **Lexicographic Optimization** to defeat LLM hallucinations. 

### The Utility Farming Trap
A naive optimizer that simply maximizes the sum of utility scores falls prey to "Utility Farming". If a 10,000 MAD payment can be matched to a single 10,000 MAD invoice (Utility 950), or fragmented into three separate invoice matches (Utility 900 each), a naive optimizer will pick the fragmented hallucination because `900 + 900 + 900 > 950`.

### The Lexicographic Objective Function
To make the optimizer immune to LLM semantic errors, we enforce strict, multi-tiered mathematical priorities that are orders of magnitude apart. A lower tier can never overpower a higher tier:

1. **Tier 1 (Maximize Money Cleared - Weight `1,000,000,000`)**: The absolute truth of accounting. Reconciling $100,000 is always mathematically superior to $99,000.
2. **Tier 2 (Maximize Items Cleared - Weight `100,000`)**: Clutter reduction. If money is tied, clearing 10 open invoices is superior to clearing 1.
3. **Tier 3 (Consolidation Penalty - Weight `-10,000`)**: Occam's Razor. We subtract a massive penalty for every hypothesis used. This absolutely stops Utility Farming. If two states clear the same money and items, the state with the fewest hypotheses is deemed mathematically simpler and wins.
4. **Tier 4 (Maximize LLM Utility - Weight `1`)**: The semantic backstop. Only when the math and item counts are a perfect identical tie does the optimizer defer to the LLM's subjective score.

This architecture guarantees that the optimizer will objectively overrule the LLM whenever a mathematically superior or simpler state exists.

---

## 7. Independent Deterministic Validation

The final proposal is validated independently of the LLM and of any intermediate optimizer response. `validation/deterministic.py` verifies:

- every referenced bank and book ID exists;
- each original bank item has exactly one disposition: matched or unresolved;
- a bank item cannot be both matched and unresolved, or appear in more than one match;
- each bank allocation fully consumes the referenced bank line;
- every match conserves monetary value between bank and book allocations;
- all allocations within a match use a compatible currency and direction;
- aggregate use of every book item stays within remaining capacity; and
- any claimed book residual equals the computed residual.

---

## 8. Evaluation Methodology and Metrics

Scenarios under `scenarios/` provide inputs and hidden ground truth. `evaluation/runner.py` runs the selected scenario under the A and B configurations, calculates metrics, and writes an artifact.

| Metric | Meaning |
| --- | --- |
| `FRR` | Fraction of proposed groups that do not match any ground-truth group. |
| `Recall` | Fraction of ground-truth groups recovered by the proposal. |
| `HoldAccuracy` | Fraction of expected unresolved bank IDs correctly held. |
| `GLOBAL_STATE_EXACT` | True only when all proposed groups are correct, expected groups recovered, and unresolved state exactly matches. |

---

## 9. Completed Capabilities and Next Steps

1. **Expanded Scenarios and CP-SAT Generation:** Exact CP-SAT candidate generation handles partial settlements and adversarial distractors effortlessly without brute-force handicaps.
2. **Lexicographic Optimization:** The B_OPTIMIZER mathematically guarantees the best global state and is immune to Utility Farming hallucinations.
3. **Integrated Routing Evaluation:** Multi-account routing assignments are tracked alongside the core pipeline.

### Next Steps

1. **Expand Multi-Currency Support:** Add test coverage for cross-currency reconciliation and handle minor FX rounding differences natively.
2. **Persist Artifacts in a Central Registry:** Persist evaluation artifacts in a durable remote storage layer to track longitudinal model performance.

---

## 10. The Ultimate Takeaway

This dataset proves our core thesis. LLMs are excellent at reading text and generating potential ideas (hypotheses), but they are terrible mathematicians and lack the structural logic to enforce mutual exclusivity. They are highly prone to hallucinating complex, fragmented accounting structures.

The Lexicographic Optimizer is the ultimate mathematical backstop. It does not replace the LLM; it creates a **True Boundary**. Together, they form a flawless system where the LLM acts purely as the creative semantic engine, and the Optimizer acts as the rigid, unyielding logical gatekeeper that defeats hallucinations.