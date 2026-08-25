# Implementation Specification: LLM-Guided Global Accounting Reconciliation Optimizer and Eval Harness

## 1. Objective

Build an experimental accounting reconciliation system in Python whose purpose is to evaluate one specific architecture:

[
\boxed{
\text{LLM reasoning}
+
\text{global mathematical optimizer tool}
+
\text{deterministic accounting validator}
}
]

The experiment is not intended to reproduce the entire Toro bookkeeping architecture. It is specifically concerned with the **bank reconciliation problem**.

The LLM is the primary reasoning engine. It receives accounting evidence, interprets the evidence, forms reconciliation hypotheses, and may call a mathematical optimizer while reasoning.

The optimizer does not interpret accounting language. It does not decide what a bank description means. It operates on structured candidate relationships produced by the LLM and determines whether those relationships can coexist globally under exact accounting constraints.

The deterministic validator independently verifies the final proposed reconciliation.

The system is evaluated against hidden ground truth.

The central research question is:

> Can a capable LLM, when equipped with an exact global reconciliation optimizer as a tool, reliably reconstruct the correct reconciliation state across materially different companies without company-specific reconciliation algorithms?

The experiment must compare this architecture against an ablation where the same LLM performs reconciliation without access to the optimizer.

---

# 2. Conceptual Reconciliation Problem

For one company, one physical bank account and one accounting period, define two sides of the reconciliation problem.

The **bank side** consists of observed bank movements.

The **book side** consists of canonical monetary accounting objects representing what the company's books believe occurred.

Additional objects such as invoices, receipts, supplier identities, customer identities, cheque references and documents are **evidence objects**. They may help determine which bank and book objects correspond, but they must not automatically be treated as independent monetary balances.

This distinction is important.

If an invoice produced a journal entry, the invoice and the journal entry must not accidentally be counted as two separate amounts of money.

The optimizer therefore receives two broad categories of objects.

The first category is **reconcilable monetary objects**.

Examples include:

```text
bank statement line
posted bank journal line
outstanding customer settlement
outstanding supplier settlement
opening-state cheque
opening-state pending transfer
opening-state TPE settlement
```

These objects carry an amount and can be consumed by reconciliation.

The second category is **evidence objects**.

Examples include:

```text
invoice
receipt
counterparty
bank description
cheque document
TPE report
loan schedule
supporting document
historical relationship
```

These objects help the LLM reason, but they do not create additional monetary capacity unless the test case explicitly represents them as a canonical monetary item.

---

# 3. Overall Runtime

The experimental runtime should behave conceptually as follows:

```text
SCENARIO INPUT
    |
    |
    + current bank statement
    + book-side monetary objects
    + opening reconciliation state
    + invoices and documents
    + counterparties
    + other accounting evidence
    |
    v
LLM RECONCILIATION AGENT
    |
    | interprets evidence
    | creates reconciliation hypotheses
    | assigns relative preferences
    |
    v
GLOBAL_RECONCILIATION_OPTIMIZER TOOL
    |
    | checks exact constraints
    | searches globally compatible configuration
    | reports conflicts
    | reports alternatives
    | reports unresolved objects
    |
    v
LLM
    |
    | accepts optimizer result
    | revises hypotheses
    | creates new hypotheses
    | calls optimizer again if necessary
    | leaves unsupported items unresolved
    |
    v
FINAL STRUCTURED RECONCILIATION PROPOSAL
    |
    v
DETERMINISTIC VALIDATOR
    |
    v
PREDICTED RECONCILIATION STATE
    |
    v
EVAL AGAINST HIDDEN GROUND TRUTH
```

The LLM may call the optimizer repeatedly.

The optimizer is therefore part of the LLM's reasoning environment rather than a post-processing step.

---

# 4. Formal Mathematical Model

The optimizer must receive a finite reconciliation optimization problem.

Define:

[
B={b_1,b_2,\ldots,b_m}
]

as the set of bank-side monetary objects.

Define:

[
J={j_1,j_2,\ldots,j_n}
]

as the set of book-side monetary objects available for reconciliation.

Opening-state monetary objects are included in (J). They retain provenance indicating that they originated in a prior period, but mathematically they are available book-side reconciliation resources.

Define:

[
H={h_1,h_2,\ldots,h_k}
]

as the set of reconciliation hypotheses supplied to the optimizer.

The optimizer does not invent new accounting relationships.

It chooses among hypotheses supplied by the LLM or deterministic candidate-generation layer.

---

# 5. Monetary Amounts

Every monetary value must be represented as an integer.

The unit is:

[
10^{-4}\text{ MAD}
]

Therefore:

[
1.0000\text{ MAD}=10,000
]

solver units.

For example:

[
18,750.0000\text{ MAD}=187,500,000
]

solver units.

Let:

[
R_b \in \mathbb{Z}_{>0}
]

represent the absolute reconcilable amount of bank object (b).

Let:

[
R_j \in \mathbb{Z}_{>0}
]

represent the remaining reconcilable amount of book object (j).

Direction is represented separately from magnitude.

Do not encode inflows as positive and outflows as negative inside capacity calculations unless the implementation has a very explicit signed convention.

For V1, it is safer for objects to contain:

```text
amount_units = positive integer magnitude
direction = INFLOW or OUTFLOW
```

and to validate direction compatibility separately.

Floating-point monetary arithmetic is prohibited.

---

# 6. Reconciliation Hypothesis

A reconciliation hypothesis represents one complete proposed match group.

A hypothesis may contain:

```text
one bank item and one book item
one bank item and several book items
several bank items and one book item
several bank items and several book items
```

Define the bank members of hypothesis (h) as:

[
B_h\subseteq B.
]

Define its book members as:

[
J_h\subseteq J.
]

For every:

[
b\in B_h
]

define:

[
\alpha_{h,b}
]

as the amount of bank object (b) consumed by hypothesis (h).

For every:

[
j\in J_h
]

define:

[
\beta_{h,j}
]

as the amount of book object (j) consumed by hypothesis (h).

These allocations are supplied explicitly with the hypothesis.

The optimizer must never silently change them.

If the LLM believes another allocation is possible, it should submit another hypothesis.

---

# 7. Complete Consumption of a Bank Movement

For V1, if a bank statement line participates in a selected reconciliation hypothesis, that hypothesis must explain the **entire remaining amount of that bank movement**.

Therefore:

[
\alpha_{h,b}=R_b
]

for every bank object (b) contained in hypothesis (h).

This is deliberate.

A single bank movement paying three invoices should be represented as one hypothesis containing:

```text
B17

matched against

A1
A2
A3
```

rather than three independent hypotheses partially consuming B17.

This produces cleaner reconciliation groups and avoids ambiguous partial consumption of an observed bank line.

If the LLM can explain only part of a bank movement, the movement should remain unresolved in V1 unless a canonical accounting adjustment object exists that explains the remainder.

This restriction may be revisited after the evals.

---

# 8. Partial Consumption of Book Objects

Book-side objects may be partially consumed.

Suppose:

[
R_j=20,000
]

MAD and a bank movement settles:

[
8,000
]

MAD.

Then:

[
\beta_{h,j}=8,000
]

is valid.

After selecting the hypothesis:

[
12,000
]

MAD remains available on that book object.

This allows partial invoice settlement and partial receivable/payable settlement.

---

# 9. Internal Monetary Conservation

Every hypothesis must be internally balanced.

For hypothesis (h):

[
\sum_{b\in B_h}\alpha_{h,b}
===========================

\sum_{j\in J_h}\beta_{h,j}.
]

For example:

```text
Bank B1
32,000 MAD

Book A1
18,000 MAD

Book A2
14,000 MAD
```

is valid because:

[
32,000=18,000+14,000.
]

The following is invalid:

[
32,000\neq18,000+13,500.
]

An imbalanced hypothesis must be rejected during optimizer input validation before the optimization model is solved.

An exception is permitted only when the hypothesis explicitly contains another canonical monetary object explaining the difference, such as a separately represented bank fee.

The optimizer must never invent balancing adjustments.

---

# 10. Decision Variables

For each hypothesis:

[
h\in H
]

create a Boolean decision variable:

[
x_h\in{0,1}.
]

Interpretation:

[
x_h=1
]

means hypothesis (h) is selected into the global reconciliation solution.

[
x_h=0
]

means it is not selected.

For every bank object:

[
b\in B
]

create an unresolved Boolean variable:

[
u_b\in{0,1}.
]

Interpretation:

[
u_b=1
]

means bank object (b) remains unresolved.

---

# 11. Bank Exclusivity Constraint

Each bank movement must have exactly one disposition.

It is either part of one selected reconciliation hypothesis or remains unresolved.

Therefore:

[
\sum_{h:b\in B_h}x_h+u_b=1
]

for every:

[
b\in B.
]

This equation is fundamental.

It guarantees that a bank line cannot be reconciled twice.

It also guarantees that the optimizer is never forced to manufacture a match, because:

[
u_b=1
]

is always available.

---

# 12. Book Capacity Constraint

Book objects may participate in multiple reconciliation groups if they represent partially settleable balances, but the total amount consumed cannot exceed the remaining balance.

For every book object (j):

[
\sum_{h:j\in J_h}\beta_{h,j}x_h
\leq
R_j.
]

This is the principal no-double-consumption rule on the book side.

Suppose:

[
R_j=10,000.
]

If H1 consumes:

[
6,000
]

and H2 consumes:

[
5,000,
]

then:

[
6,000x_{H1}+5,000x_{H2}\leq10,000.
]

The optimizer cannot select both.

---

# 13. Book Residual

After optimization, the remaining book-side amount is:

[
Q_j
===

## R_j

\sum_{h:j\in J_h}\beta_{h,j}x_h.
]

The optimizer response must calculate and return (Q_j) for each affected book item.

This gives the LLM information such as:

```text
Invoice A17 had 20,000 MAD outstanding.

Selected reconciliation consumed 8,000 MAD.

12,000 MAD remains outstanding.
```

---

# 14. Currency Compatibility

Every monetary object has:

```text
currency
```

For V1, all members of one hypothesis must use the same currency.

Therefore hypothesis (h) is valid only if:

[
currency(b)=currency(j)
]

for all bank and book members of (h).

Do not perform implicit foreign exchange conversion.

If a later system supports FX, foreign-exchange differences must appear as explicit canonical monetary objects.

---

# 15. Direction Compatibility

Direction compatibility must be deterministic.

Each monetary object should contain a normalized reconciliation direction.

For example:

```text
BANK_INFLOW
BANK_OUTFLOW
BOOK_BANK_DEBIT
BOOK_BANK_CREDIT
OPEN_RECEIVABLE_SETTLEMENT
OPEN_PAYABLE_SETTLEMENT
```

The application should maintain an explicit compatibility mapping.

The optimizer should not infer direction compatibility from natural language.

A hypothesis failing the compatibility mapping is invalid input.

---

# 16. Canonical Existence Constraint

Every ID referenced by a hypothesis must exist in the optimization problem.

If the LLM refers to:

```text
invoice_ABC_998
```

but no corresponding canonical object exists, the optimizer request must return:

```text
INVALID_INPUT
```

with an `UNKNOWN_OBJECT_REFERENCE` diagnostic.

The optimizer must never silently ignore hallucinated IDs.

---

# 17. Hypothesis Eligibility

A hypothesis should contain an explicit field:

```text
eligibility
```

with one of:

```text
SELECTABLE
COUNTERFACTUAL_ONLY
```

`SELECTABLE` means the LLM considers the hypothesis plausible enough that the optimizer may select it.

`COUNTERFACTUAL_ONLY` means the LLM wants the optimizer to reason about the hypothesis but does not authorize its automatic selection.

This prevents weak speculative ideas from entering the selected solution merely because they improve the mathematical objective.

---

# 18. Preference Utility

Each selectable hypothesis receives an integer utility:

[
w_h\in\mathbb{Z}.
]

Use the bounded range:

[
0\leq w_h\leq1000.
]

This is a **relative preference score**, not a probability.

The implementation must never describe:

[
w_h=950
]

as meaning:

[
P(h)=0.95.
]

The score means only that the LLM prefers this hypothesis over lower-scored competing hypotheses.

The score must be interpreted only within the current optimization problem.

---

# 19. Optimizer Objective

The basic objective is:

[
\max
\sum_{h\in H_s}w_hx_h
]

where:

[
H_s
]

is the subset of selectable hypotheses.

Unresolved is the baseline state.

No positive reward should initially be added merely for matching more bank transactions.

This is deliberate.

If reconciliation coverage is rewarded independently of evidence strength, the optimizer can become biased toward false matches.

In V1:

> A bank movement should be reconciled because the LLM supplied a sufficiently preferred hypothesis, not because the optimizer receives a generic reward for reducing the number of unresolved objects.

If evals later demonstrate excessive conservative HOLD behavior, a small unresolved penalty can be tested as a controlled experimental parameter.

It should not be present in the initial baseline.

---

# 20. Hard Constraints Versus Soft Preferences

The distinction must remain strict.

Hard constraints determine whether a mathematical world is permissible.

Examples:

```text
exact amount conservation
book capacity
one disposition per bank line
currency compatibility
direction compatibility
canonical object existence
```

Soft preferences determine which permissible world the LLM considers more plausible.

Examples:

```text
exact invoice reference
similar supplier name
date proximity
cheque-number match
historical payment behavior
description interpretation
same counterparty
document evidence
```

The optimizer must never convert a soft preference into permission to violate a hard constraint.

No amount of semantic confidence can make:

[
18,000+13,500=32,000.
]

---

# 21. Global Optimization Example

Suppose the available bank objects are:

```text
B1 = 10,000
B2 = 10,000
```

and book objects are:

```text
A1 = 10,000
A2 = 10,000
```

The LLM produces:

```text
H1
B1 matched to A1
utility = 750

H2
B1 matched to A2
utility = 650

H3
B2 matched to A1
utility = 980
```

The bank-exclusivity constraints are:

[
x_{H1}+x_{H2}+u_{B1}=1
]

and:

[
x_{H3}+u_{B2}=1.
]

The A1 capacity constraint is:

[
10,000x_{H1}+10,000x_{H3}\leq10,000.
]

Therefore H1 and H3 cannot coexist.

Local selection might choose:

```text
H1 for B1
```

because H1 looks stronger than H2.

Global optimization instead compares:

[
H1=750
]

with:

[
H2+H3=650+980=1630.
]

The optimum becomes:

```text
B1 uses H2 and therefore A2.

B2 uses H3 and therefore A1.
```

This is the exact class of problem the optimizer exists to solve.

---

# 22. Solver Operations

The tool should support two explicit operations.

## OPTIMIZE

`OPTIMIZE` asks:

> What is the best globally feasible reconciliation configuration among the supplied selectable hypotheses?

The optimizer chooses hypotheses.

## CHECK_PROPOSAL

`CHECK_PROPOSAL` asks:

> Are these specifically requested hypotheses jointly feasible?

The request supplies:

```text
forced_hypothesis_ids
```

The optimizer fixes:

[
x_h=1
]

for each forced hypothesis and tests feasibility.

This mode is useful when the LLM wants to test a specific accounting interpretation.

---

# 23. Optimizer Request Contract

The optimizer request should be an explicit serializable model.

Conceptually:

```json
{
  "problem_id": "maghreb_distribution_2026_07",
  "operation": "OPTIMIZE",
  "currency": "MAD",
  "bank_items": [],
  "book_items": [],
  "hypotheses": [],
  "forced_hypothesis_ids": [],
  "solver_options": {
    "max_solve_seconds": 10,
    "max_alternatives": 3,
    "alternative_objective_gap": 50
  }
}
```

All monetary amounts are decimal strings containing integer solver units.

Do not use JSON numbers for potentially large integer money values if serialization through different runtimes could lose precision.

For example:

```json
{
  "amount_units": "187500000"
}
```

is preferred.

---

# 24. Bank Item Contract

A bank item should contain:

```json
{
  "id": "B17",
  "source_type": "BANK_STATEMENT_LINE",
  "date": "2026-07-08",
  "amount_units": "187500000",
  "direction": "OUTFLOW",
  "currency": "MAD",
  "description": "VIR ABC IND FACT445",
  "reference": "FACT445"
}
```

The optimizer itself does not use `description` for semantic reasoning.

The field is retained for traceability and for displaying diagnostics to the LLM.

---

# 25. Book Item Contract

A book item should contain:

```json
{
  "id": "J71",
  "source_type": "POSTED_BOOK_ITEM",
  "origin_period": "2026-07",
  "date": "2026-07-05",
  "remaining_amount_units": "187500000",
  "direction": "OUTFLOW",
  "currency": "MAD",
  "counterparty_id": "SUP-ABC",
  "reference": "445",
  "provenance_refs": [
    "INV-445"
  ]
}
```

An opening-state object might instead contain:

```json
{
  "id": "O17",
  "source_type": "OPENING_STATE_ITEM",
  "origin_period": "2026-06",
  "date": "2026-06-27",
  "remaining_amount_units": "62500000",
  "direction": "OUTFLOW",
  "currency": "MAD",
  "reference": "CHQ-008741"
}
```

---

# 26. Hypothesis Contract

A hypothesis should look conceptually like:

```json
{
  "id": "H17",
  "eligibility": "SELECTABLE",
  "utility": 950,
  "bank_allocations": [
    {
      "bank_item_id": "B17",
      "amount_units": "187500000"
    }
  ],
  "book_allocations": [
    {
      "book_item_id": "J71",
      "amount_units": "187500000"
    }
  ],
  "evidence_refs": [
    "INV-445",
    "SUP-ABC"
  ],
  "semantic_rationale": "Bank label references ABC and FACT445, consistent with supplier ABC Industrie invoice 445."
}
```

A one-to-many hypothesis might contain:

```json
{
  "id": "H23",
  "eligibility": "SELECTABLE",
  "utility": 910,
  "bank_allocations": [
    {
      "bank_item_id": "B23",
      "amount_units": "320000000"
    }
  ],
  "book_allocations": [
    {
      "book_item_id": "J101",
      "amount_units": "180000000"
    },
    {
      "book_item_id": "J108",
      "amount_units": "140000000"
    }
  ]
}
```

The optimizer must validate:

[
320,000,000
===========

180,000,000+140,000,000.
]

---

# 27. Input Validation Phase

Before building the optimization model, perform deterministic request validation.

The solver must not be invoked if the request is structurally invalid.

Validation should include:

```text
unique canonical IDs

unique hypothesis IDs

valid integer money encoding

amount > 0

valid currency

known direction enum

known source type

all hypothesis references exist

bank allocation equals entire referenced bank amount

book allocation does not exceed individual book capacity

internal hypothesis conservation holds

direction compatibility holds

currency compatibility holds

utility range is valid
```

Return:

```text
INVALID_INPUT
```

if any of these fail.

---

# 28. Solver Result Status

The optimizer response must use one of:

```text
OPTIMAL
FEASIBLE
INFEASIBLE
INVALID_INPUT
ERROR
```

`OPTIMAL` means the solver proved the returned solution has the highest objective under the model.

`FEASIBLE` means a valid solution was found but optimality was not proved within the configured solve budget.

This distinction is important.

The LLM must not be told that a merely feasible solution is globally best.

---

# 29. Descriptive Optimizer Response

The optimizer must never return only:

```text
true
```

or:

```text
false.
```

Its response is part of the LLM's reasoning context.

A successful result should contain at least:

```json
{
  "status": "OPTIMAL",
  "objective_value": 2530,
  "selected_hypotheses": [],
  "rejected_hypotheses": [],
  "unresolved_bank_items": [],
  "book_residuals": [],
  "alternatives": [],
  "diagnostics": [],
  "solver_stats": {}
}
```

---

# 30. Selected Hypothesis Explanation

For every selected hypothesis return:

```text
hypothesis ID

utility

bank items consumed

book amounts consumed

why it was globally compatible

directly competing hypotheses

whether selection is stable across alternative solutions
```

The optimizer should not invent semantic accounting explanations.

A valid machine explanation is:

```text
Selected H23.

Utility: 910.

Consumes bank B23 completely.

Consumes J101 by 18,000 MAD and J108 by 14,000 MAD.

No capacity conflicts.

Alternative H24 competes for B23 and was not selected because the resulting global objective was lower by 180.
```

The LLM can translate this into accounting reasoning itself.

---

# 31. Rejected Hypothesis Diagnostics

Every materially competitive rejected hypothesis should contain a reason.

Possible reason codes include:

```text
BANK_ITEM_ALREADY_ALLOCATED

BOOK_CAPACITY_CONFLICT

LOWER_GLOBAL_OBJECTIVE

INCOMPATIBLE_WITH_FORCED_HYPOTHESIS

NOT_SELECTABLE

DOMINATED_BY_ALTERNATIVE_CONFIGURATION
```

For example:

```json
{
  "hypothesis_id": "H1",
  "reason": "BOOK_CAPACITY_CONFLICT",
  "conflicts_with_selected": ["H3"],
  "conflicting_book_items": ["A1"],
  "counterfactual_objective_delta": 880
}
```

---

# 32. Counterfactual Analysis

For important rejected hypotheses, the optimizer should be able to answer:

> What happens if I force this hypothesis to be true?

To calculate this, solve a second optimization problem with:

[
x_h=1.
]

If a feasible solution exists, return:

[
\Delta_h
========

U^*-U_h^{forced}
]

where:

[
U^*
]

is the optimal unconstrained objective and:

[
U_h^{forced}
]

is the best objective when hypothesis (h) is forced.

Example:

```text
H1 can be selected.

However, forcing H1 reduces the best global objective from 1,630 to 750.

It prevents H3 from being selected because both consume A1.
```

This is extremely useful information for the LLM.

---

# 33. Alternative Global Configurations

A single optimal solution can conceal ambiguity.

Suppose two configurations have:

[
U_1=2500
]

and:

[
U_2=2495.
]

Returning only configuration 1 makes the system look much more certain than it actually is.

Therefore the optimizer should calculate up to:

```text
K = max_alternatives
```

alternative feasible global configurations.

After obtaining the optimum (X^*), exclude that exact assignment and solve again.

A no-good cut can conceptually enforce:

[
X\neq X^*.
]

Repeat until:

```text
K alternatives are found

or

no other feasible solution exists

or

the alternative objective is outside the configured gap.
```

For alternative (r):

[
\Delta_r
========

U^*-U_r.
]

Return these alternatives to the LLM.

---

# 34. Solution Stability

Based on alternatives, classify the global solution as:

```text
UNIQUE

STABLE

AMBIGUOUS
```

`UNIQUE` means no alternative feasible configuration exists.

`STABLE` means alternatives exist but are materially worse than the optimum.

`AMBIGUOUS` means at least one alternative exists within the configured objective-gap tolerance.

Do not interpret this as statistical probability.

It is optimization stability.

Example:

```json
{
  "solution_stability": "AMBIGUOUS",
  "best_objective": 2500,
  "second_best_objective": 2495,
  "objective_gap": 5
}
```

The LLM may decide that such a case should remain unresolved or require more evidence.

---

# 35. Infeasibility Diagnostics

When `CHECK_PROPOSAL` is infeasible, the response must identify the conflicting assumptions as precisely as possible.

For obvious deterministic conflicts, compute the explanation directly.

Examples:

```text
H1 and H2 both require the complete amount of bank B17.

H3 and H4 together consume 25,000 MAD from book item A7, but only 20,000 MAD remains.

H7 combines currencies MAD and EUR.
```

For more complex solver-level infeasibility, a diagnostic model may use solver assumptions.

OR-Tools CP-SAT supports assumption literals and can return a subset sufficient to explain infeasibility. The OR-Tools troubleshooting documentation explicitly describes using assumptions for root-cause analysis.

Do **not** make the production diagnostic contract depend entirely on solver-specific unsatisfiable cores.

Current OR-Tools versions have caveats around assumptions, presolve and objective models, so the diagnostic layer should remain isolated behind the optimizer interface.

For V1, use direct deterministic conflict detection whenever possible and use a separate satisfaction-only diagnostic solve when deeper infeasibility analysis is required.

---

# 36. Unresolved Bank Items

For each unresolved bank item return:

```text
bank item ID

candidate hypotheses considered

highest candidate utility

why no candidate was selected

whether alternatives existed

whether unresolved resulted from conflict or lack of hypothesis
```

Reason codes should include:

```text
NO_CANDIDATES

NO_SELECTABLE_CANDIDATES

ALL_CANDIDATES_GLOBALLY_DISPLACED

FORCED_CONSTRAINT_CONFLICT

AMBIGUOUS_GLOBAL_SOLUTIONS
```

Do not output:

```text
UNKNOWN
```

when a more specific mechanical reason is available.

---

# 37. Book Residual Output

For every book item participating in any candidate relationship, return:

[
Q_j
]

the remaining amount after the selected solution.

Example:

```json
{
  "book_item_id": "A17",
  "starting_amount_units": "200000000",
  "consumed_amount_units": "80000000",
  "remaining_amount_units": "120000000"
}
```

This allows the LLM to reason correctly about partial settlements after optimization.

---

# 38. Closing-Balance Projection

The optimizer may calculate a projected reconciliation balance, but it must not become the authoritative closing validator.

Return:

```text
projected closing difference
```

when enough input information exists.

The existing deterministic validator must recalculate the result independently.

The optimizer projection is primarily useful to the LLM while reasoning.

---

# 39. LLM System Instruction

The reconciliation agent should receive an explicit role instruction approximately equivalent to:

> You are an accounting reconciliation reasoner operating inside a constrained reconciliation system. Your job is to determine plausible relationships between bank movements and canonical book-side accounting objects using the evidence supplied to you. You are not responsible for exact global allocation arithmetic. Generate explicit reconciliation hypotheses and use the Global Reconciliation Optimizer whenever competing relationships interact. The optimizer can test whether your hypotheses coexist globally. Treat its mathematical constraints as authoritative. If it reports a conflict, reconsider the accounting interpretation rather than attempting to override the constraint. Unresolved is a valid outcome. Never invent a canonical object that does not exist in the supplied state.

The model should also be informed that optimizer utility values are relative preference rankings rather than probabilities.

---

# 40. LLM Reasoning Procedure

The LLM should be encouraged to follow this conceptual reasoning procedure.

First, inspect each bank movement and retrieve plausible accounting explanations.

Second, construct hypotheses.

Third, identify whether hypotheses interact through shared book resources or competing bank allocations.

Fourth, call the optimizer with the full candidate set rather than optimizing one bank movement at a time.

Fifth, inspect:

```text
selected solution

rejected hypotheses

conflicts

alternatives

unresolved items

solution stability
```

Sixth, revise hypotheses if the optimizer reveals a global problem.

Seventh, call the optimizer again if necessary.

Finally, emit the proposed reconciliation state.

The optimizer-call limit should initially be:

```text
5 calls per scenario
```

for the eval harness.

If the model exceeds the limit, the remaining ambiguous items should be left unresolved.

---

# 41. Final LLM Output

The final model output should be structured.

For compatibility with the intended Toro design, use RFC 6902 JSON Patch.

The patch may add:

```text
proposed reconciliation groups

unresolved bank items

book residuals

opening-state items consumed

opening-state items carried forward
```

The model must not mutate:

```text
bank amounts

book amounts

canonical IDs

historical records
```

---

# 42. Independent Deterministic Validator

The validator receives the original scenario and the final LLM proposal.

It does **not** trust the LLM's arithmetic.

It does **not** trust the LLM's statement that the optimizer approved something.

It recomputes all accounting invariants.

Validation must include:

[
\text{bank exclusivity}
]

[
\text{book capacity}
]

[
\text{monetary conservation}
]

[
\text{currency compatibility}
]

[
\text{direction compatibility}
]

[
\text{canonical existence}
]

[
\text{remaining balances}
]

and:

[
\text{closing reconciliation equation}
]

where relevant.

If any deterministic invariant fails, the run is an eval failure.

---

# 43. Eval Architecture

Every eval case contains:

```text
input.json

ground_truth.json
```

The reconciliation agent receives only `input.json`.

The evaluator receives both.

The ground truth must never be placed in prompts, tool descriptions or optimizer metadata.

---

# 44. Required Eval Companies

Use exactly three initial companies.

The same reconciliation engine, prompt and optimizer implementation must be used for all three.

No company-specific reconciliation code is permitted.

---

# 45. Company A: Atlas Station SARL

This is a Moroccan petrol station.

The scenario should test repetitive settlement behavior.

Include approximately 15 to 25 bank movements.

The case should contain:

```text
straightforward fuel supplier payment

several TPE/card settlement movements

one TPE batch corresponding to several book items

cash deposit

bank fee

maintenance supplier

fleet customer payment

one prior-period outstanding cheque clearing now

duplicate monetary amounts with different counterparties

one deliberately unresolved transaction
```

At least one grouped relationship must exist.

At least one opening-state item must be correctly consumed.

At least one transaction must remain unresolved in ground truth.

---

# 46. Company B: Maghreb Distribution SARL

This is a wholesale distributor.

Include approximately 15 to 25 bank movements.

The scenario must contain:

```text
customer paying several invoices together

supplier receiving several invoices in one payment

partial settlement

supplier credit/refund

two identical amounts belonging to different counterparties

several candidate relationships sharing the same book resources
```

Most importantly, construct at least one **global assignment trap**.

Example:

```text
B1 has plausible candidates A1 and A2.

B2 has only one strong candidate, A1.
```

Ground truth:

```text
B1 uses A2.

B2 uses A1.
```

This scenario is critical because it directly tests whether the optimizer adds value beyond LLM reasoning.

---

# 47. Company C: Atlas Construction Services SARL

This company represents irregular, cross-period accounting.

Include:

```text
supplier cheques

subcontractor payments

customer advance

partial client payment

long payment delay

old opening-state items

messy bank labels
```

At least one current-period item should look locally attractive because its amount matches the bank movement.

However, ground truth should show that the correct relationship is an older opening-state object.

At least one old outstanding item must remain unresolved and carry forward.

---

# 48. Ground Truth Model

Ground truth must include exact reconciliation groups.

For every group include:

```text
bank IDs

book IDs

exact allocations

opening-state provenance if applicable
```

Ground truth must also specify:

```text
correct unresolved bank lines

correct remaining book balances

correct opening items consumed

correct carry-forward items

expected closing bank balance

expected closing book balance

expected reconciliation difference
```

The evaluator should be capable of reconstructing the expected full reconciliation state from this object.

---

# 49. Eval Configurations

Run two configurations.

## Configuration A

```text
LLM + deterministic validator
```

The optimizer tool is unavailable.

## Configuration B

```text
LLM + global optimizer tool + deterministic validator
```

Use the same:

```text
LLM model

prompt

temperature

input

scenario

validator
```

to the extent possible.

The only intended experimental variable is optimizer access.

---

# 50. Repeated Runs

Each company/configuration pair should initially run:

[
N=10
]

times.

This creates:

[
3\times2\times10=60
]

reconciliation runs.

If LLM usage cost makes ten runs impractical during development, use five while debugging and ten for the first serious evaluation.

---

# 51. Primary Safety Metric

Define false reconciliation precision failure as:

[
FRR
===

\frac{\text{incorrect selected reconciliation groups}}
{\text{all selected reconciliation groups}}.
]

The initial research gate should require:

[
FRR=0
]

across all optimizer-enabled serious eval runs.

A false HOLD is preferable to a false reconciliation.

---

# 52. Reconciliation Recall

Define:

[
Recall
======

\frac{\text{correctly selected ground-truth groups}}
{\text{all autonomously reconcilable ground-truth groups}}.
]

This measures how much work the system actually completes.

For the initial research gate, target:

[
Recall\geq90%.
]

This is a research target, not a production promise.

---

# 53. HOLD Accuracy

Some scenarios intentionally contain insufficient evidence.

Define:

[
HoldAccuracy
============

\frac{\text{correctly unresolved designed-HOLD items}}
{\text{all designed-HOLD items}}.
]

Target:

[
HoldAccuracy=100%.
]

The system should not gain recall by making unsupported guesses.

---

# 54. Global State Accuracy

For each run determine whether all of the following exactly match ground truth:

```text
selected reconciliation groups

exact allocations

unresolved bank movements

book residual amounts

opening-state items consumed

carry-forward items
```

Define:

```text
GLOBAL_STATE_EXACT = true or false
```

This is stricter than per-match accuracy.

---

# 55. Closing Arithmetic

For every scenario designed to close:

[
ExpectedClosingBalance
======================

ActualStatementClosingBalance.
]

The difference must equal exactly:

[
0
]

integer units.

Not approximately zero.

---

# 56. Optimizer Incremental Value

For each scenario calculate:

[
\Delta Recall
=============

## Recall_{optimizer}

Recall_{LLM-only}
]

and:

[
\Delta FRR
==========

## FRR_{optimizer}

FRR_{LLM-only}.
]

Also count:

```text
number of LLM local decisions corrected by optimizer

number of optimizer conflicts that caused useful LLM revision

number of optimizer calls that changed nothing

number of optimizer suggestions rejected by LLM

number of cases where optimizer introduced no measurable benefit
```

We need evidence that the optimizer actually earns its complexity.

---

# 57. Optimizer-Specific Eval Metrics

Record:

```text
optimal versus merely feasible solve status

solve duration

number of decision variables

number of hard constraints

best objective

second-best objective

alternative solution count

solution stability

counterfactual solve count
```

These measurements will later tell us whether reconciliation problems are becoming computationally difficult.

---

# 58. LLM-Specific Metrics

Record:

```text
number of hypotheses generated

number of incorrect hypotheses generated

number of optimizer calls

number of hypothesis revisions

number of hallucinated IDs

number of invalid tool requests

number of invalid final JSON patches

tokens

latency
```

This allows failures to be attributed correctly.

---

# 59. Failure Taxonomy

Every failed run must be assigned one or more mechanical failure categories.

Use at least:

```text
LLM_SEMANTIC_ERROR

LLM_MISSED_HYPOTHESIS

LLM_FALSE_HYPOTHESIS

LLM_HALLUCINATED_OBJECT

LLM_FAILED_TO_USE_TOOL

LLM_FAILED_TO_REVISE_AFTER_CONFLICT

OPTIMIZER_MODELING_ERROR

OPTIMIZER_GLOBAL_SELECTION_ERROR

OPTIMIZER_TIMEOUT

INSUFFICIENT_CANDIDATE_SET

DETERMINISTIC_VALIDATION_FAILURE

INCORRECT_HOLD

UNSUPPORTED_SCENARIO_STRUCTURE
```

Do not respond to failures by immediately adding mathematical machinery.

First identify which category dominates.

---

# 60. Logging and Reproducibility

Each run must persist a complete execution artifact.

Record:

```text
scenario ID

scenario hash

ground truth hash

run ID

timestamp

LLM provider

exact model name

temperature and sampling parameters

system prompt hash

tool schema hash

optimizer implementation version

solver parameters

all messages sent to LLM

every hypothesis generated

every optimizer request

every optimizer response

all alternative solutions

all counterfactual checks

final JSON Patch

deterministic validation output

eval metrics
```

The purpose is to make every surprising result reproducible.

---

# 61. Python Project Boundary

This experimental system should be implemented entirely in Python.

It does not need to interact with production Go code for the first eval.

Use clean Python domain models, preferably Pydantic or equivalent typed validation.

Suggested conceptual package structure:

```text
reconciliation_eval/

    domain/
        bank.py
        books.py
        evidence.py
        opening_state.py
        hypothesis.py

    optimizer/
        protocol.py
        model.py
        cp_sat.py
        diagnostics.py
        alternatives.py

    agent/
        reconciliation_agent.py
        prompt.py
        optimizer_tool.py

    validation/
        deterministic.py

    evaluation/
        ground_truth.py
        metrics.py
        evaluator.py
        runner.py

    scenarios/
        atlas_station/
        maghreb_distribution/
        atlas_construction/
```

The optimizer interface must be independent from CP-SAT.

---

# 62. Implementation Order

Implement in this exact order.

First, implement domain models and serialization.

Second, implement deterministic optimizer request validation.

Third, implement the mathematical optimizer without any LLM.

Create manually written hypotheses and prove that the solver:

```text
enforces capacity

enforces bank exclusivity

chooses globally superior configurations

supports grouped matches

supports partial book settlement

leaves unmatched bank movements unresolved
```

Fourth, implement optimizer diagnostics and alternative-solution enumeration.

Fifth, implement the deterministic final validator.

Sixth, create one small eval scenario with manually supplied hypotheses.

Seventh, integrate the LLM and allow it to generate those hypotheses itself.

Eighth, allow iterative optimizer tool calls.

Ninth, implement the three complete companies.

Tenth, execute the ablation eval.

Do not begin by asking the LLM to solve all three companies while the optimizer itself is still unverified.

---

# 63. Required Unit Tests for the Optimizer

Before connecting an LLM, write deterministic tests for at least these mathematical situations.

### Exact one-to-one

```text
B1 = 10,000
A1 = 10,000
```

H1 must be selectable.

### Internal imbalance

```text
B1 = 10,000
A1 = 9,999
```

Hypothesis must be rejected before solving.

### Double bank allocation

Two hypotheses both consume B1.

Both cannot be selected.

### Book capacity overflow

```text
A1 remaining = 10,000

H1 consumes 6,000
H2 consumes 5,000
```

Both cannot be selected.

### Partial book settlement

```text
A1 remaining = 20,000
H1 consumes 8,000
```

Residual must equal:

[
12,000.
]

### Global assignment

Use the B1/B2/A1/A2 example above and prove that the optimizer chooses the globally superior configuration.

### Unresolved

A bank line with no hypothesis must result in:

```text
u_b = 1.
```

### Opening-state consumption

An opening-state item must behave as a valid book-side monetary resource.

### Ambiguous optimum

Create two equally scoring global configurations and verify that alternative enumeration reports ambiguity.

### Counterfactual

Force a suboptimal hypothesis and verify that the optimizer returns the objective loss and displaced hypotheses.

---

# 64. What the Optimizer Must Not Do

The optimizer must not:

```text
interpret bank descriptions

guess counterparties

generate hypotheses

modify monetary amounts

invent accounting adjustments

create invoices

retrieve documents

apply business-specific semantic accounting rules

declare the accounting period officially closed
```

Its responsibility is exactly:

> Select and analyze globally consistent configurations of explicitly supplied reconciliation hypotheses under deterministic mathematical constraints.

---

# 65. What the LLM Must Not Do

The LLM must not be trusted to:

```text
guarantee exact monetary equality

guarantee that an invoice was not consumed twice

guarantee global optimality

guarantee capacity constraints

invent canonical objects

override optimizer hard constraints

override deterministic validator failures
```

Its responsibility is:

> Interpret accounting evidence, generate plausible reconciliation hypotheses, use the optimizer intelligently, and decide when evidence remains insufficient.

---

# 66. Decision Rule After the Experiment

If the optimizer-enabled architecture achieves:

[
FRR=0
]

across the curated scenarios and repeated runs, while achieving high reconciliation recall and exact closing state, expand the eval corpus.

Do not immediately add additional mathematics.

The next step should be more scenarios and eventually anonymized real accounting cases.

If the architecture fails, inspect the failure taxonomy.

If failures are primarily:

```text
semantic interpretation failures
```

improve LLM context, prompting or evidence retrieval.

If failures are:

```text
missing hypothesis failures
```

improve candidate generation.

If failures are:

```text
global combinatorial failures
```

improve the optimizer model.

If failures involve:

```text
uncertainty between many globally similar worlds
```

then probabilistic methods may become justified.

If failures involve:

```text
large structural clusters of unresolved accounting state
```

then spectral or graph-level analysis may become justified.

Additional mathematical systems should therefore be introduced only in response to observed deficiencies.

---

# 67. Final Definition of the Experiment

This experiment is testing the proposition that bank reconciliation can be decomposed into two complementary forms of reasoning.

The first is semantic:

[
\boxed{\text{What probably happened?}}
]

That belongs to the LLM.

The second is global and mathematical:

[
\boxed{\text{Can all of those proposed explanations be true simultaneously?}}
]

That belongs to the optimizer.

The final accounting question is deterministic:

[
\boxed{\text{Does the accepted reconciliation satisfy exact accounting invariants?}}
]

That belongs to the validator.

The optimizer is therefore neither an accountant nor an AI model.

Formally, it solves:

[
\boxed{
\max_{x}
\sum_{h\in H_s}w_hx_h
}
]

subject to:

[
\boxed{
x_h\in{0,1}
}
]

[
\boxed{
\sum_{h:b\in B_h}x_h+u_b=1
\quad\forall b\in B
}
]

[
\boxed{
u_b\in{0,1}
}
]

[
\boxed{
\sum_{h:j\in J_h}\beta_{h,j}x_h
\leq
R_j
\quad\forall j\in J
}
]

and every supplied hypothesis must already satisfy:

[
\boxed{
\sum_{b\in B_h}\alpha_{h,b}
===========================

\sum_{j\in J_h}\beta_{h,j}
}
]

together with currency, direction, provenance and canonical-reference validity.

Its output is not merely a Boolean answer.

Its output is a structured explanation of the global accounting configuration:

```text
what was selected

what was rejected

what remains unresolved

what capacity remains

which hypotheses conflict

what alternatives exist

how much objective quality is lost under counterfactual choices

whether the solution is unique, stable or ambiguous

whether the result is proven optimal or merely feasible
```

That structured mathematical response is returned to the LLM so that the LLM can continue reasoning.

That interaction between semantic intelligence and exact global optimization is the architecture being evaluated.
