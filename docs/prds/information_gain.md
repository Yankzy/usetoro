# Product Requirements Document (PRD)

## Layer 2: Information Gain Runtime and Metric Framework

**Status:** Draft v1.0  
**Owner:** Runtime & AI Infrastructure Engineering  
**Audience:** Runtime Engineering, AI Infrastructure, ASE Core  

---

## 1. Objective

Implement the runtime measurement and tracking of **Information Gain (IG)** across the Autonomous Semantic Engine (ASE) node network. 

The existing `ase` package computes Shannon entropy $H(X)$ locally at a node level to represent the node's immediate classification uncertainty. However, entropy is a static snapshot. 

This document defines the requirements for **Layer 2: Information Gain**, which measures the *change* in uncertainty over time (across reasoning steps/probes). The output of this framework will allow the ASE to optimize reasoning execution budgets, prune unproductive LLM execution branches, detect cognitive loops, and identify sub-optimal node configurations.

---

## 2. Problem Statement

Currently, the ASE scheduling and execution decisions are based on static entropy and confidence thresholds:
* If $C \ge 0.98$, the node collapses.
* If $C < 0.98$, the node continues to THINK/ACTIVATE (publishing requests for more context, running more probes).

This static approach introduces several system inefficiencies:
1. **Unproductive Probes (Cognitive Gridlock):** A node can trigger multiple external LLM probes or request cycles (`LifetimeProbes`) without significantly reducing its entropy. The system continues to consume tokens and latency budget even when subsequent steps yield near-zero improvement.
2. **Undifferentiated Reasoning Priority:** The ASE cannot tell the difference between a probe that successfully resolved $0.80$ bits of entropy versus a probe that only resolved $0.01$ bits.
3. **Static DAG Performance Auditing:** There is no programmatic metric to audit which nodes in a workflow DAG are consistently high-performing (driving rapid convergence) versus those that are poorly designed (contributing little to entropy reduction).

---

## 3. Design Principles

Before defining the mathematics, it is important to understand the philosophical approach of Layer 2:

1. **Entropy measures state.** It tells us how uncertain we are right now.
2. **Information Gain measures progress.** It tells us how much we learned from our last action.
3. **ASE optimizes progress, not merely low entropy.** A system that aggressively seeks information gain will naturally reach low entropy.
4. **A node that reaches low entropy efficiently is more valuable than one that reaches the same entropy after many reasoning cycles.**
5. **Information Gain is therefore a first-class runtime metric rather than merely an analytical metric.** It is used immediately by the `ASE` to make real-time execution decisions.

---

## 4. Mathematical Modeling of Information Gain

### 4.1 Definition of a "Step"

To compute Information Gain deterministically, we must precisely define what constitutes a step $t$. A step represents a single reasoning or evidence-gathering cycle that mutates the node's state.

The exact lifecycle for Information Gain computation is:
1. **Node Activated**
2. **Entropy Snapshot** ($H(t-1)$)
3. **Reasoning** (LLM invocation, external evidence gathering, etc.)
4. **State Update** (Property candidates are mutated)
5. **Entropy Recomputed** ($H(t)$)
6. **Information Gain Computed**
7. **Telemetry Published**

### 4.2 Single-Step Information Gain

For an individual property key $k$ (e.g., `resolved_account_id`), the actual Information Gain $IG_k$ at probe step $t$ is defined as:

$$IG_k(t) = H_k(t-1) - H_k(t)$$

Where $H_k(t)$ is the Shannon entropy calculated using `CalculateEntropy` for the candidates of property $k$ at step $t$.

### 4.3 Negative Information Gain

Entropy does not always decrease. If a node starts with high certainty (e.g., Expense = 99%) but receives new evidence that invalidates this assumption (e.g., Expense = 40%, Asset = 60%), entropy will increase.

In this scenario, Information Gain will be negative (e.g., $IG = -0.34$).
**Negative Information Gain is allowed and valuable.** It signals that new evidence has successfully invalidated a previous, potentially incorrect certainty.

### 4.4 Expected Information Gain

Before executing a potentially expensive operation, the ASE needs to evaluate if the reasoning is worth paying for. We define **Expected Information Gain** ($ExpectedIG$) as the anticipated reduction in entropy.

For example, the scheduler can ask: *Should I spend 3000 tokens to gain an expected 0.01 bits of information?* If $ExpectedIG$ is below a threshold, the ASE may choose to halt reasoning or escalate to a human.

### 4.5 Unified Node Information Gain

For a node with multiple property requirements, the Unified Information Gain $IG_{unified}$ is the difference in the node's unified entropy over a transition step:

$$IG_{unified}(t) = H_{unified}(t-1) - H_{unified}(t)$$

Where $H_{unified}$ is the sum of the property entropies:

$$H_{unified} = \sum_{k \in \text{properties}} H(k)$$

### 4.6 Normalized Information Gain

Absolute Information Gain can be misleading when comparing nodes with different starting entropies. Removing 0.9 bits from a starting entropy of 1.8 represents a 50% reduction, whereas removing 0.2 bits from 0.2 represents 100% convergence.

We define **Normalized Information Gain** to allow fair comparison:

$$NormalizedIG = \frac{IG}{EntropyBefore}$$

This bounds the value between $0$ and $1$ (excluding cases of negative IG), making it much easier to compare the relative performance of different nodes.

### 4.7 Cumulative Information Gain

To measure the overall progress of a transaction micro-agent from its raw ingestion ($t=0$) to its current state ($t=n$):

$$IG_{cumulative}(n) = H_{unified}(0) - H_{unified}(n)$$

### 4.8 Composite Efficiency Metrics

Information Gain alone does not capture the cost of reasoning. Two nodes might both achieve $IG = 0.4$, but one required 8 LLM calls while the other required 1.

We define the following composite optimization metrics:
* **$IGPerProbe$**: $\frac{IG_{cumulative}(n)}{\text{LifetimeProbes}}$
* **$IGPerSecond$**: $\frac{IG_{cumulative}(n)}{\text{ExecutionDurationSec}}$
* **$IGPerToken$**: $\frac{IG_{cumulative}(n)}{\text{TotalTokensConsumed}}$

### 4.9 Implementation Location

All mathematical formulations and helpers for calculating Information Gain (single-step, unified, cumulative, normalized, and composite metrics) must be implemented inside [math.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/math.go).

---

## 5. Proposed Changes to `ase` Package

To implement Layer 2, the `ase` runtime package must be extended to track historical entropy values and evaluate the efficiency of state transitions.

### 5.1 Node State Extension ([node.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/node.go))

* **History Tracking:** We must store historical entropy/confidence snapshots to calculate the difference.
* **Execution Trace Integration:** Extend `NodeExecutionStep` to capture the entropy and confidence immediately before and after the step execution.

```go
// Add to NodeExecutionStep in node.go
type NodeExecutionStep struct {
	DAGNodeID        string                 `json:"dag_node_id"`
	ResumeNodeID     string                 `json:"resume_node_id,omitempty"`
	Kind             string                 `json:"kind"`
	PropertyKey      string                 `json:"property_key,omitempty"`
	Candidates       []ProbabilityCandidate `json:"candidates,omitempty"`
	SelectedEdge     string                 `json:"selected_edge,omitempty"`
	Timestamp        time.Time              `json:"timestamp"`
	
	// Layer 2 additions
	EntropyBefore    float64                `json:"entropy_before"`
	EntropyAfter     float64                `json:"entropy_after"`
	ConfidenceBefore float64                `json:"confidence_before"`
	ConfidenceAfter  float64                `json:"confidence_after"`
}
```

### 5.2 Telemetry Additions ([telemetry.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/telemetry.go))

* Emitted telemetry payloads (`TelemetryPayload`) must include the new metrics and **versioning** to ensure historical comparisons remain valid if the algorithm is upgraded.

```go
type TelemetryPayload struct {
	// Existing fields ...
	EventType             TelemetryEventType `json:"event_type"`
	NodeID                string             `json:"node_id"`
	Entropy               float64            `json:"entropy"`
	Confidence            float64            `json:"confidence,omitempty"`
	
	// Layer 2 additions
	InformationGain       float64            `json:"information_gain"`
	NormalizedIG          float64            `json:"normalized_ig"`
	CumulativeGain        float64            `json:"cumulative_information_gain"`
	ExpectedIG            float64            `json:"expected_ig,omitempty"`
	IGVersion             string             `json:"ig_version"`      // e.g., "1.0"
	EntropyVersion        string             `json:"entropy_version"` // e.g., "1.0"
	Timestamp             time.Time          `json:"timestamp"`
}
```

---

## 6. Runtime Strategies (ASE)

The ASE and the node executor use Information Gain **immediately** at runtime to make intelligent execution choices:

### 6.1 Cognitive Loop & Plateau Prevention
If a node has been probed multiple times (e.g., `LifetimeProbes >= 3`) and the Information Gain for the last probe drops below a defined threshold:

$$IG_{unified}(t) < \text{MinInformationGainThreshold} \quad (\text{e.g., } 0.02)$$

The ASE will conclude that the current reasoning path has plateaued. Instead of scheduling another expensive LLM probe, the node will:
1. Halt automated worker loops.
2. Route itself directly to `HOLD_AMBIGUOUS` or the Moroccan Exception-Pilot Operational Dashboard (`EXCEPTION_QUEUE`).

### 6.2 Priority-Based Telemetry Queueing
When the NATS event mesh handles high volumes of concurrent transactions:
* Nodes with high $ExpectedIG$ are scheduled with higher NATS queue priority.
* Nodes with low $ExpectedIG$ are processed as low-priority background events.

---

## 7. Analytics and Observability

Unlike runtime strategies, which act on immediate $IG$ values, analytics involve aggregating metrics over time to evaluate node and DAG performance.

### 7.1 Developer Dashboard Metrics
The system aggregates telemetry logs to compute:
* **Average Information Gain:** Contribution of each DAG node across executions.
* **Percentiles (p50, p90, p99):** Of IG and Efficiency metrics ($IGPerToken$, $IGPerSecond$).
* **Historical Trends:** Tracking node convergence performance over time.
* **Node Rankings:** 
  * **Low-performing nodes:** Consistently showing $IG < 0.05$. These are candidates for prompt refinement or topological redesign.
  * **High-performing nodes:** Driving $IG > 0.80$ in a single step. These are recognized as critical decision junctions.

### 7.2 Persistence Policy
Telemetry describing Information Gain must be persisted efficiently:
* **Execution Trace (Raw Probes):** Retained for **7 days** to allow immediate debugging of specific transaction paths.
* **Aggregates:** Retained **forever** to support historical trend analysis, node rankings, and algorithm version comparisons.

---

## 8. Failure Modes

If a step cannot accurately compute Information Gain (for example, if $EntropyBefore$ is missing due to an initialization error or corrupted state):
* The system should emit $IG = 0$ in the telemetry payload.
* The system should log a `WARN` level event indicating missing prior entropy.
* **Do not fail the node.** The node's execution and transition should continue based on the current state, ensuring robustness despite metric computation failures.

---

## 9. Acceptance Criteria

Engineering is done when:
* **Mathematical Correctness:** Information Gain is computed deterministically.
* **Negative IG Support:** Negative Information Gain is correctly calculated and supported by the runtime.
* **State Persistence:** Historical snapshots are retained for the execution lifetime of the node.
* **Telemetry Compatibility:** The telemetry schema is backward compatible and includes `InformationGainVersion` and `EntropyVersion`.
* **Observability:** IG, Normalized IG, Expected IG, and composite metrics ($IGPerProbe$, $IGPerSecond$, $IGPerToken$) are exported to observability platforms.
* **Testing:** Unit tests verify mathematical correctness (including negative IG scenarios). Integration tests verify correct telemetry emission across a full node lifecycle step.
