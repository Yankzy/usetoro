# Product Requirements Document: Probabilistic Constraint Reconciliation Engine

## Status

Proposed research and implementation direction for the reconciliation stage of Toro's bookkeeping system.

## 1. Purpose

Toro's bookkeeping architecture is divided into four distinct stages:

[
\text{Opening State}
\rightarrow
\text{Transaction Understanding}
\rightarrow
\text{Reconciliation}
\rightarrow
\text{Closing State}
]

This separation already exists as an architectural principle in the reconciliation-state foundation. Transaction understanding is responsible for determining what a bank movement probably means economically and accounting-wise. Reconciliation is deliberately separate: it connects bank reality to book reality and determines whether the relationship between them is sufficiently evidenced to become part of an accepted accounting state. 

This PRD concerns only the **third stage: reconciliation**.

The purpose of the proposed system is to replace a primarily procedural reconciliation engine with a mathematical inference system capable of reasoning over many competing explanations simultaneously.

The central idea is that reconciliation should not be modeled as an ever-growing collection of rules such as:

```text
if amount matches
then inspect date
then inspect supplier
then inspect invoice
then inspect previous month
then inspect cheque number
...
```

That approach eventually becomes brittle because real accounting relationships are interconnected. A bank movement may correspond to one invoice, several invoices, an old cheque, a TPE settlement, an internal transfer, a supplier refund, a previously unidentified transaction, or no currently available accounting object at all.

Instead, Toro should model reconciliation as a **constrained probabilistic graph**.

The graph represents possible accounting worlds. Mathematics determines which worlds are possible, which are plausible, which combination of relationships best explains the evidence, and how uncertain the remaining answer is.

LLMs remain part of this architecture. They are not replaced by mathematics. They provide semantic reasoning where deterministic systems are weak, while mathematical systems prevent semantic reasoning from becoming accounting truth merely because an LLM sounded confident.

The objective is therefore not to build a "mathematical accountant."

The objective is to build a reconciliation environment in which:

[
\text{semantic reasoning}
+
\text{probability}
+
\text{accounting constraints}
+
\text{optimization}
+
\text{uncertainty measurement}
]

cooperate to determine whether a proposed reconciliation has earned the right to become accepted state.

---

# 2. Relationship to the Existing Reconciliation-State Foundation

The probabilistic reconciliation engine must sit **above**, not replace, the immutable reconciliation-state foundation.

Toro already treats Shadow ERP journal lines, bank statement lines, matches and reconciliation-state memberships as canonical records rather than allowing reconciliation state to duplicate transaction facts. 

That remains unchanged.

Similarly, the deterministic accounting invariants already established remain authoritative. A state cannot close merely because the probabilistic engine believes the relationships are likely. The closing balance must reconcile exactly using Toro's fixed four-decimal monetary representation. 

The architecture therefore separates **inference** from **truth**.

The probabilistic engine answers:

> What most likely explains the accounting evidence currently available?

The immutable reconciliation-state service answers:

> Has this explanation satisfied the deterministic requirements necessary to become accepted reconciliation state?

This separation is critical.

A probability of 99.99% cannot make:

[
18,750 \neq 18,749.99
]

become true.

Similarly, no LLM reasoning can make debit and credit arithmetic balance when they mathematically do not.

Probability ranks possible accounting explanations. Accounting invariants define what is permissible.

---

# 3. The Fundamental Representation: A Reconciliation Graph

The reconciliation engine should represent the current reconciliation problem as a graph.

Suppose the bank statement contains:

```text
B1: -18,750 MAD
B2: -6,250 MAD
B3: +32,000 MAD
```

The accounting environment contains:

```text
A1: Supplier invoice, 18,750 MAD
A2: Supplier invoice, 18,750 MAD
A3: Outstanding cheque, 6,250 MAD
A4: Customer invoice, 18,000 MAD
A5: Customer invoice, 14,000 MAD
A6: Customer invoice, 32,000 MAD
```

Toro should not immediately choose relationships.

Instead, it constructs hypotheses:

```text
B1 may correspond to A1
B1 may correspond to A2

B2 may clear A3

B3 may settle A4 + A5
B3 may settle A6
```

These relationships are edges in the reconciliation graph.

An edge does **not** mean:

> these records are reconciled.

It means:

> this relationship is currently considered a possible explanation.

The graph can contain many alternatives simultaneously.

This matters because accounting reasoning is often contextual. A relationship that looks reasonable in isolation can become impossible after another transaction consumes the same invoice or after a global accounting constraint is considered.

The engine therefore reasons over **configurations of relationships**, not merely individual pairwise matches.

---

# 4. Hard Constraints

The first mathematical layer should not be probabilistic at all.

Certain accounting relationships are impossible.

These should be represented as hard constraints.

If a candidate relationship violates one of these constraints, Toro should remove that world from consideration rather than merely lower its probability.

For example, if a MAD bank movement is being reconciled directly against a EUR book movement without an appropriate foreign-exchange mechanism, the relationship may be structurally invalid.

Likewise, if an invoice has only 10,000 MAD of outstanding balance remaining, a proposed reconciliation cannot consume 14,000 MAD of it.

A canonical amount should not normally be consumed twice by independent reconciliations.

A bank movement and the accounting relationships explaining it should preserve monetary conservation.

A grouped match claiming that:

[
10,000 + 5,000 = 14,500
]

is not "unlikely."

It is impossible.

Mathematically, we can represent such relationships using a constraint function:

[
\phi_{\text{hard}}(X)
=====================

\begin{cases}
1 & \text{if configuration } X \text{ is valid}\
0 & \text{if configuration } X \text{ violates an invariant}
\end{cases}
]

When converted to optimization cost, an impossible configuration can be interpreted as having:

[
C(X)=+\infty
]

This distinction between hard and soft evidence is important.

"Invoice reference does not match" is weak evidence against a relationship.

"Amounts cannot possibly balance" is not weak evidence. It is a mathematical contradiction.

The reconciliation engine should never treat these two things equivalently.

---

# 5. Minimum-Cost Flow

One important class of reconciliation problems naturally resembles a network-flow problem.

In minimum-cost flow, we have a directed graph:

[
G=(V,E)
]

where (V) is the set of nodes and (E) is the set of possible edges.

Each edge (e) has a capacity:

[
u_e
]

a cost:

[
c_e
]

and an amount of actual flow:

[
x_e.
]

The optimization objective is:

[
\min \sum_{e\in E}c_ex_e
]

subject to capacity constraints:

[
0 \leq x_e \leq u_e
]

and conservation constraints at appropriate nodes.

Minimum-cost flow is formally the problem of finding the least-cost way of moving quantities through a network while respecting supplies, demands and capacities. ([Google Developers][1])

For accounting, the useful conceptual leap is that **flow can represent monetary value being explained**.

Suppose a bank credit is:

[
15,000\text{ MAD}
]

and the books contain two open customer invoices:

[
10,000\text{ MAD}
]

and:

[
5,000\text{ MAD}.
]

Toro may construct:

[
x_{B,A_1}=10,000
]

and:

[
x_{B,A_2}=5,000.
]

The conservation condition becomes:

[
x_{B,A_1}+x_{B,A_2}=15,000.
]

The entire observed bank movement has now been accounted for.

Capacity is also natural. If invoice (A_1) has only 10,000 MAD outstanding:

[
x_{B,A_1}\leq10,000.
]

This prevents the solver from "using" more of an accounting object than actually exists.

Assignment problems can themselves be represented as minimum-cost-flow problems because capacities and flow conservation prevent incompatible simultaneous assignments. ([Google Developers][2])

This makes minimum-cost flow very attractive for deterministic one-to-one and grouped reconciliation problems.

However, minimum-cost flow should **not** become Toro's universal reconciliation algorithm.

Its weakness is that it assumes that a useful graph and useful edge costs already exist.

The difficult accounting question often comes before that:

> How plausible is this edge in the first place?

That leads to probability.

---

# 6. Bayesian Reasoning

Bayesian reasoning gives Toro a disciplined way of updating beliefs when evidence arrives.

Suppose (M) represents the hypothesis:

> Bank movement B1 settles invoice A1.

And (E) represents the evidence available to Toro.

Bayes' theorem gives:

[
P(M\mid E)
==========

\frac{P(E\mid M)P(M)}
{P(E)}.
]

The quantity we care about is:

[
P(M\mid E),
]

the posterior probability of the relationship after observing the evidence.

The evidence might include an exact amount match, dates, invoice reference, counterparty name, payment method, whether the invoice is still open, historical payment timing, the previous reconciliation state, and semantic interpretations produced by an LLM.

For example:

```text
Bank:
-18,750 MAD
"VIR ABC IND FACT 445"

Invoice:
18,750 MAD
ABC Industrie SARL
Invoice 445
```

Evidence such as the exact amount and open invoice balance can come directly from deterministic code.

The interpretation:

```text
"ABC IND" probably refers to "ABC Industrie SARL"
```

may come from an LLM.

The interpretation:

```text
"FACT 445" appears to refer to invoice 445
```

may also come from the LLM.

These pieces of evidence change the probability of the match.

This provides a more principled foundation than manually declaring:

```text
counterparty match = +20 points
reference match = +40 points
```

But Bayes' theorem by itself is **not the complete solution**.

The mathematical issue is not that Bayesian probability can only contain a few variables. A joint distribution can formally contain an arbitrary finite set:

[
P(X_1,X_2,\ldots,X_n\mid E).
]

The practical problem is combinatorial explosion.

If Toro has 100 binary uncertainties, the naive joint state space already contains:

[
2^{100}
]

possible configurations.

Exact inference quickly becomes computationally infeasible.

Therefore the architecture should use Bayesian reasoning as a probabilistic foundation without assuming that Toro will calculate enormous joint distributions directly.

---

# 7. Negative Log Probability as Cost

Probability connects elegantly with optimization.

Suppose a candidate relationship has probability:

[
P(M\mid E)=0.99.
]

Toro can convert this into an optimization cost:

[
C(M)=-\log P(M\mid E).
]

Then:

[
-\log(0.99)\approx0.010.
]

A relationship with probability:

[
0.50
]

has:

[
-\log(0.50)\approx0.693.
]

A relationship with probability:

[
0.01
]

has:

[
-\log(0.01)\approx4.605.
]

High-probability relationships therefore become cheap.

Low-probability relationships become expensive.

This transformation is valuable because maximizing joint probability can often be transformed into minimizing sums of negative log probabilities.

Instead of inventing arbitrary reconciliation costs, Toro can increasingly derive costs from calibrated probabilistic evidence.

This does not mean the first implementation must already possess perfectly learned probabilities. Early models may still require manually initialized priors and empirically fitted likelihoods.

The important architectural decision is that costs should have a path toward becoming statistically meaningful rather than remaining arbitrary constants forever.

---

# 8. Why a Simple Bayesian Network Is Not Enough

A classical Bayesian network is a directed acyclic graph.

That creates difficulties for the kind of reconciliation environment Toro is trying to represent.

Accounting evidence can contain relationships that are mutually reinforcing, and the useful computational graph may contain loops.

For example, the interpretation of a bank movement may affect the probability of a supplier relationship. That supplier relationship may affect which historical payment pattern is relevant. That payment pattern may strengthen or weaken the original interpretation.

A simple directed Bayesian network is not always the natural representation for such relationships.

Toro should therefore investigate **factor graphs** as the more general probabilistic representation.

A factor graph represents a global function as a product of smaller local factors. Factor graphs are widely used to represent structured probabilistic models and can express factorizations related to both Bayesian networks and undirected graphical models. ([Stanford University][3])

---

# 9. Factor Graphs

Let:

[
X=(X_1,X_2,\ldots,X_n)
]

represent the unknown reconciliation variables.

For example:

[
X_1 = \text{which accounting object explains B1}
]

[
X_2 = \text{which accounting object explains B2}
]

and so forth.

Instead of attempting to define one enormous probability table, Toro defines local factors:

[
\phi_1,\phi_2,\ldots,\phi_k.
]

Then:

[
P(X\mid E)
==========

\frac{1}{Z}
\prod_{k=1}^{K}
\phi_k(X_k,E),
]

where (Z) is the normalization constant.

A factor is simply a function expressing how compatible some local configuration is with the evidence.

Toro might eventually have factors such as:

[
\phi_{\text{amount}}
]

[
\phi_{\text{date}}
]

[
\phi_{\text{counterparty}}
]

[
\phi_{\text{reference}}
]

[
\phi_{\text{opening-state}}
]

[
\phi_{\text{payment-method}}
]

[
\phi_{\text{document-chain}}
]

[
\phi_{\text{historical-pattern}}
]

[
\phi_{\text{LLM-semantics}}
]

[
\phi_{\text{flow-conservation}}
]

[
\phi_{\text{double-entry}}
]

and:

[
\phi_{\text{closing-balance}}.
]

The crucial advantage is modularity.

The entire accounting universe does not have to fit into one giant formula.

Each accounting relationship contributes evidence through its own factor.

The Stanford formulation of factor graphs similarly represents an assignment's weight as the product of the factors associated with that assignment. ([Stanford University][4])

This maps naturally onto ASE.

A node can introduce new evidence. That evidence modifies one or more factors. The inference system recomputes what those factors imply about the reconciliation hypotheses.

---

# 10. Hard Factors and Soft Factors

Toro should explicitly distinguish hard factors from soft factors.

A soft factor expresses plausibility.

For example:

[
\phi_{\text{date}}=0.95
]

could mean that a three-day delay is highly compatible with the expected settlement timing.

A 45-day delay might produce:

[
\phi_{\text{date}}=0.30.
]

Neither is impossible.

A hard factor instead returns zero when an accounting law is violated.

For example:

[
\phi_{\text{currency}}(X)=0
]

for a relationship that requires impossible currency equivalence.

Likewise:

[
\phi_{\text{capacity}}(X)=0
]

if an invoice would be consumed beyond its open amount.

Once any multiplicative factor becomes zero:

[
P(X\mid E)=0.
]

That accounting world disappears.

This gives us an elegant hierarchy:

**Accounting laws eliminate worlds. Evidence ranks surviving worlds.**

That should be a core design principle of the reconciliation engine.

---

# 11. Maximum A Posteriori Inference

Toro usually does not need the complete probability of every possible world.

Often the immediate goal is to find:

> What is the most probable globally consistent reconciliation configuration?

This is known as maximum a posteriori inference, or MAP:

[
X^*
===

\arg\max_X P(X\mid E).
]

Because logarithms are monotonic:

[
X^*
===

\arg\min_X
-\log P(X\mid E).
]

If the probability factorizes:

[
P(X\mid E)
\propto
\prod_k\phi_k(X_k,E),
]

then:

[
-\log P(X\mid E)
================

-\sum_k\log\phi_k(X_k,E)+C.
]

This turns probabilistic inference into an optimization problem.

That is the deeper mathematical connection between Bayesian reasoning, factor graphs and minimum-cost optimization.

They are not necessarily separate inventions glued together.

Under the right formulation, they become different views of the same inference problem.

---

# 12. Exact Inference Versus Approximate Inference

Factor graphs do not magically remove computational complexity.

Exact inference can still become expensive because eliminating variables can create increasingly large intermediate factors. The computational cost depends heavily on the structure of the graph and on how interconnected its variables become. ([Stanford University][5])

Therefore Toro should not commit to one universal solver.

The engine should inspect the structure of a reconciliation subproblem and choose the simplest solver capable of solving it.

A clean one-to-one matching problem might be solved exactly.

A simple money-allocation problem might be solved using minimum-cost flow.

A tightly constrained grouped reconciliation problem may be better represented as an integer optimization problem.

A complex probabilistic graph may require approximate probabilistic inference.

This is a major architectural principle:

> **Representation should be shared; solver choice should be adaptive.**

The reconciliation engine should not distort an accounting problem merely because one favorite mathematical algorithm is available.

---

# 13. Monte Carlo Methods

When exact probabilistic inference becomes infeasible, Toro can approximate the distribution by sampling.

The general Monte Carlo idea is straightforward.

Instead of explicitly computing every possible reconciliation world:

[
X_1,X_2,\ldots,X_N,
]

Toro generates samples from plausible reconciliation configurations.

Suppose the engine produces 100,000 valid sampled worlds.

It discovers that:

```text
93,700 worlds match B1 to Invoice 445
4,800 worlds match B1 to Invoice 449
1,500 worlds leave B1 unresolved
```

Toro can estimate:

[
P(B1\rightarrow A445\mid E)\approx0.937.
]

This is particularly valuable when the graph contains interactions that make exact enumeration impossible.

Monte Carlo is therefore not an alternative to Bayesian reasoning.

It is one family of computational methods for approximating probability distributions that cannot practically be calculated exactly.

---

# 14. Markov Chain Monte Carlo

Markov Chain Monte Carlo, or MCMC, is one such family.

MCMC constructs a sequence of states whose long-run distribution approximates a target probability distribution.

For Toro, a state could represent one complete reconciliation world:

[
X^{(t)}.
]

The sampler proposes another configuration:

[
X^{(t+1)}
]

and decides whether the system should move to that configuration according to an acceptance rule designed to make the generated states represent the target posterior distribution.

Modern Bayesian systems use MCMC specifically to obtain samples from posterior distributions that are difficult to evaluate analytically. Stan, for example, describes its MCMC machinery as sampling from target densities that are typically Bayesian posteriors. ([Stan][6])

Toro should not immediately implement sophisticated MCMC merely because it exists.

MCMC introduces its own engineering problems: convergence, mixing, diagnostics, computational cost and reproducibility.

It should become an available tool when the reconciliation graph genuinely requires it.

---

# 15. Sequential Monte Carlo

Accounting also has a temporal property that makes another class of methods interesting: **Sequential Monte Carlo**, or SMC.

Reconciliation evidence arrives over time.

At one moment Toro may know:

```text
bank movement exists
amount known
description partially understood
```

Then an invoice arrives.

Later a payment reference is extracted.

Later the opening state exposes an old cheque.

Instead of repeatedly solving the entire probabilistic universe from scratch, a sequential inference system can maintain a population of hypotheses and update their weights as new evidence arrives.

Conceptually:

[
{X_1,w_1},{X_2,w_2},\ldots,{X_N,w_N}
]

represent competing reconciliation worlds and their weights.

When new evidence (E_t) arrives:

[
w_i'
\propto
w_iP(E_t\mid X_i).
]

Implausible worlds lose weight.

Plausible worlds gain relative weight.

When too much probability mass concentrates in very few particles, the system can resample.

This may eventually fit ASE particularly well because reconciliation is not necessarily a single batch computation. It is an evolving state receiving additional evidence.

However, like MCMC, SMC should be considered an advanced solver, not an immediate V1 dependency.

---

# 16. Shannon Entropy as the Common Uncertainty Interface

Different reconciliation subgraphs may use different mathematical solvers, but ASE needs one common language for deciding whether a node has enough information to proceed.

That language can remain Shannon entropy.

Given a discrete distribution:

[
P(X=x_i)=p_i,
]

entropy is:

[
H(X)
====

-\sum_i p_i\log p_i.
]

If one reconciliation hypothesis dominates:

```text
Invoice 445    0.990
Invoice 449    0.006
Unresolved     0.004
```

entropy is low.

The system has effectively collapsed toward one explanation.

If:

```text
Invoice 445    0.36
Invoice 449    0.34
Unresolved     0.30
```

entropy is high.

The system should not pretend certainty exists.

The elegance here is that ASE does not need to know how the probability distribution was produced.

It may have come from an exact solver, Bayesian inference, min-cost flow converted into probabilistic alternatives, Monte Carlo samples, or a hybrid method.

ASE receives:

[
P(X)
]

and computes uncertainty.

This means entropy becomes the interface between **reconciliation mathematics** and **ASE execution control**.

---

# 17. Entropy Alone Is Not Sufficient

Toro should not use entropy as the only collapse criterion.

Consider:

```text
A = 0.999
B = 0.001
```

Entropy is extremely low.

But if the model producing these probabilities is badly calibrated, this apparent certainty is dangerous.

Therefore collapse should require several independent conditions.

Conceptually:

[
\text{COLLAPSE}
===============

\text{low entropy}
\land
\text{hard constraints satisfied}
\land
\text{accounting invariants satisfied}
\land
\text{evidence provenance sufficient}
\land
\text{model calibration acceptable}.
]

Entropy answers:

> Is the model uncertain?

It does not answer:

> Is the model correct?

The architecture must preserve that distinction.

---

# 18. LLMs as Semantic Inference Instruments

LLMs remain central to this architecture because accounting evidence is not purely numerical.

Bank descriptions are messy.

Supplier names are abbreviated.

Invoice references may be embedded in free text.

Documents can imply relationships without stating them formally.

A bank description such as:

```text
VIR ETS EL MANSOURI FCT 0526 SOLDE
```

requires semantic interpretation.

A mathematical optimizer cannot understand the phrase by itself.

The LLM can.

But the LLM should not output:

```text
RECONCILED
```

It should produce **evidence or hypotheses**.

Toro already constrains LLM output through RFC 6902 JSON Patch. The reconciliation engine should preserve that principle.

The LLM might add:

```json
[
  {
    "op": "add",
    "path": "/evidence/semantic/counterparty",
    "value": {
      "candidate_id": "supplier_el_mansouri",
      "confidence": 0.96,
      "basis": "Bank label abbreviation appears consistent with canonical supplier name."
    }
  },
  {
    "op": "add",
    "path": "/evidence/semantic/reference",
    "value": {
      "candidate_reference": "0526",
      "interpretation": "likely invoice or period reference",
      "confidence": 0.84
    }
  }
]
```

The JSON Patch requirement acts as the first structural gate.

The patch must then pass schema validation.

It must reference objects that actually exist.

It must not mutate prohibited accounting truth.

Only after those checks does the evidence enter the probabilistic graph.

The LLM has therefore contributed reasoning without being granted authority over reconciliation state.

---

# 19. LLMs as Hypothesis Generators

An even more important role for the LLM is hypothesis generation.

Suppose deterministic candidate generation finds no obvious match for:

```text
32,000 MAD incoming transfer
```

The LLM sees the bank description, customer records, document metadata and surrounding transactions.

It might reason that the payment could represent two invoices rather than one.

It can propose:

[
B_7 \rightarrow A_{12}+A_{19}.
]

That proposal becomes a new graph structure.

Mathematics then determines whether the proposal survives.

If:

[
18,000+14,000=32,000
]

the amount constraint passes.

If both invoices belong to the counterparty implied by the bank description, a counterparty factor strengthens the proposal.

If both remain outstanding, capacity constraints pass.

If the transaction reference mentions both invoice numbers, the semantic factor becomes strong.

The LLM therefore expands the **hypothesis space**.

It does not decide which hypothesis becomes truth.

---

# 20. LLMs as Active Investigators

The most interesting integration with ASE occurs when uncertainty remains high.

Suppose the reconciliation engine produces:

[
P(A)=0.42
]

[
P(B)=0.39
]

[
P(C)=0.19.
]

Entropy is high.

Instead of immediately escalating to a human, ASE can ask:

> What additional evidence would most reduce uncertainty between these competing explanations?

The LLM may conclude that the invoice number would distinguish A from B.

Or that the cheque number is decisive.

Or that an unresolved item from the opening state should be retrieved.

ASE can then obtain that evidence and rerun inference.

Conceptually:

[
E_t
\rightarrow
P(X\mid E_t)
\rightarrow
H(X)
]

If entropy remains high, gather new evidence:

[
E_{t+1}=E_t+\Delta E.
]

Then:

[
P(X\mid E_{t+1})
]

is computed.

This creates a closed reasoning loop:

```text
hypothesize
measure uncertainty
identify missing information
gather evidence
update graph
recompute
```

The loop continues until the system collapses or reaches a configured information, cost or reasoning budget.

---

# 21. The Solver Router

The long-term reconciliation engine should include a solver router rather than one universal algorithm.

The solver router inspects the mathematical shape of a subproblem.

If it is a trivial exact one-to-one match with hard equality conditions, deterministic code should solve it directly.

If it is a classical assignment or divisible-flow problem, minimum-cost flow may solve it.

If it contains discrete grouped assignments and hard combinatorial constraints, integer programming may be appropriate.

If it is a sparse probabilistic factor graph where exact inference remains tractable, exact probabilistic inference may be used.

If exact inference becomes impractical, approximate techniques such as message passing, MCMC or SMC become candidates.

The principle is:

> **Use the cheapest trustworthy solver appropriate to the structure of the problem.**

Sophisticated mathematics should not be used where simple arithmetic is sufficient.

This prevents the system itself from becoming unnecessarily complex.

---

# 22. Proposed Reconciliation Runtime

The resulting runtime should conceptually behave as follows:

```text
CANONICAL BANK EVIDENCE
CANONICAL BOOK EVIDENCE
OPENING RECONCILIATION STATE
DOCUMENT EVIDENCE
        |
        v
CANDIDATE GRAPH CONSTRUCTION
        |
        v
LLM SEMANTIC REASONING
AND HYPOTHESIS GENERATION
        |
        v
RFC 6902 STRUCTURAL GATE
        |
        v
FACTOR GRAPH
        |
        + hard accounting factors
        + amount factors
        + date factors
        + reference factors
        + counterparty factors
        + historical factors
        + LLM semantic factors
        |
        v
SOLVER ROUTER
        |
   +----+-----------+--------------+
   |                |              |
 exact          min-cost       probabilistic
 solver           flow           inference
                                    |
                           approximate solver
                           when necessary
        |
        v
POSTERIOR / CANDIDATE DISTRIBUTION
        |
        v
SHANNON ENTROPY
        |
    +---+----+
    |        |
COLLAPSE    HOLD
    |        |
    |        +---- LLM identifies useful
    |              missing evidence
    |                    |
    |                    v
    |                 retry
    |
    v
DETERMINISTIC RECONCILIATION VALIDATION
        |
        v
IMMUTABLE RECONCILIATION STATE
```

This should be understood as a conceptual architecture rather than a requirement that every transaction pass through every box.

A trivial deterministic match may bypass most of it.

The sophisticated machinery exists for the difficult tail.

---

# 23. V1 Implementation Path

Toro should not attempt to implement the entire mathematical architecture immediately.

The first version should establish the representation correctly.

The existing immutable reconciliation foundation already provides the canonical bank records, Shadow ERP journal records, state memberships, match groups, predecessor chains and deterministic closing calculations required underneath this system. 

The next implementation should therefore concentrate on a **reconciliation candidate graph**.

Every bank line should be capable of having zero or more candidate relationships to book-side objects.

Each candidate relationship should record which pieces of evidence support or contradict it.

At first, the probability model can remain deliberately simple.

The existing deterministic V1 rules of equal amount, compatible direction and configurable date windows already provide an excellent candidate-generation boundary. 

The first probabilistic version can add semantic evidence and empirical weighting without attempting Monte Carlo inference.

Once real accountant review data accumulates, Toro can begin measuring how predictive each factor actually is.

For example:

[
P(\text{match}\mid\text{exact reference})
]

can eventually be estimated from observed reconciliations.

So can:

[
P(\text{match}\mid\text{same counterparty, exact amount, 3-day delay}).
]

This allows the cost model to move gradually from engineered heuristics toward calibrated probabilities.

Only after this representation is stable should Toro introduce a solver capable of global assignment.

Minimum-cost flow is an excellent candidate for the first global solver because the existing V1 problem already contains one-to-one and manually grouped money-allocation relationships.

More sophisticated probabilistic inference should follow only when real reconciliation cases demonstrate that global dependencies cannot be represented adequately by the simpler solver.

MCMC and SMC therefore belong to the architectural roadmap, not the first milestone.

---

# 24. Calibration and Learning From Accountant Decisions

The system must eventually learn whether its probabilities mean what they claim to mean.

If Toro says "95% probability" one thousand times, approximately 950 of those relationships should prove correct if the model is properly calibrated.

Otherwise a 95% number is merely an internal score disguised as probability.

Human reconciliation decisions therefore become valuable feedback.

When an accountant accepts a candidate, rejects it, constructs a grouped match, or identifies the correct alternative, Toro receives labeled evidence.

This history can gradually improve:

[
P(E\mid M)
]

and:

[
P(M).
]

Different realms may eventually have different priors.

A petrol station may have highly regular daily TPE settlements.

A construction company may have irregular supplier settlements.

A retail chain may commonly aggregate hundreds of transactions.

The same observation can therefore mean different things in different businesses.

The probabilistic architecture allows Toro to learn those differences without rewriting the reconciliation engine itself.

Predictive checking should also become part of model evaluation. In Bayesian workflows, posterior predictive checks compare data simulated from a fitted model against observed data to determine whether the model captures relevant characteristics of reality. ([Stan][7])

Toro can apply the same philosophy operationally: inferred reconciliation behavior should continually be compared with what accountants ultimately approve.

---

# 25. Explainability

A mathematical system used in accounting cannot simply return:

```text
MATCHED: 98.7%
```

It must explain why.

Every candidate should retain its evidence decomposition.

An accountant should be able to inspect something conceptually equivalent to:

```text
Candidate:
Bank line B17 <-> Invoice A92

Exact amount:              strong support
Direction:                 hard-valid
Currency:                  hard-valid
Invoice open balance:      hard-valid
Counterparty identity:     strong support
Reference "F445":          very strong support
Date difference:           moderate support
Opening-state conflict:    none
Competing candidates:      weak

Posterior probability:     0.987
Entropy contribution:      low
Solver:                    min-cost assignment
```

The exact interface can come later.

The requirement is architectural: evidence and inference provenance must not disappear once a solver produces an answer.

This aligns with the existing requirement that Toro retain enough linkage to explain every closing difference and carried-forward item to an accountant. 

---

# 26. Failure Philosophy

The engine must be designed around the assumption that some reconciliation problems will remain unresolved.

Failure to collapse is not a system failure.

It is information.

If:

[
H(X)
]

remains high after available evidence has been exhausted, the correct state is HOLD.

If no candidate world satisfies the accounting constraints, the correct conclusion is not to choose the least-bad world.

It is:

```text
NO VALID RECONCILIATION CONFIGURATION FOUND
```

That distinction is essential.

Optimization algorithms always want to optimize something.

Toro must ensure they are never forced to produce an accounting answer when the valid answer is "insufficient or contradictory evidence."

The graph therefore needs an explicit unresolved state.

There should always be a mathematically legitimate path corresponding to:

[
X=\text{UNRESOLVED}.
]

Otherwise the optimizer may be forced to choose an incorrect match simply because every transaction is required to go somewhere.

---

# 27. Success Criteria

The success of this system should not initially be measured by whether Toro implements sophisticated Bayesian or Monte Carlo mathematics.

It should be measured by whether the architecture permits increasing mathematical sophistication without changing the accounting truth layer.

A successful first generation will allow a bank movement to have competing hypotheses, preserve the evidence supporting each hypothesis, enforce hard accounting constraints, use LLM reasoning only to contribute structured semantic evidence or new hypotheses, assign interpretable scores or probabilities, compute uncertainty and refuse to reconcile when uncertainty remains excessive.

A successful second generation will solve globally interacting relationships rather than considering transactions independently.

A successful later generation will be capable of selecting approximate probabilistic inference when exact solutions become computationally impractical.

Throughout every generation, reconciliation remains subordinate to the immutable accounting state.

---

# 28. Final Architectural Principle

The reconciliation engine should not be built around one mathematical theorem.

It should be built around a mathematical **representation of uncertainty and constraints**.

The representation is the durable asset.

Once Toro can represent:

[
\text{variables}
]

[
\text{hypotheses}
]

[
\text{evidence}
]

[
\text{factors}
]

[
\text{hard constraints}
]

and:

[
\text{probability distributions},
]

different solvers can evolve underneath it.

Minimum-cost flow can solve the problems that are naturally flows.

Exact inference can solve small structured probabilistic problems.

Integer optimization can solve combinatorial accounting constraints.

Monte Carlo methods can approximate distributions when exact enumeration becomes impossible.

LLMs can interpret semantics, create hypotheses and determine what additional information should be sought.

Shannon entropy can remain ASE's common measure of uncertainty.

And the immutable reconciliation service remains the final gate deciding what becomes accounting state.

The intended relationship is therefore:

[
\boxed{
\text{LLM reasoning}
+
\text{probabilistic factors}
+
\text{optimization}
+
\text{accounting invariants}
+
\text{entropy}
}
]

feeding:

[
\boxed{
\text{immutable deterministic reconciliation state}
}
]

The central design philosophy is:

> **The LLM proposes meaning. Probability measures belief. Optimization searches for globally coherent explanations. Accounting laws eliminate impossible worlds. Entropy determines whether uncertainty has collapsed. Immutable state records what was actually accepted as accounting truth.**

That is the mathematical direction Toro should pursue for the reconciliation stage.

[1]: https://developers.google.com/optimization/flow/mincostflow?hl=en&utm_source=chatgpt.com "Minimum Cost Flows  |  OR-Tools  |  Google for Developers"
[2]: https://developers.google.com/optimization/flow/assignment_min_cost_flow?utm_source=chatgpt.com "Assignment as a Minimum Cost Flow Problem  |  OR-Tools  |  Google for Developers"
[3]: https://web.stanford.edu/~montanar/TEACHING/Stat375/papers/sumprod.pdf?utm_source=chatgpt.com "Factor graphs and the sum-product algorithm - Information Theory, IEEE Transactions on"
[4]: https://stanford.edu/~shervine/teaching/cs-221/cheatsheet-variables-models/?utm_source=chatgpt.com "CS 221 - Variables-based Models Cheatsheet"
[5]: https://web.stanford.edu/~lindrew/cs228.pdf?utm_source=chatgpt.com "CS 228: Probabilistic Graphical Models"
[6]: https://mc-stan.org/docs/2_36/reference-manual/mcmc.html?utm_source=chatgpt.com "MCMC Sampling"
[7]: https://mc-stan.org/docs/2_38/stan-users-guide/posterior-predictive-checks.html?utm_source=chatgpt.com "Posterior and Prior Predictive Checks"
