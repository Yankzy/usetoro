I actually think these five layers form a coherent mathematical architecture. Each layer answers a different question. If you remove one, the harness loses an important capability.

---

# Layer 1: Shannon Entropy

**Question it answers:**

> *How uncertain is this individual node?*

This is the foundation of everything.

Every node receives some information from upstream and reasons about it.

For example:

```
Transaction

"Amazon - $142.37"
```

The LLM may determine:

* Expense: 98%
* Revenue: 1%
* Transfer: 1%

The entropy is very low.

Now consider:

```
ABC Holdings
$5,200
```

The LLM might determine:

* Asset: 35%
* Expense: 30%
* Equity: 20%
* Liability: 15%

The entropy is high.

The harness immediately knows this node should not proceed.

### Why it's necessary

Without Shannon entropy, every decision becomes binary.

The harness can't distinguish between:

* "I'm almost certain."
* "I'm guessing."

Entropy gives the harness a quantitative measure of uncertainty.

---

# Layer 2: Information Gain

**Question it answers:**

> *How much uncertainty did this node remove?*

Entropy is a snapshot.

Information Gain measures progress.

Example:

Before reasoning:

```
Entropy = 0.91
```

After reasoning:

```
Entropy = 0.08
```

Information Gain:

```
0.83
```

Another node:

Before:

```
0.20
```

After:

```
0.18
```

Information Gain:

```
0.02
```

The first node accomplished much more.

### Why it's necessary

The harness shouldn't treat all nodes equally.

Some nodes unlock huge amounts of downstream work.

Others barely contribute.

Information Gain tells the scheduler where reasoning effort has the greatest payoff.

It also helps identify nodes that are consistently adding little value and may be candidates for redesign.

---

# Layer 3: Mutual Information

**Question it answers:**

> *How much does solving this node reduce uncertainty elsewhere in the graph?*

This is different from Information Gain.

Information Gain is local.

Mutual Information is relational.

Suppose you classify:

```
Expense
```

Immediately, many downstream uncertainties shrink:

* VAT rules
* Required documents
* Ledger accounts
* Vendor matching

You haven't processed those nodes yet, but you've already reduced their uncertainty.

### Why it's necessary

It tells the scheduler which nodes have the greatest influence over the rest of the workflow.

Instead of asking:

> "Which node is ready?"

the scheduler asks:

> "Which node will unlock the most downstream certainty?"

That's a much more powerful scheduling strategy.

---

# Layer 4: Bayesian Updating

**Question it answers:**

> *Given all the evidence collected so far, what should I currently believe?*

The world isn't static.

New evidence arrives continuously.

For example:

Initially:

```
Expense: 60%
Asset: 40%
```

An invoice arrives.

Now:

```
Expense: 98%
Asset: 2%
```

Nothing magical happened.

The system simply updated its belief using new evidence.

### Why it's necessary

Your harness already pauses workflows while waiting for:

* invoices
* receipts
* human approval
* external APIs

When new evidence arrives, you don't want to restart reasoning from scratch.

Bayesian updating lets every node revise its belief incrementally.

This naturally fits your event-driven architecture.

---

# Layer 5: Graph Fourier Transform (GFT)

**Question it answers:**

> *What is the global pattern of uncertainty across the entire workflow?*

The previous four layers are local.

GFT is global.

Imagine this:

One transaction has high entropy.

That isn't concerning.

Now imagine:

Every transaction under one supplier suddenly becomes uncertain.

Or every equity transaction becomes unstable.

Or uncertainty spreads through an entire branch.

The scheduler needs to know whether it's seeing:

* one isolated problem,
* a branch problem,
* or a workflow-wide failure.

GFT analyzes the spatial distribution of uncertainty across the graph.

It distinguishes:

* localized spikes,
* smooth propagation,
* oscillations,
* correlated failures.

### Why it's necessary

Without GFT, the scheduler only sees individual nodes.

With GFT, it understands the health of the workflow as a whole.

That's why I think of it as an analytical or diagnostic layer rather than a reasoning layer.

---

# How the layers work together

I think of them as a pipeline:

```
Evidence

↓

Shannon Entropy
```

"How uncertain am I?"

↓

```
Information Gain
```

"Did this reasoning reduce uncertainty?"

↓

```
Mutual Information
```

"Did reducing my uncertainty also reduce uncertainty elsewhere?"

↓

```
Bayesian Updating
```

"Now that new evidence has arrived, what should I believe?"

↓

```
Graph Fourier Transform
```

"What does the uncertainty landscape of the entire workflow look like?"

---

# The hierarchy

Each layer operates at a different scale:

| Layer                   | Scope                              | Primary Question                               |
| ----------------------- | ---------------------------------- | ---------------------------------------------- |
| Shannon Entropy         | Single node                        | How uncertain is this decision?                |
| Information Gain        | Single node over time              | How much progress did this node make?          |
| Mutual Information      | Relationships between nodes        | Which nodes influence others the most?         |
| Bayesian Updating       | Single node with evolving evidence | How should beliefs change as evidence arrives? |
| Graph Fourier Transform | Entire workflow                    | Is uncertainty localized or systemic?          |

That's why I think these layers complement each other rather than compete. They describe uncertainty at increasing levels of abstraction: from one decision, to one reasoning step, to dependencies, to evolving beliefs, and finally to the health of the entire information refinement graph.

## NOTE: they should be implemented inside go/internal/erp/ase/math.go