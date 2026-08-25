# Product Requirements Document: LLM-Guided Global Reconciliation Optimizer V1

## Status

Proposed V1 architecture and evaluation plan.

This document defines the reconciliation architecture that Toro should build and evaluate before pursuing more elaborate probabilistic or spectral mathematical systems.

The working hypothesis is deliberately simple:

> **Use the LLM as the primary accounting reasoning system. Give the LLM a deterministic global mathematical optimizer as a tool. Allow the LLM to form hypotheses, test those hypotheses against the optimizer, revise them when necessary, and finally propose a reconciliation. Deterministic accounting validation remains the final authority.**

The purpose of V1 is to determine experimentally whether this combination is sufficiently reliable across materially different companies to justify making it the production reconciliation architecture.

---

# 1. Context

Toro's bookkeeping system has been divided into four stages:

[
\text{Opening State}
\rightarrow
\text{Transaction Understanding}
\rightarrow
\text{Reconciliation}
\rightarrow
\text{Closing State}
]

The opening-state and reconciliation-state foundation has already been implemented.

The current architecture treats reconciliation as something distinct from transaction classification. The PCM DAG may determine economic intent and propose journal treatment, but classification itself is not evidence that a transaction has been reconciled.

Toro also already distinguishes canonical bank records from canonical Shadow ERP book records. The reconciliation state references these immutable records rather than becoming another transaction store.

This architecture should remain intact.

The new system described in this PRD lives entirely inside the **reconciliation stage**.

It receives the opening state, the current bank statement, the current book state and available accounting evidence. Its purpose is to determine how bank reality and book reality relate.

The resulting reconciliation is then passed into the existing deterministic closing-state machinery.

---

# 2. Why V1 Should Lean Hard Into the LLM

Earlier research considered Bayesian inference, factor graphs, minimum-cost flow, Monte Carlo methods, Graph Fourier Transform and other mathematical systems.

These remain potentially useful.

However, introducing them before proving that they solve an actual deficiency in LLM reasoning risks building mathematical machinery that duplicates something a capable LLM already performs more flexibly.

Accounting contains a large semantic component.

Consider a bank label:

```text
VIR ETS EL MANSOURI REG FACT 0526 SOLDE
```

Understanding that this probably refers to a supplier, that `REG` probably means a settlement, that `FACT` refers to a facture, and that `0526` may identify an invoice or accounting period is a semantic reasoning task.

A traditional optimizer does not understand this.

Encoding every possible abbreviation, banking convention, business practice and accounting interpretation into mathematical factors or hand-maintained rules would create a brittle system.

Rules also evolve. Businesses behave differently. Accounting practice changes. Bank descriptions vary. New forms of payment appear.

An LLM is much better suited to this part of the problem.

The purpose of mathematics in V1 is therefore **not to replace LLM intelligence**.

Mathematics should perform the work for which mathematics has structural advantages over an LLM.

An LLM can reason that two invoices probably explain a payment.

An optimizer can prove whether accepting those two invoices causes another payment to become impossible to reconcile.

An LLM can reason about an abbreviated supplier name.

An optimizer can guarantee that the same invoice amount has not been consumed twice.

An LLM can reason that a payment probably clears an outstanding cheque from the opening state.

The deterministic reconciliation layer can verify that the amounts, currencies, capacities and closing balances actually work.

This gives V1 a clear division of responsibility.

---

# 3. Core Design Principle

The architecture is based on three kinds of intelligence.

The **LLM provides semantic intelligence**.

The **optimizer provides global combinatorial intelligence**.

The **existing reconciliation service provides accounting truth**.

Conceptually:

```text
                     ACCOUNTING EVIDENCE
                             |
                             v
                            LLM
                             |
                  forms hypotheses about
                  what probably happened
                             |
                             v
              GLOBAL RECONCILIATION OPTIMIZER
                             |
                tests whether hypotheses
                can coexist mathematically
                             |
                             v
                            LLM
                             |
                  reasons over optimizer
                  output and may revise
                  its hypotheses
                             |
                             v
                     RFC 6902 JSON Patch
                             |
                             v
                DETERMINISTIC VALIDATION
                             |
                             v
              IMMUTABLE RECONCILIATION STATE
```

The optimizer is therefore a **tool available to the LLM**, not an independent intelligence pipeline running after the LLM has finished.

This difference is fundamental.

The LLM should be able to ask the optimizer questions while it is reasoning.

---

# 4. What the LLM Is Responsible For

The LLM is responsible for interpreting the accounting world presented to it.

It receives a deliberately constructed reconciliation context containing only evidence relevant to the current bank account and reconciliation period.

The context may include the opening state, current bank statement lines, posted Shadow ERP journal lines, outstanding customer and supplier items, invoices, receipts, cheque references, TPE settlements, loan information and other evidence available to the system.

The LLM should be informed explicitly that it is operating inside Toro's reconciliation engine.

It should understand that its job is not to generate prose explaining accounting.

Its job is to reason about relationships between canonical accounting objects.

For example, the LLM might observe:

```text
Bank B17

2026-07-04
-18,750 MAD
VIR ABC IND FACT 445
```

and discover:

```text
Invoice A71
ABC Industrie SARL
Invoice 445
18,750 MAD
Outstanding

Invoice A93
ABC Industrie SARL
Invoice 449
18,750 MAD
Outstanding
```

The LLM may reasonably hypothesize:

```text
B17 probably settles A71.
```

That hypothesis is useful.

It is not yet reconciliation truth.

The LLM should therefore construct one or more **candidate reconciliation hypotheses**.

For ambiguous cases it should be encouraged to preserve alternatives rather than forcing itself to choose immediately.

For example:

```text
Hypothesis H1:
B17 settles A71

Hypothesis H2:
B17 settles A93

Preference:
H1 is substantially stronger because bank reference 445
matches invoice reference 445.
```

The optimizer then receives those hypotheses.

---

# 5. The Global Optimizer as an LLM Tool

The tool should expose an abstraction such as:

```text
GLOBAL_RECONCILIATION_OPTIMIZER
```

The LLM should not need to know whether the implementation uses CP-SAT, MILP, minimum-cost flow or another optimization technology.

That implementation can change later.

The semantic tool contract is:

> Given these canonical accounting objects and these candidate relationships, determine whether there is a globally consistent configuration of those relationships under the supplied accounting constraints. If there are several feasible configurations, return the best configuration according to the supplied preferences. If the proposed configuration is impossible, explain the mathematical conflict and, when possible, return a feasible alternative constructed from the supplied candidate hypotheses.

That is the entire conceptual contract.

This matters because the optimizer becomes a reusable capability.

The LLM reasons in accounting language.

The optimizer reasons in variables and constraints.

---

# 6. Why Global Optimization Is Needed

The primary reason for introducing an optimizer is a weakness that exists even with a very capable LLM: **local decisions can interfere with other local decisions**.

Consider:

```text
Bank B1 = 10,000 MAD
Bank B2 = 10,000 MAD

Book A1 = 10,000 MAD
Book A2 = 10,000 MAD
```

Suppose the evidence is:

```text
B1 -> A1 looks very plausible
B1 -> A2 looks plausible

B2 -> A1 is extremely strong
B2 -> A2 is impossible
```

An LLM processing `B1` first might choose:

```text
B1 -> A1
```

It has made a perfectly reasonable local decision.

But it has now made `B2` impossible to explain.

The globally superior configuration is:

```text
B1 -> A2
B2 -> A1
```

The optimizer sees both relationships simultaneously.

That is the capability we are buying with mathematics.

We are **not** introducing mathematical optimization because arithmetic is cheaper than LLM tokens.

We are introducing optimization because global constraint satisfaction is structurally different from semantic reasoning.

---

# 7. Mathematical Representation

The optimizer should operate on candidate reconciliation hypotheses.

Let:

[
H={h_1,h_2,\ldots,h_n}
]

be the candidate hypothesis set generated by the LLM and deterministic candidate discovery.

For each hypothesis (h), define a binary decision variable:

[
x_h\in{0,1}
]

where:

[
x_h=1
]

means the hypothesis has been selected into the proposed global reconciliation configuration.

A hypothesis can represent a one-to-one match:

[
B_1\leftrightarrow A_1
]

a one-to-many match:

[
B_1\leftrightarrow{A_1,A_2,A_3}
]

or a many-to-one match:

[
{B_1,B_2}\leftrightarrow A_1.
]

The optimizer does not need to understand why the LLM proposed the relationship.

It only needs the canonical IDs, proposed allocations and preference metadata.

---

# 8. Exact Monetary Representation

Toro already represents monetary values using signed `big.Int` quantities at a fixed precision of:

[
0.0001\text{ MAD}
]

and refuses to use floating point for reconciliation equality.

That must remain true.

The optimizer must never receive floating-point accounting amounts.

For example:

[
18,750.2500\text{ MAD}
]

becomes:

[
187,502,500
]

solver units.

The transformation is:

[
M_{\text{solver}}
=================

M_{\text{MAD}}\times10,000.
]

If the selected optimization library only supports 64-bit integer coefficients, the adapter must first prove that the exact `big.Int` amount fits into the supported range.

The production accounting representation remains `big.Int`.

The solver's integer representation is a temporary computational projection.

An overflow must result in an explicit optimizer error.

It must never result in truncation or floating-point conversion.

---

# 9. Monetary Conservation

Suppose the LLM proposes:

```text
Bank B10
+32,000 MAD

matches

Invoice A8
18,000 MAD

Invoice A9
14,000 MAD
```

The optimizer can accept this relationship because:

[
18,000+14,000=32,000.
]

But:

```text
18,000 + 13,500
```

does not explain a:

```text
32,000
```

payment.

This is not a semantic judgment.

It is an accounting constraint.

The optimizer should reject that hypothesis.

For a reconciliation group (h):

[
\sum_{b\in B_h}a_{hb}
=====================

\sum_{j\in J_h}a_{hj}
]

where (a) represents the exact amount allocated from each canonical member to the proposed group.

This allows the same mathematical structure to support one-to-one, one-to-many and many-to-one reconciliation.

---

# 10. Capacity Constraints

Accounting objects have finite remaining balances.

Suppose an invoice originally contained:

[
20,000
]

MAD but already received:

[
12,000
]

MAD of valid settlement.

Only:

[
8,000
]

MAD remains available.

The optimizer must enforce:

[
\sum_h a_{hj}x_h
\leq
\operatorname{remaining}(j)
]

for each book-side item (j).

Similarly, a bank movement cannot be consumed beyond its available amount.

This prevents the same accounting value from being silently reused in different reconciliation hypotheses.

---

# 11. Currency Constraints

Candidate relationships must use compatible currencies.

For ordinary V1 reconciliation:

[
currency(B)=currency(J)
]

must hold.

If future Toro versions model foreign-exchange reconciliation, that should be represented explicitly through canonical FX accounting objects.

The optimizer should never implicitly perform currency conversion merely because amounts happen to look similar.

---

# 12. Direction Constraints

The optimizer should validate that the proposed economic direction is structurally compatible.

An incoming customer settlement cannot ordinarily be matched against an unrelated outgoing supplier payment.

This should not require the optimizer to understand accounting semantics.

The LLM or preprocessing layer should provide normalized roles such as:

```text
BANK_INFLOW
BANK_OUTFLOW
BOOK_BANK_DEBIT
BOOK_BANK_CREDIT
CUSTOMER_RECEIVABLE_SETTLEMENT
SUPPLIER_PAYABLE_SETTLEMENT
```

The optimizer consumes a deterministic compatibility matrix provided by configuration.

This matrix should describe mechanical ledger relationships, not changing accounting policy.

---

# 13. No Double Consumption

One of the most important global constraints is that canonical accounting value cannot be used twice.

Suppose:

```text
Invoice A91 = 18,750 MAD
```

and the LLM proposes:

```text
H1: B17 -> A91
H2: B23 -> A91
```

If both hypotheses require the entire open amount of A91:

[
x_{H1}+x_{H2}\leq1.
]

The optimizer should immediately detect the conflict.

The tool response should not merely say:

```text
INFEASIBLE
```

It should identify that both hypotheses compete for the same limited accounting resource.

That gives the LLM something meaningful to reason about.

---

# 14. Opening-State Items

The opening state must participate in reconciliation as ordinary canonical evidence.

This is critical.

Suppose June closed with:

```text
Outstanding cheque CHQ-008741
6,250 MAD
```

and July contains:

```text
B29
-6,250 MAD
CHQ 008741
```

The LLM can propose:

```text
B29 -> opening_item_CHQ_008741
```

The optimizer treats that opening-state object as available reconciliation capacity.

Once the item has been consumed completely by an accepted reconciliation relationship, no other July movement can consume it.

This gives historical continuity without forcing the LLM to search arbitrary previous accounting periods.

The existing opening-state model intentionally carries forward unresolved canonical records rather than loading the whole accounting history.

---

# 15. The Objective Function

Not every feasible configuration is equally desirable.

The LLM should therefore be allowed to express relative preference among hypotheses.

However, V1 should **not pretend these preferences are calibrated probabilities**.

If the LLM says:

```text
confidence: 0.97
```

we should not automatically interpret that number as:

[
P(match)=0.97.
]

LLM confidence scores are not necessarily statistically calibrated.

Instead, V1 should treat preference as an optimization ranking signal.

Each hypothesis can receive an integer preference weight:

[
w_h.
]

The optimizer maximizes:

[
\max\sum_h w_hx_h.
]

Equivalent minimization formulations are also acceptable.

The important point is that the optimizer chooses the globally compatible collection of highly preferred hypotheses.

A later version may replace these engineering weights with calibrated probabilities and negative-log likelihoods.

V1 should not require that research result.

---

# 16. Unresolved Is a Valid Solution

The optimizer must never be constructed so that every bank movement is forced into a match.

That would create dangerous false reconciliations.

Every movement must have a legitimate unresolved state.

Let:

[
u_b\in{0,1}
]

represent whether bank movement (b) remains unresolved.

Then the optimizer may enforce:

[
\sum_{h:b\in h}x_h+u_b=1.
]

This means each bank movement is either explained by one selected reconciliation structure or explicitly unresolved.

The objective may apply a moderate unresolved penalty:

[
-\lambda\sum_bu_b
]

to encourage the optimizer to reconcile when strong feasible hypotheses exist.

But (\lambda) must never be so strong that the solver prefers a weak or contradictory match over HOLD.

In accounting:

> **Unresolved is better than falsely reconciled.**

That philosophy must be encoded structurally.

---

# 17. Suggested Objective

A useful initial objective is:

[
\max
\left(
\sum_h w_hx_h
-------------

## \lambda_u\sum_bu_b

\lambda_c C
\right)
]

where (w_h) represents LLM preference, (u_b) represents unresolved movements, and (C) represents optional complexity penalties.

Complexity penalties can discourage unnecessarily elaborate grouped matches when a simpler equally supported configuration exists.

For example, all else equal:

```text
B1 -> A1
```

may be preferred over:

```text
B1 -> A2 + A3 + A4 + A5 + A6.
```

This should remain a weak preference.

Accounting evidence must dominate aesthetic simplicity.

---

# 18. Why CP-SAT Is a Strong V1 Candidate

The first optimizer implementation should favor a general integer constraint solver rather than a narrowly specialized algorithm.

The reconciliation problem contains Boolean decisions, exact integer monetary quantities, mutually exclusive relationships, capacities and grouped selections.

That is naturally expressible as constraint programming / integer optimization.

Google OR-Tools contains a CP-SAT solver specifically designed around Boolean and integer constraints, and its repository includes a Go `cpmodel` package for constructing CP-SAT models.

Therefore CP-SAT is a strong research candidate.

However, the optimizer tool contract must remain solver-independent.

Toro should be able to replace CP-SAT with another solver later without changing the LLM interaction model.

The abstraction is the asset.

The solver is an implementation detail.

---

# 19. Optimizer Tool Request Contract

The LLM should call the optimizer using structured arguments.

A conceptual request should resemble:

```json
{
  "problem_id": "atlas_station_2026_07",
  "reconciliation_state_id": "state_july_open_r2",
  "mode": "PARTIAL",
  "bank_items": [
    {
      "id": "B17",
      "amount_4dp": "-187500000",
      "currency": "MAD",
      "direction": "OUTFLOW"
    }
  ],
  "book_items": [
    {
      "id": "A71",
      "remaining_amount_4dp": "187500000",
      "currency": "MAD",
      "role": "SUPPLIER_PAYABLE"
    }
  ],
  "hypotheses": [
    {
      "id": "H1",
      "preference": 950,
      "bank_allocations": [
        {
          "id": "B17",
          "amount_4dp": "187500000"
        }
      ],
      "book_allocations": [
        {
          "id": "A71",
          "amount_4dp": "187500000"
        }
      ],
      "evidence_refs": [
        "invoice_reference_match",
        "counterparty_match"
      ]
    }
  ]
}
```

This JSON is an ephemeral solver request.

It does not replace canonical records.

The IDs must correspond to canonical records or explicitly materialized eval fixtures.

The optimizer should never silently modify the amounts proposed by the LLM.

If the proposed allocation is mathematically impossible, the correct response is to reject it or select another supplied hypothesis.

---

# 20. Optimizer Modes

The tool should support at least two modes.

`PARTIAL` mode is used while the LLM is reasoning.

Unresolved transactions are allowed.

The tool determines the globally best feasible subset of hypotheses and returns residual problems.

`CLOSE` mode represents a proposed final reconciliation configuration.

In this mode the optimizer also verifies that the requested accounting closure constraints can be satisfied.

Even when `CLOSE` succeeds, the result must still pass Toro's existing deterministic closing service.

The optimizer is advisory.

The reconciliation state service remains authoritative.

---

# 21. Optimizer Response Contract

The optimizer should return structured information specifically designed to help the LLM continue reasoning.

A successful response should resemble:

```json
{
  "status": "FEASIBLE",
  "selected_hypotheses": [
    "H1",
    "H8",
    "H11"
  ],
  "rejected_hypotheses": [
    {
      "id": "H2",
      "reason": "LOWER_OBJECTIVE_GLOBAL_CONFIGURATION"
    }
  ],
  "unresolved_bank_items": [
    "B22"
  ],
  "unresolved_book_items": [
    "A93"
  ],
  "objective_value": 2680,
  "projected_closing_difference_4dp": "0",
  "diagnostics": []
}
```

An infeasible request might return:

```json
{
  "status": "INFEASIBLE",
  "selected_hypotheses": [],
  "conflicts": [
    {
      "type": "BOOK_CAPACITY_EXCEEDED",
      "canonical_id": "A91",
      "available_amount_4dp": "187500000",
      "claimed_amount_4dp": "375000000",
      "hypotheses": [
        "H17",
        "H23"
      ]
    }
  ]
}
```

The response should be designed for machine reasoning.

It should not contain long natural-language explanations.

The LLM can interpret the structured diagnostics itself.

---

# 22. Feasible Alternatives

One of the most useful optimizer capabilities is returning an alternative configuration.

Suppose the LLM proposes:

```text
B1 -> A1
B2 -> A1
```

which is impossible.

But the candidate set also contains:

```text
B1 -> A2
B2 -> A1
```

The optimizer should be able to return:

```json
{
  "status": "FEASIBLE_ALTERNATIVE",
  "selected_hypotheses": [
    "B1_A2",
    "B2_A1"
  ],
  "displaced_hypotheses": [
    "B1_A1"
  ]
}
```

The LLM can then reason:

> My first B1 interpretation was locally plausible, but using A1 there prevents a much stronger B2 relationship. The globally coherent configuration uses A2 for B1.

This is exactly the behavior V1 is trying to obtain.

---

# 23. The LLM Reasoning Loop

The LLM should be allowed to call the optimizer more than once.

A typical reconciliation episode may be:

```text
LLM examines evidence

LLM generates candidate hypotheses

LLM calls optimizer

optimizer reports conflict

LLM examines conflict

LLM generates alternative hypothesis

LLM calls optimizer again

optimizer returns feasible configuration

LLM determines whether remaining unresolved
items require more evidence

LLM either retrieves evidence or places HOLD

LLM emits final RFC 6902 patch
```

This turns the optimizer into a reasoning instrument.

The LLM does not merely receive mathematical judgment after completing its reasoning.

The mathematical system participates in reasoning itself.

---

# 24. RFC 6902 Remains the LLM Output Gate

Toro already uses RFC 6902 JSON Patch as the structural contract for LLM output.

That should remain unchanged.

The final LLM output for the reconciliation node must therefore be a valid JSON Patch.

For example:

```json
[
  {
    "op": "add",
    "path": "/proposed_matches/-",
    "value": {
      "bank_line_ids": ["B17"],
      "book_line_ids": ["A71"],
      "optimizer_problem_id": "OPT-883",
      "optimizer_solution_id": "SOL-12"
    }
  },
  {
    "op": "add",
    "path": "/holds/-",
    "value": {
      "bank_line_id": "B22",
      "reason": "INSUFFICIENT_EVIDENCE"
    }
  }
]
```

If the response is not valid RFC 6902 JSON Patch, it does not proceed.

The LLM is asked again.

Passing the JSON Patch gate does not imply accounting correctness.

It only means the response is structurally valid.

---

# 25. Deterministic Final Validation

After the LLM finishes reasoning, Toro must independently validate its proposed patch.

The final validator should not trust the optimizer invocation merely because the LLM claims to have called it.

The validator should retrieve or reproduce the relevant optimizer solution and independently verify the critical accounting invariants.

This includes exact amounts, member existence, currency compatibility, available capacity, duplicate consumption, match-group totals and closing-balance arithmetic.

The existing reconciliation system already requires exact four-decimal equality before a state may close.

Nothing in this PRD weakens that requirement.

---

# 26. Immutable State Creation

Only after deterministic validation succeeds may Toro create canonical reconciliation matches and a new reconciliation-state snapshot.

The probabilistic or semantic reasoning trace is evidence about how Toro reached the answer.

It is not the accounting state itself.

The accepted state remains immutable.

If a closed reconciliation later proves incorrect, the existing supersession model applies. Toro creates a new chain rather than modifying historical closed state.

---

# 27. V1 Evaluation Philosophy

V1 should not attempt to prove this architecture theoretically.

It should be evaluated on concrete accounting worlds.

Instead of testing ten different mathematical architectures, Toro should hold the architecture constant and vary the companies.

The research question becomes:

> **Can the same LLM + global optimizer + deterministic validation architecture reconcile materially different businesses without company-specific reconciliation code?**

That is a much more valuable test.

If the answer is yes, we have evidence that the architecture generalizes.

---

# 28. Eval Company One: Atlas Station SARL

The first company should be a Moroccan petrol station.

This case stresses high-volume repetitive financial activity.

Atlas Station should have card/TPE sales, cash deposits, fuel-supplier payments, maintenance suppliers, bank fees and opening-state items.

The eval should deliberately include a settlement such as:

```text
TPE batch:
32,000 MAD
```

that corresponds to several underlying book items.

It should also include an outstanding June cheque that clears in July.

There should be at least one ambiguous transaction whose description is poor enough that the LLM must reason semantically.

There should also be one genuinely unresolved transaction.

The correct result must leave that transaction on HOLD rather than inventing a match.

This company tests:

[
\text{high volume}
]

[
\text{batch settlement}
]

[
\text{opening-state continuity}
]

and:

[
\text{repetitive transaction structure}.
]

---

# 29. Eval Company Two: Maghreb Distribution SARL

The second company should be a wholesale distributor.

Its accounting pattern should be materially different.

Customers frequently pay several invoices with one bank transfer.

Suppliers may receive settlements covering multiple purchase invoices.

Partial payments should exist.

Two unrelated customers should occasionally have transactions for exactly the same amount.

A supplier credit note or refund should appear.

One transaction should create a situation where a locally attractive match causes a global conflict.

For example:

```text
B1 can plausibly use A1 or A2

B2 can only use A1
```

The LLM should initially have enough ambiguity that the optimizer's global view becomes useful.

The correct global solution is:

```text
B1 -> A2
B2 -> A1
```

This company is the most important test of whether the optimizer provides real incremental capability beyond LLM reasoning.

It tests:

[
\text{one-to-many}
]

[
\text{many-to-one}
]

[
\text{partial settlement}
]

[
\text{duplicate amounts}
]

and:

[
\text{global assignment conflicts}.
]

---

# 30. Eval Company Three: Atlas Construction Services SARL

The third company should have longer-lived and less regular payment behavior.

A construction-oriented company is useful because transactions may involve supplier cheques, client advances, partial settlements, subcontractors and items that remain outstanding for several periods.

Its opening state should contain several old unresolved objects.

One should clear during the current period.

Another should legitimately remain outstanding.

The bank statement should contain semantically messy descriptions.

There should be at least one transaction where a current-period invoice appears to match by amount but an opening-state cheque is actually the correct explanation.

This tests whether the LLM correctly reasons across the opening state rather than assuming every current bank movement belongs to the current month's invoices.

The company therefore stresses:

[
\text{cross-period reconciliation}
]

[
\text{long-lived outstanding items}
]

[
\text{cheques}
]

[
\text{semantic ambiguity}
]

and:

[
\text{historical accounting context}.
]

---

# 31. Eval Fixture Structure

Each eval should contain two separate objects.

The first is the **input world**.

The second is the **ground truth**.

The LLM and optimizer must never receive ground truth.

A repository might use:

```text
evals/
  reconciliation/
    atlas_station/
      july_2026/
        input.json
        ground_truth.json

    maghreb_distribution/
      july_2026/
        input.json
        ground_truth.json

    atlas_construction/
      july_2026/
        input.json
        ground_truth.json
```

`input.json` contains only information that Toro would legitimately possess at reconciliation time.

`ground_truth.json` contains the accountant-approved answer.

This separation is essential.

Otherwise the eval becomes contaminated.

---

# 32. Ground Truth

Ground truth should identify every canonical movement's actual reconciliation disposition.

For each bank movement it should say whether the movement belongs to a match group or should remain unresolved.

For matched movements it should specify the exact canonical objects and allocations.

For outstanding book movements it should specify whether they remain carry-forward items.

The ground truth must also contain the correct closing state.

This means the evaluator can test more than individual matches.

It can determine whether the entire reconstructed accounting world is correct.

---

# 33. Relationship-Level Evaluation

At the smallest level, the evaluator compares predicted relationships with ground truth.

A correct relationship is:

```text
predicted match = ground-truth match
```

A false reconciliation occurs when Toro selects a relationship that ground truth says is incorrect.

A missed reconciliation occurs when the correct match existed but Toro left the item unresolved.

A correct HOLD occurs when ground truth says the evidence is insufficient and Toro also refuses to reconcile.

These outcomes should not receive equal penalties.

A false reconciliation is the most dangerous error.

---

# 34. Global-State Evaluation

The most important eval is not individual match accuracy.

It is whether Toro reconstructed the correct global reconciliation state.

For each company, evaluate:

[
\text{Selected Match Groups}
]

[
\text{Unresolved Bank Items}
]

[
\text{Outstanding Book Items}
]

[
\text{Carried Forward Items}
]

[
\text{Closing Bank Position}
]

[
\text{Closing Book Position}
]

and:

[
\text{Closing Difference}.
]

The ideal result is exact equality with ground truth.

---

# 35. Primary Metrics

The primary safety metric should be:

[
\text{False Reconciliation Rate}
================================

\frac{\text{incorrect accepted reconciliations}}
{\text{accepted reconciliations}}.
]

For the initial research gate, the target should be:

[
0
]

false reconciliations across the three curated companies.

The second metric is reconciliation coverage:

[
Coverage
========

\frac{\text{correct autonomous reconciliations}}
{\text{reconcilable ground-truth items}}.
]

The third is correct HOLD behavior.

Toro should receive credit for recognizing that evidence is insufficient.

The fourth is global-state correctness.

The final closing state must exactly match expected state where the scenario is intended to close.

---

# 36. Measuring the Optimizer's Contribution

Although the main architecture under evaluation is LLM + optimizer, the eval harness should support one useful ablation:

```text
LLM + deterministic validation
```

versus:

```text
LLM + optimizer tool + deterministic validation
```

This does not require building a second reconciliation system.

It simply disables the tool for one run.

This comparison answers an extremely valuable question:

> **Did the optimizer actually solve problems the LLM could not solve reliably itself?**

For Maghreb Distribution, the deliberately constructed global conflict should be particularly informative.

If the LLM consistently solves it without the optimizer, then the optimizer's incremental value may be smaller than expected.

If the optimizer prevents local but globally inconsistent decisions, its value has been empirically demonstrated.

---

# 37. Repeated Runs

LLM behavior may vary between executions.

A single successful run therefore should not be considered sufficient.

Each company scenario should be executed repeatedly with the same underlying accounting data.

For the research stage, five to ten runs per scenario is enough to expose obvious instability without creating a huge evaluation burden.

The important measurement is whether a previously correct reconciliation occasionally becomes an incorrect reconciliation.

A system that succeeds nine times and invents a false match on the tenth run is materially different from a system that succeeds nine times and HOLDs on the tenth.

The latter may be acceptable.

The former requires further work.

---

# 38. Eval Run Record

Every eval run should preserve:

```text
case ID
model identifier
model configuration
prompt version
tool version
optimizer version
input hash
LLM tool calls
optimizer requests
optimizer responses
final RFC 6902 patch
deterministic validation result
predicted reconciliation state
ground-truth comparison
latency
token usage
optimizer solve time
```

The purpose is reproducibility.

If changing a prompt suddenly improves Atlas Station but breaks Maghreb Distribution, Toro must be able to identify exactly what changed.

---

# 39. The First Research Gate

Passing three company scenarios is **not proof that Toro has solved reconciliation generally**.

But it is a meaningful first gate.

If the architecture can correctly process:

```text
high-volume TPE / retail reconciliation

complex invoice allocation / distribution reconciliation

cross-period cheque / construction reconciliation
```

without company-specific reconciliation logic, that is strong enough evidence to justify expanding the dataset.

The sequence should therefore be:

[
3\ companies
\rightarrow
30\ scenarios
\rightarrow
real\ anonymized\ accountant\ cases
\rightarrow
production\ shadow\ mode.
]

There is no reason to build the later stages before the first gate succeeds.

---

# 40. What V1 Explicitly Does Not Build

V1 does not need Bayesian inference.

It does not need factor graphs.

It does not need Monte Carlo simulation.

It does not need Graph Fourier Transform.

It does not need learned probability calibration.

It does not need a custom mathematical solver.

It does not need to eliminate the LLM from any semantic accounting judgment.

Those remain research options.

They should only enter the system when an eval exposes a specific failure mode that they are well suited to solve.

For example, if the LLM + optimizer architecture produces correct relationships but poor uncertainty calibration, Bayesian calibration becomes interesting.

If a future reconciliation graph becomes computationally difficult, specialized min-cost flow or decomposition methods become interesting.

If uncertainty across large accounting graphs displays systemic patterns that individual reasoning misses, Graph Fourier analysis becomes interesting.

The architecture should grow in response to observed holes.

---

# 41. Success Condition for This Experiment

The hypothesis being tested is:

[
\boxed{
\text{LLM reasoning}
+
\text{global constraint optimizer tool}
+
\text{deterministic accounting validation}
}
]

is sufficient to perform high-quality V1 bank reconciliation across materially different businesses.

The LLM should be able to understand messy accounting evidence and generate candidate explanations.

The optimizer should prevent locally reasonable explanations from creating globally impossible accounting worlds.

The LLM should be capable of using optimizer diagnostics to revise its reasoning.

The RFC 6902 gate should ensure that only structurally valid state proposals leave the model.

The deterministic validator should ensure that no accounting relationship becomes accepted merely because the LLM or optimizer preferred it.

The immutable reconciliation service should remain the final authority over what becomes accounting history.

---

# 42. Final Architecture

The V1 reconciliation architecture should therefore be understood as:

```text
                  OPENING STATE
                       +
                 BANK STATEMENT
                       +
                  SHADOW ERP
                       +
             ACCOUNTING EVIDENCE
                       |
                       v
                CONTEXT BUILDER
                       |
                       v
                      LLM
                       |
             accounting reasoning
             semantic interpretation
             candidate hypotheses
                       |
                       v
       GLOBAL RECONCILIATION OPTIMIZER
                       |
              exact integer math
              global constraints
              capacity validation
              conflict detection
              alternative solution
                       |
                       v
                      LLM
                       |
              accept / reconsider
              retrieve evidence
              generate alternatives
              HOLD if necessary
                       |
                       v
              RFC 6902 JSON PATCH
                       |
                       v
          DETERMINISTIC ACCOUNTING GATE
                       |
                       v
             RECONCILIATION MATCHES
                       |
                       v
              IMMUTABLE CLOSING STATE
                       |
                       v
            NEXT PERIOD OPENING STATE
```

The core philosophy is:

> **Use the LLM wherever the problem requires flexible reasoning. Use mathematics where exactness, global consistency and combinatorial search provide capabilities the LLM cannot guarantee. Never use mathematical complexity merely to replace reasoning that the LLM already performs reliably. Never allow LLM reasoning to replace accounting invariants that can be verified exactly.**

The eval system exists to determine whether that division of labor is enough.

If the three-company experiment succeeds consistently, Toro should expand the dataset rather than expand the mathematics.

If it fails, the failures themselves become the research agenda.

That is when additional mathematical tools should be introduced, one demonstrated deficiency at a time.
