# Product Requirements Document (PRD)

## 6. Enterprise State Engine (ESE) & Enterprise Awareness System

**Document Reference:** `docs/prds/harness_10x/6_enterprise_state_engine.md`  
**Status:** Approved Master Architecture v5.3  
**Owner:** Core Architecture & Platform Engineering  
**Subsystem:** System 5 of the 5 Core Harness Subsystems  
**Target Architecture:** Toro Enterprise Runtime (TAP / ASE / AlloyDB)

---

# 1. Executive Summary & Core Philosophy

The **Enterprise State Engine (ESE)** is the **Enterprise Awareness System (System 5)** of the harness. It maintains a live, continuous mathematical world model of complex systems.

```text
                    ENTERPRISE / PROJECT / DOMAIN
                                  │
               distributed observations (NATS / ASE)
                                  │
                                  ▼
                        ┌──────────────────┐
                        │   ESE / SYSTEM 5 │
                        │                  │
                        │  Enterprise &    │
                        │  Domain State    │
                        └────────┬─────────┘
                                 │
                                 ▼
                  ACTUAL vs DESIRED STATE FIELD
                                 │
                                 ▼
                        ┌──────────────────┐
                        │   STATE FIELD    │
                        │                  │
                        │ Entropy Delta    │
                        │ Confidence       │
                        │ Risk             │
                        │ Evidence         │
                        │ Temporal change  │
                        │ Graph dynamics   │
                        └────────┬─────────┘
                                 │
                                 ▼
                        MAXWELL THRESHOLD
                 (State/Consequence Dependent)
                                 │
                       ┌─────────┴─────────┐
                       │                   │
                  Below threshold      Threshold crossed
                       │                   │
                       ▼                   ▼
                    OBSERVE          SPAWN AGENTS
                                 (State-Based Work)
                                           │
                                   "Restore Balance /
                                    Reduce Uncertainty"
                                           │
                                           ▼
                                   SYSTEM 2 DECISION
                                           │
                                           ▼
                                   SYSTEM 3 EXECUTION
                                           │
                                           ▼
                                   ESE RE-OBSERVES
                                 (Field Restoration)
```

### 1.1 The 3 Distinct Metaphors of ESE

1. **Bee Colony: How Awareness is Generated**
   - The enterprise or project produces countless local observations across workflows, agents, transactions, documents, communications, and NATS telemetry (`ase.telemetry.*`).
   - No individual micro-agent needs a complete understanding of the enterprise. ESE continuously incorporates tiny micro-observations into an aggregate state.
   - **Key Principle:** *Distributed sensing without centralized consensus.*

2. **Entropy Field: How State is Represented and Visualized**
   - ESE models state not as static database rows or simple dashboard metrics, but as a continuous **State Field**.
   - The field dynamically characterizes:
     - Regions of stability vs. regions of uncertainty
     - Localized disturbances vs. propagating disturbances (e.g. component delay $\rightarrow$ installation delay $\rightarrow$ inspection bottleneck $\rightarrow$ client handover)
     - Systemic shifts and state gradients
     - High-entropy vs. low-entropy subgraphs
     - Temporal derivatives (rate of state change $\frac{d\vec{V}}{dt}$)
   - **Signal Processing Independence:** Graph Fourier Transforms (GFT), Bayesian inference, GNN embeddings, Kalman filtering, and temporal graph networks function as **field-analysis mechanisms** over this landscape.

3. **Maxwell's Demon: When Information Becomes Actionable**
   - Awareness alone does not automatically trigger execution. ESE calculates when state disturbances cross a consequence-dependent significance boundary known as the **Maxwell Threshold**.
   - Crossing the Maxwell Threshold creates an **Intervention Condition**, spawning agents with a specific **State Objective**: *"Reduce the entropy and restore balance to this region of the project."*
   - Selective control directives:
     - **DO / ACCELERATE**: *"Accelerate this high-value milestone."*
     - **DON'T / BLOCK**: *"Prevent this action; high entropy or risk detected."*
     - **ESCALATE / INVESTIGATE**: *"Context missing; request virtual workforce input."*
     - **REQUIRE VERIFICATION**: *"Action is irreversible; mandate dual approval."*
     - **OBSERVE**: *"State is within healthy threshold bounds; leave alone."*

> **Core Principle:** *ESE creates awareness. Maxwell's threshold converts awareness into agency.*

### 1.2 Autonomic Nervous System vs. Management Dashboard
The ESE is **not built as a management dashboard**. Dashboards observe; operating systems act. The primary consumer of ESE is **the harness itself**, functioning as an artificial autonomic nervous system. Just as the human autonomic nervous system continuously senses heart rate and blood sugar without conscious mental intervention, ESE senses state changes and signals System 2 and System 3 to react.

### 1.3 Decoupling Responsibilities: System 5 vs. System 2 vs. System 3
* **Enterprise State Engine (System 5)**: Answers *"What is true about the world right now?"*
* **Decision System (System 2)**: Answers *"Which action yields the highest Expected Value?"* ($EV = (P(S \mid a) \cdot ExpectedIG(a)) - Cost_{\text{dynamic}}(a)$).
* **Execution System / Orchestrator (System 3)**: Answers *"Execute the chosen action safely."*

---

### 1.4 Enterprise IP Protection & The Intelligence Context Firewall

A critical strategic and architectural function of the Enterprise State Engine is serving as the **Intelligence Context Firewall (Contextual Isolation)** between enterprise IP and external AI models (LLMs).

#### The Enterprise IP Problem
Traditional enterprise AI implementations treat LLM context windows as a temporary representation of the company's brain, feeding entire SOPs, transaction histories, pricing logic, supplier terms, and organizational dependencies into model prompts. This creates severe enterprise IP leakage risks, vendor lock-in, and unpredictable compliance boundaries.

#### The ESE Context Firewall Solution
> **The model doesn't own your company's intelligence. Your company does.**

The company's core intellectual property (SOPs, ledgers, relationships, risk profiles, historical decisions) resides permanently inside the **Enterprise State Model (ESE)**. External LLMs are treated as **disposable reasoning infrastructure**.

```text
                 COMPANY IP / SOPs / LEDGERS
                             │
                             ▼
                ┌─────────────────────────┐
                │          ESE            │
                │                         │
                │ Enterprise Knowledge    │
                │ Enterprise State        │
                │ Relationships           │
                │ SOPs & Policies         │
                │ History & Memory        │
                │ Confidence & Entropy    │
                └────────────┬────────────┘
                             │
                     MINIMUM CONTEXT SLICE
                     (Entropy Balance)
                             │
                             ▼
                     ┌──────────────┐
                     │     LLM      │
                     │              │
                     │ Reason about │
                     │ this task    │
                     └──────┬───────┘
                            │
                      proposed result
                            │
                            ▼
                           ESE
                            │
                      state changes
                            │
                            ▼
                     System 2 / 3
```

#### Minimum Information Principle
Instead of sending massive enterprise context to an LLM, ESE evaluates the state entropy field and asks:
> **"What is the minimum information required to reduce the uncertainty of the current enterprise state enough to act?"**

#### Strategic Positioning: The Enterprise Moat
* **Don't put your company inside an LLM. Put the LLM inside your company's state architecture.**
* The moat lives in the **ESE State Architecture + Knowledge Graph + Memory**, allowing the enterprise to swap LLM providers seamlessly without rebuilding or leaking institutional intelligence.
* *"We don't give an AI access to your company. We give your company an intelligence system that can safely use AI."*

---

### 1.5 Domain-Independent State Engine & Desired State Basins

A fundamental insight in v5.3 is that the Enterprise State Engine is **not merely an enterprise accounting/procurement monitoring tool**—it is a **general computational engine for managing complex systems**.

#### 1.5.1 Desired State Field vs. Actual State Field
Every complex project or domain begins by constructing a **Target / Desired State Field**.

For example, an **Interior Design Office Project**:
```text
                    DESIRED OFFICE STATE
                              │
        ┌─────────────────────┼─────────────────────┐
        │                     │                     │
        ▼                     ▼                     ▼
    Reception              Lounge               Workspace
        │                     │                     │
   ┌────┼────┐            ┌───┼────┐            ┌───┼────┐
   ▼    ▼    ▼            ▼   ▼    ▼            ▼   ▼    ▼
Color Desk Sign         Sofa Coffee Bar       Chair Light
```
Each entity maintains state variables (Type, Quantity, Supplier, Specs, Budget, Delivery Date, Quality Grade, Installation Status, Confidence).

As the project progresses, ESE measures the **delta** between the **Actual State Field** and the **Desired State Field**:
```text
State Delta: ΔV = V_actual - V_desired
```

#### 1.5.2 Productive Uncertainty vs. Dangerous Uncertainty
Universal entropy minimization is an anti-pattern. Early in creative or architectural phases (e.g. initial interior design concepting), uncertainty about materials or layouts represents **productive uncertainty**.

The goal of ESE is:
> **Manage the state field toward the desired state, subject to acceptable uncertainty bounds.**

Maxwell Thresholds are consequence- and context-dependent. A $50 coffee machine delay creates a minor localized disturbance, whereas a missing structural beam in construction or a missing tax filing deadline creates a critical disturbance crossing the Maxwell Threshold.

#### 1.5.3 State-Based Autonomous Work & Closed Feedback Loop
When a disturbance crosses the Maxwell Threshold, agents are deployed with a **State Objective**: *"Reduce entropy in this region of the project."*

```text
Disturbance Detected (e.g. Chair Delay)
       │
       ▼
   HIGH ENTROPY FIELD
       │
       ▼
 Maxwell Threshold Crossed
       │
       ▼
   Agent Spawned (State Objective: Restore Chair Balance)
       │
   ┌───┼───────────┐
   ▼   ▼           ▼
Contact  Find      Check
Supplier Alternatives Budget
   │       │         │
   └───┬───┴─────────┘
       ▼
   System 2 EV Evaluation
       │
       ▼
   System 3 Action Execution
       │
       ▼
   ESE Re-Observes State Field
       │
   ┌───┴────────────────┐
   ▼                    ▼
Field Restored       Field Still Distorted
(Entropy Falls)      (Spawn Secondary Intervention)
```

**Agent Success Criterion:** Success is **not** merely "workflow step executed." Success is **empirically verified restoration of the state field into its desired state basin**.

#### 1.5.4 Multi-Domain Generalization
The ESE engine operates seamlessly across any domain:
* **Interior Design**: Desired office state $\rightarrow$ procurement $\rightarrow$ installation $\rightarrow$ inspection $\rightarrow$ handover.
* **Software Engineering**: Desired product state $\rightarrow$ spec $\rightarrow$ architecture $\rightarrow$ impl $\rightarrow$ tests $\rightarrow$ deployment.
* **Financial Accounting**: Desired ledger state $\rightarrow$ documents $\rightarrow$ reconciliation $\rightarrow$ classification $\rightarrow$ filing.
* **Construction**: Desired building state $\rightarrow$ materials $\rightarrow$ subcontractors $\rightarrow$ milestones $\rightarrow$ completion.
* **Legal Operations**: Desired legal outcome $\rightarrow$ discovery $\rightarrow$ filings $\rightarrow$ deadlines $\rightarrow$ resolution.

---

# 2. Re-Architecting `EnterpriseStateVector`: State Description vs. Derived Parameters

### 2.1 Concept: State Description vs. Derived Parameters
The `EnterpriseStateVector` is strictly the **mathematical description of the enterprise or project's current state**.

```text
EnterpriseStateVector (System 5 - State Description)
│
├── Global State Index (Unified Entropy H_global, Mean Confidence)
├── Domain & Project States (Accounting, Design, Procurement, Logistics)
├── Entity States (Fact-level probabilities & completeness)
├── Entropy Distribution (Spatial spectral decomposition)
├── State Field Delta (ΔV = V_actual - V_desired)
├── Confidence Distribution (Property-level certainty)
├── Evidence Completeness (Attachable & document coverage)
├── Risk Fields (Fraud risk, milestone delay risk, compliance risk)
├── Temporal Derivatives (dEntropy/dt, velocity of uncertainty)
├── Graph Spectral Signals (GFT laplacian eigenvalues & energy)
├── State Transitions (Historical trajectory vectors)
└── Maxwell-Threshold Conditions (Active intervention boundaries)
```

---

# 3. Graph Taxonomy & Storage Architecture Decision

## 3.1 The 4 Graphs of the Harness Architecture

1. **Workflow Graph (DAG)**: Execution order, node steps, and edge routing (`dag.go`).
2. **Knowledge Graph**: Directional relations between enterprise and project entities.
3. **Communication Graph**: Multi-agent interaction topologies over TAP NATS performatives.
4. **Entropy Graph (The Entropy Field)**: **The Knowledge Graph annotated with dynamic state vectors, Shannon entropy values, and state field deltas ($\Delta V$).**

## 3.2 Storage Engine Analysis: PostgreSQL Property Graph vs. Neo4j vs. In-Memory

| Architectural Criteria | Standalone Graph DB (e.g., Neo4j) | Pure In-Memory Graph | Native AlloyDB/PostgreSQL Property Graph |
|---|---|---|---|
| **ACID Integrity** | Secondary transaction boundary; dual write sync risk. | None. Volatile (Server crash = total state loss). | **Single Source of Truth**; native ACID SQL transactions. |
| **Operational Complexity** | High (Requires separate cluster, backup, monitoring). | High (Requires state snapshotting & recovery logic). | **Zero additional infra** (Reuses primary database). |
| **Traversal Depth Required** | Optimized for 6+ hop social graphs. | Ultra-fast in-memory lookup. | **Optimized for 1-3 hop operational traversals** via Recursive CTEs & B-Trees. |
| **Data Fusion (SQL + Vectors)** | Poor (Separate from pgvector & JSONB). | Poor. | **Seamless** (SQL, pgvector, JSONB, and graph in 1 query). |
| **Decision Verdict** | ❌ **Rejected for Phase 1** | ❌ **Rejected (Too Volatile)** | ✅ **SELECTED ARCHITECTURE** |

### Selected Architecture: Native AlloyDB / PostgreSQL Property Graph DDL Schema

```sql
CREATE SCHEMA IF NOT EXISTS toro_core;

-- Layer 1: Authoritative Fact Nodes
CREATE TABLE IF NOT EXISTS toro_core.enterprise_facts (
    fact_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    realm_id VARCHAR(64) NOT NULL,
    entity_type VARCHAR(64) NOT NULL, -- e.g. 'invoice', 'chair', 'supplier', 'milestone'
    uri VARCHAR(255) UNIQUE NOT NULL, -- e.g. 'fact:design:furniture:chair_32'
    payload JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Layer 2: Directional Relationships
CREATE TABLE IF NOT EXISTS toro_core.enterprise_relationships (
    relationship_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    realm_id VARCHAR(64) NOT NULL,
    from_fact_id UUID NOT NULL REFERENCES toro_core.enterprise_facts(fact_id) ON DELETE CASCADE,
    to_fact_id UUID NOT NULL REFERENCES toro_core.enterprise_facts(fact_id) ON DELETE CASCADE,
    relation_type VARCHAR(64) NOT NULL, -- e.g. 'PART_OF', 'DEPENDS_ON', 'ISSUED_BY'
    weight FLOAT8 NOT NULL DEFAULT 1.0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT unique_relation UNIQUE (realm_id, from_fact_id, to_fact_id, relation_type)
);

-- Layer 4: Dynamic Probabilistic Object State (The Entropy Field Layer)
CREATE TABLE IF NOT EXISTS toro_core.entity_states (
    fact_id UUID PRIMARY KEY REFERENCES toro_core.enterprise_facts(fact_id) ON DELETE CASCADE,
    confidence FLOAT8 NOT NULL DEFAULT 0.0,
    fraud_risk FLOAT8 NOT NULL DEFAULT 0.0,
    evidence_completeness FLOAT8 NOT NULL DEFAULT 0.0,
    shannon_entropy FLOAT8 NOT NULL DEFAULT 1.0,
    temporal_velocity FLOAT8 NOT NULL DEFAULT 0.0, -- dEntropy/dt
    state_vector JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_rel_from_to ON toro_core.enterprise_relationships(from_fact_id, to_fact_id);
CREATE INDEX idx_facts_realm_type ON toro_core.enterprise_facts(realm_id, entity_type);
```

---

# 4. Enterprise Awareness & Mathematical State Modeling

### 4.1 Shannon Entropy & Mathematical Foundations
The state entropy $H$ of any entity or classification candidate set is computed via normalized Shannon Entropy (`go/internal/erp/ase/math.go`):
$$H = -\sum_{i=1}^N p_i \log_2(p_i)$$

Unified Confidence is modeled inversely:
$$C = 1 - \frac{\sum H(k)}{\text{Total Properties}}$$

### 4.2 Graph Fourier Transform (GFT) & Disturbance Propagation
To characterize how an entropy disturbance propagates across graph topology (e.g. `Chair Supplier Delay` $\rightarrow$ `Lounge Installation` $\rightarrow$ `Final Inspection` $\rightarrow$ `Handover`), ESE uses Graph Fourier Transforms (GFT) over the Graph Laplacian matrix $L = D - A$:
$$\hat{x} = U^T x$$
where $U$ represents the eigenvectors of the normalized Graph Laplacian $L$. Localized disturbances exhibit high-frequency spectral components, whereas systemic shifts manifest as low-frequency spatial patterns across the graph topology.

---

# 5. Maxwell's Threshold to System 2 Dynamic Cost Modulation

When Maxwell's Threshold is crossed, Maxwell's Demons translate state field disturbances into dynamic action costs consumed by System 2 (`policy_engine.go`):

$$EV = (P(S \mid a) \cdot ExpectedIG(a)) - Cost_{\text{dynamic}}(a)$$

```text
Low Entropy State  (Below Threshold)   --> Continue automatically: Cost 1   | Human Review: Cost 500
Distorted Field    (Threshold Crossed) --> Continue automatically: Cost 200 | Ask Supplier: Cost 4 | Human Review: Cost 12
```

---

# 6. Core Go Data Models & Interfaces (`go/internal/erp/ase`)

```go
package ase

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// EnterpriseStateVector represents the live mathematical description of the enterprise or project state.
type EnterpriseStateVector struct {
	GlobalEntropy        float64                `json:"global_entropy"`        // 0.0 (certainty) -> 1.0 (max uncertainty)
	MeanConfidence       float64                `json:"mean_confidence"`       // 0.0 -> 1.0
	DomainEntropies      map[string]float64     `json:"domain_entropies"`      // "accounting": 0.12, "interior_design": 0.65
	TargetStateDelta     float64                `json:"target_state_delta"`    // Magnitude of deviation from desired state
	EvidenceCoverage     float64                `json:"evidence_coverage"`     // 0.0 -> 1.0 completeness
	RiskFieldScore       float64                `json:"risk_field_score"`      // Composite risk index
	TemporalVelocity     float64                `json:"temporal_velocity"`     // dH/dt rate of entropy change
	SpectralEnergy       float64                `json:"spectral_energy"`       // High-frequency GFT disturbance magnitude
	ActiveInterventions  map[string]string      `json:"active_interventions"`  // Region -> "DO" | "DONT" | "INVESTIGATE"
	DerivedParameters    DerivedControlParams   `json:"derived_parameters"`    // Computed downstream parameters
	LastUpdated          time.Time              `json:"last_updated"`
}

// DerivedControlParams contains operational parameters calculated downstream for System 2.
type DerivedControlParams struct {
	AutomationLevel        float64 `json:"automation_level"`
	EvidenceThreshold      float64 `json:"evidence_threshold"`
	HumanReviewBias        float64 `json:"human_review_bias"`
	ExternalValidationBias float64 `json:"external_validation_bias"`
}

// DefaultEnterpriseStateVector provides baseline state initialization.
func DefaultEnterpriseStateVector() EnterpriseStateVector {
	return EnterpriseStateVector{
		GlobalEntropy:      0.15,
		MeanConfidence:     0.88,
		DomainEntropies:    map[string]float64{"accounting": 0.1, "project_management": 0.2},
		TargetStateDelta:   0.05,
		EvidenceCoverage:   0.90,
		RiskFieldScore:     0.05,
		TemporalVelocity:   0.0,
		SpectralEnergy:     0.02,
		ActiveInterventions: map[string]string{},
		DerivedParameters: DerivedControlParams{
			AutomationLevel:        0.85,
			EvidenceThreshold:      0.80,
			HumanReviewBias:        1.0,
			ExternalValidationBias: 1.0,
		},
		LastUpdated: time.Now().UTC(),
	}
}

// EnterpriseAwarenessEngine defines the interface for System 5 (ESE).
type EnterpriseAwarenessEngine interface {
	// GetStateVector returns the live mathematical description of the enterprise/project state.
	GetStateVector(ctx context.Context, realmID, domain string) (EnterpriseStateVector, error)

	// PublishTelemetry ingests micro-observations to continuously update the Enterprise State Field.
	PublishTelemetry(ctx context.Context, telemetry map[string]interface{}) error
}
```

---

# 7. Summary of Architectural Separation

| Subsystem | Primary Question | Metaphor / Role | Inputs | Primary Output |
|---|---|---|---|---|
| **1. Knowledge System** | *How is reality represented?* | Fact & Relationship Model | DB rows, embeddings, speech acts | Structured fact nodes & entity state |
| **2. Decision System** | *Which action has highest EV?* | Maxwell Demons Action Selector | `EnterpriseStateVector`, candidate actions | Selected Action ID & Expected Value |
| **3. Execution System** | *How is work executed & verified?* | Autonomic Motor Response | Selected Action ID, DAG step definition | Executed state mutation / RFC 6902 patch |
| **4. Learning System** | *How does the system adapt?* | Memory & Reflex Evolution | Execution trace, human corrections | Local memory rules & threat priors |
| **5. Enterprise Awareness System (ESE)** | *What is true about the world right now?* | **Bee Colony (Sensing)** + **Entropy Field (State Delta $\Delta V$)** + **Maxwell Threshold** + **Context Firewall** | Observations, telemetry, risk signals, target state | `EnterpriseStateVector` & State Field Distortions |
