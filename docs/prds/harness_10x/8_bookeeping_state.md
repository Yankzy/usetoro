# Product Requirements Document (PRD)

## 8. ESE Bookkeeping Engine & State Subsystem

**Document Reference:** `docs/prds/harness_10x/8_bookeeping_state.md`  
**Status:** Approved Master Architecture v2.0  
**Owner:** Core Architecture & Bookkeeping ESE Engineering  
**Subsystem:** Bookkeeping Domain ESE (System 5 Domain Instance)  
**Target Architecture:** Toro Enterprise Runtime (`services/ese-bookkeeping` Python Microservice / PyTorch Geometric / NATS JetStream / ToroDB)

---

# 1. Executive Summary

The **Enterprise State Engine (ESE)** is Toro's continuous, state-aware world modeling system (System 5 of the 5 Core Harness Subsystems).

For the initial production implementation, ESE is instantiated as a concrete, domain-specific state engine:

> **The ESE Bookkeeping Engine**

The objective is to build a complete, continuously updating **Bookkeeping State Engine** capable of:

1. Ingesting real-time bookkeeping events and evidence across NATS JetStream topics (`ese.bookkeeping.event.*`);
2. Constructing and updating a canonical bookkeeping knowledge graph backed by **ToroDB**'s Layer 1 Authoritative Fact Nodes ([`toro_core.enterprise_facts`](file:///Users/Yankz/programming/usetoro/sql/schema/040_create_toro_core_knowledge_system.sql#L6-L16)) and Layer 2 Directional Relationships ([`toro_core.enterprise_relationships`](file:///Users/Yankz/programming/usetoro/sql/schema/040_create_toro_core_knowledge_system.sql#L28-L39));
3. Maintaining the state of every canonical bookkeeping entity;
4. Maintaining the state of dynamic relationships between entities;
5. Representing current bookkeeping reality as a structured state model composed of 20 first-class state artifacts;
6. Computing local and global field signals over a **PyTorch Geometric (PyG)** heterogeneous graph computation substrate;
7. Detecting graph disturbances, state discontinuities, and accumulating entropy;
8. Determining whether a disturbance crosses the consequence-dependent Maxwell threshold;
9. Triggering the System 2 **Decision Tree / Decision Theory System** when nodes enter `HOLD` or `AMBIGUOUS` states ([`2_decision_system.md`](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/2_decision_system.md));
10. Dispatching appropriate game-theoretic negotiation via the Communication System (System 3: [`3_communication_system.md`](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/3_communication_system.md)) and ASE DAG Engine ([`ase_bridge_worker.go`](file:///Users/Yankz/programming/usetoro/go/internal/workers/ase_bridge_worker.go)), or escalating to human CPAs;
11. Measuring whether intervention restored system balance toward equilibrium;
12. Preserving the complete, auditable history of state transitions and state snapshots.

```text
                                TORO ENTERPRISE PLATFORM

                                  ENTERPRISE STATE ENGINE
                                            │
                                 ┌──────────┴──────────┐
                                 │                     │
                           BOOKKEEPING ESE       Future Domain ESEs
                      (`services/ese-bookkeeping`)
                                 │
                     ┌───────────┼───────────┐
                     ▼           ▼           ▼
                  Events       State       Graph
                     │
                     ▼
                Field State
                     │
                     ▼
               Maxwell Gate
                     │
          ┌──────────┴──────────┐
          ▼                     ▼
       Observe         HOLD / Ambiguous State
                                │
                                ▼
                      System 2 Decision Tree
                   (Expected Value EV Optimization)
                                │
                                ▼
                       System 3 Execution
                   (Demon / TAP Performative)
                                │
                                ▼
                             Action
                                │
                                ▼
                              Event ─────► ESE Re-Observation
```

---

# 2. Microservice Deployment Architecture (`services/ese-bookkeeping`)

The ESE Bookkeeping Engine is deployed as an autonomous **Python Microservice** (`services/ese-bookkeeping`), operating alongside the main Go runtime and auxiliary Python workers.

### 2.1 Why Implemented in Python
PyTorch Geometric (PyG) serves as the core graph computation substrate. Because PyG, PyTorch, Torch-Sparse, and GFT (Graph Fourier Transform) spectral processing libraries operate natively within the Python ecosystem, the ESE state container is implemented in Python (Python 3.11+).

### 2.2 Microservice Responsibilities & Inter-Service Communication
The ESE Python microservice does not handle direct web request routing or raw user API authorization. Instead, it operates as an event-driven state awareness system:

1. **Event Consumption**: Listens for state mutations and raw ingest events over NATS JetStream topics (e.g. `ese.bookkeeping.event.*`, `ase.telemetry.*`).
2. **PyG Graph In-Memory Topology**: Manages in-memory heterogeneous `HeteroData` graph tensor representations loaded continuously from ToroDB.
3. **Field & Maxwell Evaluation**: Continuously calculates local energy $S_i$, graph entropy $H(X)$, and Maxwell score $M$.
4. **State Publication**: Publishes updated `EntityState`, `FieldState`, and `MaxwellState` payloads back to NATS JetStream (`ese.bookkeeping.state.*`).
5. **Persistence Interface**: Connects directly to PostgreSQL / ToroDB (`toro_core`) to persist state transitions, state snapshots, enterprise facts, and relationships.

---

# 3. Integration with ToroDB Knowledge System

Authoritative, long-term state storage for ESE is decoupled from temporary PyG computational graph tensors. ESE persists canonical entity nodes, relationships, and source evidence into **ToroDB**'s core knowledge system.

```text
                               ESE PYTHON SERVICE
                          (`services/ese-bookkeeping`)
                                       │
                         PyG Heterogeneous Computations
                                       │
                                       ▼
                             TORO DB (PostgreSQL)
                                (`toro_core`)
                                       │
        ┌──────────────────────────────┼──────────────────────────────┐
        ▼                              ▼                              ▼
  Layer 1 Fact Nodes           Layer 2 Relationships           Master Documents
`toro_core.enterprise_facts` `toro_core.enterprise_relationships` `toro_core.documents`
```

### 3.1 Layer 1: Authoritative Fact Nodes (`toro_core.enterprise_facts`)
Defined in [`040_create_toro_core_knowledge_system.sql`](file:///Users/Yankz/programming/usetoro/sql/schema/040_create_toro_core_knowledge_system.sql#L6-L16).
Every canonical entity (Company, BankAccount, Transaction, Invoice, Supplier, GLAccount, Evidence) persists as a ground truth fact node:
* `fact_id` (UUID): Unique primary key.
* `realm_id` (TEXT): Tenant/organization identifier.
* `namespace` (TEXT): Domain namespace (e.g. `'bookkeeping'`).
* `entity_type` (TEXT): Canonical entity classification (e.g. `'invoice'`, `'bank_transaction'`).
* `uri` (TEXT): Universal resource identifier (e.g. `'fact:bookkeeping:invoice:inv_983'`).
* `payload` (JSONB): Full structured entity attributes and baseline features.

### 3.2 Layer 2: Directional Relationships (`toro_core.enterprise_relationships`)
Defined in [`040_create_toro_core_knowledge_system.sql`](file:///Users/Yankz/programming/usetoro/sql/schema/040_create_toro_core_knowledge_system.sql#L28-L39).
Typed edges connecting canonical facts maintain directional graph topology:
* `relationship_id` (UUID): Primary key.
* `from_fact_id` & `to_fact_id` (UUID): Foreign keys referencing `enterprise_facts`.
* `relation_type` (TEXT): Edge semantics (`'BELONGS_TO'`, `'MATCHED_TO'`, `'CLASSIFIED_AS'`, `'SUPPORTED_BY'`, `'SETTLED_BY'`).
* `weight` (FLOAT8): Dynamic edge weight reflecting confidence or strength $[0.0, 1.0]$.

### 3.3 Master Documents (`toro_core.documents`)
Defined in [`040_create_toro_core_knowledge_system.sql`](file:///Users/Yankz/programming/usetoro/sql/schema/040_create_toro_core_knowledge_system.sql#L51-L68).
Stores raw proof of purchase artifacts (Invoices, Receipts, Bank Statements) with OCR status, extracted JSON metadata, and SHA-256 deduplication hashes.

### 3.4 Data Access Substrate
Go services interface with the graph store via [`graph_store.go`](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/knowledge_system/graph_store.go) and [`knowledge_system.sql`](file:///Users/Yankz/programming/usetoro/sql/queries/knowledge_system.sql). The ESE Python service reads and writes through compatible SQLAlchemy / Asyncpg drivers.

---

# 4. Alignment with TAP / ASE DAG Execution Engine

The ESE Bookkeeping Engine operates in tight feedback loops with the **Toro Agent Protocol (TAP)** and **Autonomous Semantic Engine (ASE)** DAG Execution Engine.

### 4.1 DAG Workflow Integration
Workflows (e.g. [`ase_bookkeeping.yml`](file:///Users/Yankz/programming/usetoro/tap/workflows/ase_bookkeeping.yml), [`pcm_bookkeeping.yml`](file:///Users/Yankz/programming/usetoro/tap/workflows/pcm_bookkeeping.yml)) are registered in ToroDB ([`007_ase_dags.sql`](file:///Users/Yankz/programming/usetoro/sql/schema/007_ase_dags.sql)) and validated by [`validate_dag.py`](file:///Users/Yankz/programming/usetoro/python-worker/validate_dag.py).
The Go runtime orchestrates execution steps via [`ase_bridge_worker.go`](file:///Users/Yankz/programming/usetoro/go/internal/workers/ase_bridge_worker.go) and [`workflow_schema.go`](file:///Users/Yankz/programming/usetoro/tap/workflows/workflow_schema.go).

### 4.2 State Update Synchronization
1. **Node Step Execution**: As a DAG step completes, an execution step event is emitted.
2. **ESE State Mutator**: ESE intercepts the event, updates the corresponding `EntityState` and `RelationshipState` in ToroDB, and updates node vectors in PyG.
3. **Field Recalculation**: ESE evaluates local energy $S_i$ over affected k-hop subgraphs.
4. **DAG Feedback**: If energy spikes or confidence drops below structural thresholds, ESE updates the DAG node status to `HOLD` or `AMBIGUOUS`.

---

# 5. System 2 Decision Tree & Theory for `HOLD` & Ambiguous States

When Maxwell's evaluation detects a disturbance or a DAG node enters a `HOLD` / `AMBIGUOUS` state (e.g., confidence score below structural threshold $0.98$, missing receipt evidence, ambiguous GL classification), execution halts and the **System 2 Decision Tree / Decision Theory System** is invoked.

```text
                                 DAG / ESE NODE
                                       │
                          Enters `HOLD` or `AMBIGUOUS` State
                           (e.g., Confidence < 0.98)
                                       │
                                       ▼
                           SYSTEM 2 DECISION ENGINE
                         (`docs/prds/harness_10x/2_decision_system.md`)
                                       │
                         Traverse ToroDB Knowledge Graph
                     (`enterprise_facts` + `relationships`)
                                       │
                                       ▼
                            Calculate Expected Value
                  EV(a) = P(S|a) * ExpectedIG(a) - Cost(a)
                                       │
        ┌──────────────────────┬───────┴──────────────┬──────────────────────┐
        ▼                      ▼                      ▼                      ▼
Action 1: Client Outreach Action 2: Vector Search Action 3: Demon Action 4: Human CPA
  (TAP Performative /      (Situation Vector    (Reconciliation /    (System 2 / Human
    Holding Email)            Memory)            Classification)        Escalation)
        │                      │                      │                      │
        └──────────────────────┴───────┬──────────────┴──────────────────────┘
                                       ▼
                              Execute via ASE DAG Engine
                   (`go/internal/workers/ase_bridge_worker.go`)
                                       │
                                       ▼
                            Resume DAG Execution
                        (`ase_resolution_worker.go`)
```

### 5.1 Trigger Conditions for `HOLD` State
A node or transaction enters `HOLD` when:
* Unified Confidence Score falls below the structural threshold ($0.98$);
* Multiple competing candidate matches exist with high entropy ($H(X) > 0.8$);
* Material exposure is high without mandatory supporting evidence;
* Contradictory GL accounting rules are triggered.

Reference: [`test_holding_email/main.go`](file:///Users/Yankz/programming/usetoro/tap/cmd/test_holding_email/main.go) and [`013_ase_session_email_holds.sql`](file:///Users/Yankz/programming/usetoro/sql/schema/013_ase_session_email_holds.sql).

### 5.2 Decision Theory Optimization
As specified in [`2_decision_system.md`](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/2_decision_system.md), System 2 evaluates candidate recovery actions using the Expected Value equation:

$$EV(a) = \left( P(S \mid a) \times ExpectedIG(a) \right) - Cost_{\text{dynamic}}(a)$$

Where:
* $P(S \mid a)$: Historical probability of successfully resolving ambiguity given action $a$.
* $ExpectedIG(a)$: Shannon entropy reduction achieved by obtaining target evidence.
* $Cost_{\text{dynamic}}(a)$: Dynamic cost modulated by the published `BookkeepingState`.

### 5.3 Knowledge Graph Traversal
The Decision Engine queries ToroDB Layer 1 facts and Layer 2 relationships to inspect adjacent node context:
* Connected vendor profiles and historical classification rules;
* Counterparty contact credentials;
* Past resolved holding sessions with matching vector embeddings ([`006_ase_vector_memory.sql`](file:///Users/Yankz/programming/usetoro/sql/schema/006_ase_vector_memory.sql)).

### 5.4 Candidate Action Resolution Pathways
1. **Outbound Client Negotiation**: Dispatches a game-theoretically structured inquiry via the Communication System (System 3: [`3_communication_system.md`](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/3_communication_system.md)) using TAP performatives / holding email ([`test_holding_email/main.go`](file:///Users/Yankz/programming/usetoro/tap/cmd/test_holding_email/main.go)).
2. **Vector Memory Lookup**: Searches historical resolution patterns.
3. **Autonomous Demon Dispatch**: Launches narrow Demons (Evidence, Reconciliation, Classification) executed as DAG action steps by [`action_provider_workers.go`](file:///Users/Yankz/programming/usetoro/go/internal/workers/action_provider_workers.go) via [`ase_bridge_worker.go`](file:///Users/Yankz/programming/usetoro/go/internal/workers/ase_bridge_worker.go).
4. **Human CPA Escalation**: Routes to a human reviewer when cost or risk parameters exceed autonomous safety thresholds.

### 5.5 Execution Resumption
Once ambiguous context is supplied (by human response, document upload, or Demon output), [`ase_resolution_worker.go`](file:///Users/Yankz/programming/usetoro/go/internal/workers/ase_resolution_worker.go) processes the resolution payload, clears the `HOLD` state, updates ToroDB facts, and resumes DAG execution.

---

# 6. Core Principle

ESE does not attempt to store the entire enterprise inside a single vector.

Instead:

> **ESE maintains a decision-sufficient representation of bookkeeping reality.**

The system progressively transforms raw observations into increasingly structured information:

```text
Raw Reality
    │
    ▼
Events
    │
    ▼
Canonical Entities
    │
    ▼
Relationships
    │
    ▼
Entity State
    │
    ▼
Bookkeeping State
    │
    ▼
Field State
    │
    ▼
Disturbance
    │
    ▼
Decision (System 2 EV Optimization)
    │
    ▼
Action (System 3 Execution)
```

This reflects Toro's core intelligence principle:

> **Intelligence is the ability to compress reality while preserving the information necessary to predict, decide, and act.**

---

# 7. Architectural Principles

## 7.1 ESE is a State System

ESE is not:
* a dashboard;
* a database;
* a vector database;
* an LLM prompt;
* a graph database;
* a GNN;
* an anomaly detector.

It is a **continuously updated state system** composed of these supporting technologies.

## 7.2 Events Are Historical Truth

Events record what happened. State represents what the system currently believes.

Therefore:

$$\text{Event} \neq \text{State}$$

An event is immutable. State is a projection derived from events, rules, and current evidence.

## 7.3 State Must Be Explainable

Every important state value must have full provenance. ESE must answer:

> "Why does the system believe this transaction is 94% reconciled?"

The answer points to source events, evidence artifacts, matching rules, model outputs, prior states, and state transitions.

## 7.4 PyG Is the Graph Computation Substrate

Toro uses **PyTorch Geometric (PyG)** in Python for graph representation and computation.

PyG is responsible for graph-level computation:
* Heterogeneous graph representations (`HeteroData`);
* Neighborhood aggregation and message passing;
* Sparse graph computations and convolutions;
* Graph pooling and GPU acceleration.

ESE remains responsible for accounting semantics, state transitions, field energy, materiality, entropy interpretation, Maxwell thresholds, intervention policies, and Demon orchestration.

---

# 8. Bookkeeping Domain Ontology

The primary bookkeeping graph contains the following canonical entity types, persisted as ToroDB facts (`toro_core.enterprise_facts`):

| Entity | Purpose | ToroDB Entity Type |
| :--- | :--- | :--- |
| Company | Bookkeeping owner / Organization | `company` |
| BankAccount | Financial account | `bank_account` |
| BankTransaction | Immutable bank movement | `bank_transaction` |
| Invoice | Supplier/customer invoice | `invoice` |
| Receipt | Proof of purchase | `receipt` |
| Payment | Settlement event | `payment` |
| Supplier | Vendor/counterparty | `supplier` |
| Customer | Customer/counterparty | `customer` |
| GLAccount | Chart-of-accounts destination | `gl_account` |
| GLEntry | Accounting posting | `gl_entry` |
| TaxRecord | Tax/VAT information | `tax_record` |
| Evidence | Supporting source artifact | `evidence` |
| AccountingPeriod | Temporal accounting boundary | `accounting_period` |

---

# 9. Bookkeeping Graph Topology

The bookkeeping graph is represented as a heterogeneous PyG graph:

$$\mathcal{H} = (\mathcal{V}, \mathcal{E}, \mathbf{X}, \mathbf{W})$$

Where $\mathcal{V}$ are typed entities, $\mathcal{E}$ are typed relationships, $\mathbf{X}$ are node state feature tensors, and $\mathbf{W}$ are edge weight tensors.

```text
BankTransaction
      │
      ├── belongs_to ───────► BankAccount
      │
      ├── matched_to ───────► Invoice
      │
      ├── associated_with ──► Supplier
      │
      ├── classified_as ────► GLAccount
      │
      └── supported_by ─────► Evidence

Invoice
      │
      ├── issued_by ────────► Supplier
      ├── settled_by ───────► Payment
      └── contains ─────────► TaxRecord
```

---

# 10. Bookkeeping State Artifacts

The operational memory of ESE consists of 20 first-class state artifacts:

```text
BookkeepingStateArtifacts
│
├── 1. EntityState
├── 2. RelationshipState
├── 3. EvidenceState
├── 4. ReconciliationState
├── 5. ClassificationState
├── 6. TaxState
├── 7. UncertaintyState
├── 8. RiskState
├── 9. MaterialityState
├── 10. FieldState
├── 11. MaxwellState
├── 12. InterventionState
├── 13. BookkeepingState
├── 14. StateSnapshot
├── 15. CompanyState
├── 16. BankAccountState
├── 17. BankTransactionState
├── 18. InvoiceState
├── 19. SupplierState
└── 20. StateTransition
```

---

# 11. Artifact 1: EntityState

Every canonical entity receives an `EntityState`:

```text
EntityState
├── entity_id
├── entity_type
├── existence
├── identity_confidence
├── evidence_completeness
├── verification_confidence
├── overall_confidence
├── consistency
├── freshness
├── materiality
├── uncertainty
├── risk
└── domain_state
```

All normalized confidence and state scores are bounded in $[0.0, 1.0]$.

---

# 12. Entity State Semantics

* **Existence**: Degree of belief that the entity exists as a valid bookkeeping object.
* **Identity Confidence**: Confidence in canonical entity identification.
* **Evidence Completeness**: Proportion of expected supporting evidence present.
* **Verification Confidence**: Degree to which entity claims are verified.
* **Overall Confidence**: Aggregated confidence in current state representation.
* **Consistency**: Agreement with accounting rules and connected graph neighbors.
* **Freshness**: Currency of underlying observation data.
* **Materiality**: Economic significance relative to enterprise scale.
* **Uncertainty**: Remaining ambiguity or entropy in state.
* **Risk**: Potential negative economic or regulatory consequence.

---

# 13. Artifact 2: RelationshipState

Relationships are stateful objects corresponding to ToroDB `enterprise_relationships`:

```text
RelationshipState
├── relationship_id
├── source_entity
├── target_entity
├── relationship_type
├── confidence
├── strength
├── verification
├── temporal_validity
├── monetary_compatibility
└── provenance
```

---

# 14. Artifact 3: EvidenceState

Evidence is modeled independently from supported entities, linking to ToroDB master documents (`toro_core.documents`):

```text
EvidenceState
├── evidence_id
├── source_type
├── source_reference
├── integrity
├── extraction_confidence
├── authenticity_confidence
├── completeness
├── freshness
└── supported_claims
```

---

# 15. Artifact 4: ReconciliationState

Domain-specific state artifact governing transaction matching:

```text
ReconciliationState
├── entity_id
├── reconciliation_status
├── match_confidence
├── candidate_count
├── amount_difference
├── date_difference
├── unresolved_amount
├── evidence_requirement
└── reconciliation_provenance
```

Possible statuses: `UNASSESSED`, `CANDIDATE`, `PARTIALLY_RECONCILED`, `RECONCILED`, `REJECTED`, `REOPENED`, `ESCALATED`, `HOLD`.

---

# 16. Artifact 5: ClassificationState

```text
ClassificationState
├── entity_id
├── proposed_gl_account
├── confidence
├── candidate_accounts
├── historical_support
├── policy_support
├── model_support
├── human_confirmation
└── classification_status
```

LLM outputs propose candidate GL classifications but cannot mutate state directly; proposals become state only after passing ESE validation and provenance checks.

---

# 17. Artifact 6: TaxState

```text
TaxState
├── entity_id
├── jurisdiction
├── tax_type
├── taxable_status
├── expected_rate
├── observed_rate
├── tax_amount
├── confidence
├── compliance_status
└── evidence
```

Tax logic remains modular by jurisdiction, enabling multi-country compliance (e.g. Moroccan VAT / US Sales Tax).

---

# 18. Artifact 7: UncertaintyState

Uncertainty preserves complete hypothesis distributions rather than scalar point estimates:

```text
Supplier A: 0.55
Supplier B: 0.40
Supplier C: 0.05
```

Entropy $H(X)$ is computed as:

$$H(X) = -\sum_{i} p_i \log p_i$$

```text
UncertaintyState
├── hypothesis_distribution
├── entropy
├── confidence_margin
├── unresolved_questions
├── missing_information
└── uncertainty_sources
```

---

# 19. Artifact 8: RiskState

```text
RiskState
├── financial_exposure
├── tax_exposure
├── duplicate_risk
├── fraud_signal
├── compliance_risk
├── operational_risk
├── confidence
└── risk_drivers
```

Risk is distinct from uncertainty: a transaction can be 100% certain but carry extreme financial risk.

---

# 20. Artifact 9: MaterialityState

```text
MaterialityState
├── amount
├── normalized_materiality
├── company_threshold
├── percentage_of_revenue
├── percentage_of_cash
├── tax_exposure
└── materiality_class
```

Materiality classes: `TRIVIAL`, `LOW`, `MODERATE`, `MATERIAL`, `CRITICAL`.

---

# 21. Artifact 10: FieldState

`FieldState` represents local energy and spatial condition over the PyG graph topology:

```text
FieldState
├── local_energy
├── neighborhood_energy
├── state_discontinuity
├── uncertainty_density
├── risk_density
├── materiality_density
├── temporal_change
├── spectral_features
└── field_classification
```

Local Dirichlet graph energy $S_i$ measures disagreement between node $i$ and its neighborhood $N(i)$:

$$S_i = \frac{1}{2} \sum_{j \in N(i)} w_{ij} \| x_i - x_j \|^2$$

---

# 22. Field Interpretation

A high-energy node represents a **disturbance**, not an immediate error. Disturbance signals are interpreted through accounting constraints, materiality, uncertainty, and historical baselines before triggering interventions.

---

# 23. Artifact 11: MaxwellState

`MaxwellState` determines whether a disturbance warrants intervention:

```text
MaxwellState
├── entity_or_subgraph
├── disturbance_score
├── materiality_score
├── uncertainty_score
├── risk_score
├── persistence_score
├── intervention_score
├── threshold
├── severity
├── trigger_reason
└── status
```

Intervention score formula:

$$M = f(\text{Energy}, \text{Materiality}, \text{Uncertainty}, \text{Risk}, \text{Persistence})$$

---

# 24. Maxwell Severity Levels

```text
NORMAL ────► WATCH ────► INVESTIGATE ────► INTERVENE / HOLD ────► ESCALATE
```

* **NORMAL**: State is within equilibrium; observe only.
* **WATCH**: Monitoring frequency increased.
* **INVESTIGATE**: Information-gathering Demons dispatched.
* **INTERVENE / HOLD**: Autonomous action permitted; node placed in `HOLD` if context is required.
* **ESCALATE**: Escalated to System 2 Decision Tree or human CPA.

---

# 25. Artifact 12: InterventionState

Every Demon or System 3 action produces an observable state artifact:

```text
InterventionState
├── intervention_id
├── trigger
├── target_subgraph
├── selected_demon
├── objective
├── permitted_actions
├── actions_taken
├── evidence_acquired
├── state_before
├── state_after
├── energy_before
├── energy_after
├── outcome
├── cost
├── duration
└── escalation_reason
```

---

# 26. Artifact 13: BookkeepingState

Domain-level aggregate state:

```text
BookkeepingState
├── reconciliation_health
├── evidence_health
├── classification_health
├── tax_health
├── uncertainty_health
├── risk_health
├── material_exposure
├── global_field_energy
├── active_disturbances
├── active_interventions
└── timestamp
```

Materially weighted reconciliation health:

$$H_R = \frac{\sum_i M_i R_i}{\sum_i M_i}$$

---

# 27. Artifact 14: StateSnapshot

Captures complete materialized state at point in time for auditability, replay, and evaluation:

```text
StateSnapshot
├── company_id
├── timestamp
├── graph_version
├── state_version
├── bookkeeping_state
├── active_disturbances
├── active_maxwell_events
└── metrics
```

---

# 28. Event-to-State Architecture & Feedback Loop

```text
Source ──► Event ──► ToroDB Fact Node ──► PyG HeteroData Update ──► Field Recalculation
                                                                           │
                                                                           ▼
                                                                  Maxwell Evaluation
                                                                           │
                                                    ┌──────────────────────┴──────────────────────┐
                                                    ▼                                             ▼
                                                 NORMAL                                    THRESHOLD CROSSED
                                                   │                                              │
                                                Observe                                     Enter `HOLD`
                                                                                                  │
                                                                                                  ▼
                                                                                        System 2 Decision Tree
                                                                                        (Expected Value EV)
                                                                                                  │
                                                                                                  ▼
                                                                                          System 3 Execution
                                                                                          (Demon / TAP)
                                                                                                  │
                                                                                                  ▼
                                                                                        ESE Re-Observation Loop
```

---

# 29. PyG Heterogeneous Graph Representation

PyG `HeteroData` node and edge store mappings:

```python
import torch
from torch_geometric.data import HeteroData

data = HeteroData()

# Node Stores
data['company'].x = company_features
data['bank_account'].x = bank_account_features
data['bank_transaction'].x = transaction_features
data['invoice'].x = invoice_features
data['receipt'].x = receipt_features
data['supplier'].x = supplier_features
data['customer'].x = customer_features
data['payment'].x = payment_features
data['gl_account'].x = gl_account_features
data['gl_entry'].x = gl_entry_features
data['tax_record'].x = tax_record_features
data['evidence'].x = evidence_features

# Edge Stores
data['bank_transaction', 'belongs_to', 'bank_account'].edge_index = transaction_account_edges
data['bank_transaction', 'matched_to', 'invoice'].edge_index = transaction_invoice_edges
data['bank_transaction', 'associated_with', 'supplier'].edge_index = transaction_supplier_edges
data['bank_transaction', 'classified_as', 'gl_account'].edge_index = transaction_gl_edges
data['invoice', 'issued_by', 'supplier'].edge_index = invoice_supplier_edges
data['invoice', 'settled_by', 'payment'].edge_index = invoice_payment_edges
data['invoice', 'supported_by', 'evidence'].edge_index = invoice_evidence_edges
```

---

# 30. PyG Feature Vectors

Numerical feature vector formulation for PyG tensor computations:

$$\mathbf{x}_{\text{transaction}} = \begin{bmatrix}
\text{existence} \\
\text{identity\_confidence} \\
\text{evidence\_completeness} \\
\text{verification\_confidence} \\
\text{consistency} \\
\text{freshness} \\
\text{materiality} \\
\text{uncertainty} \\
\text{risk} \\
\text{reconciliation\_confidence} \\
\text{classification\_confidence}
\end{bmatrix}$$

---

# 31. Graph Computation & Incremental Updates

State recalculations favor incremental k-hop neighborhood computations rather than full graph re-computations:

$$\text{Affected Entity} \longrightarrow \text{k-hop Neighborhood} \longrightarrow \text{PyG Local Message Passing} \longrightarrow \text{Field & Maxwell Update}$$

---

# 32. Progressive GNN Roadmap

* **Phase 1**: Deterministic rules & weighted graph aggregation.
* **Phase 2**: Neighborhood message passing and graph Fourier transforms.
* **Phase 3**: Self-supervised node & edge graph embeddings.
* **Phase 4**: GNN prediction models for link prediction and classification.
* **Phase 5**: Reinforcement learning for autonomous intervention selection.

---

# 33. Spectral Graph Processing

Graph Fourier Transform (GFT) field analysis over the normalized graph Laplacian $\mathbf{L} = \mathbf{I} - \mathbf{D}^{-1/2} \mathbf{A} \mathbf{D}^{-1/2}$ decomposes field disturbances into low-frequency (systemic structural trends) and high-frequency (localized anomalies) components.

---

# 34. State Transition Determinism & Auditing

Every state mutation generates an explicit `StateTransition` record:

```text
StateTransition
├── transition_id
├── entity_id
├── state_dimension
├── previous_value
├── new_value
├── triggering_event
├── supporting_evidence
├── computation_method
├── confidence
├── version
└── timestamp
```

---

# 35. State Provenance & Auditability

Every state metric maintains complete backwards provenance:

$$\text{Materialized State} \longrightarrow \text{State Transition} \longrightarrow \text{Trigger Event} \longrightarrow \text{ToroDB Master Document} \longrightarrow \text{Original Artifact}$$

---

# 36. ESE and LLM Boundary (IntelligenceContext Firewall)

LLMs operate as external, disposable reasoning units. They can propose hypotheses or extract document metadata, but cannot directly mutate enterprise state. Proposals must pass ESE validation, rule checks, and provenance verification before updating state.

---

# 37. ESE and Vector Search

Vector search ([`006_ase_vector_memory.sql`](file:///Users/Yankz/programming/usetoro/sql/schema/006_ase_vector_memory.sql)) provides semantic memory retrieval for candidate matching. Embeddings are inputs into state evaluation, not the state engine itself.

---

# 38. Accounting Truth Categorization

ESE strictly distinguishes five truth levels:
1. **Observed Fact** (Raw bank feed / API event);
2. **Inferred Fact** (Algorithmic / model match);
3. **Predicted Fact** (GNN / probabilistic forecast);
4. **Verified Fact** (Rule-validated claim);
5. **Human-Approved Fact** (CPA confirmed).

---

# 39. First Production ESE Loop

1. Bank transaction ingested via NATS.
2. Canonical entity created in `toro_core.enterprise_facts`.
3. Candidate invoice lookup in ToroDB graph.
4. `ReconciliationState` & local field energy $S_i$ calculated.
5. Maxwell score $M$ evaluated:
   - Below threshold $\rightarrow$ `NORMAL` (monitor).
   - Above threshold $\rightarrow$ Enter `HOLD` / dispatch Evidence Demon $\rightarrow$ Acquire proof $\rightarrow$ State updated $\rightarrow$ Maxwell clears.

---

# 40. Initial Demon Suite

Narrow autonomous Demons executed via the ASE DAG Execution Engine ([`ase_bridge_worker.go`](file:///Users/Yankz/programming/usetoro/go/internal/workers/ase_bridge_worker.go)):
* **Evidence Demon**: Fetches missing invoices/receipts.
* **Reconciliation Demon**: Executes candidate matching.
* **Classification Demon**: Resolves unclassified GL destinations.
* **Duplicate Demon**: Detects duplicate entries.
* **Verification Demon**: Runs deterministic accounting rule checks.

---

# 41. Demon Success Criteria

Demons are evaluated by energy and uncertainty reduction:

$$\Delta E = E_{\text{before}} - E_{\text{after}}$$

$$\Delta U = U_{\text{before}} - U_{\text{after}}$$

$$\Delta \text{Confidence} = C_{\text{after}} - C_{\text{before}}$$

---

# 42. Agent Churn Prevention & Cooldown

Every Maxwell event tracks retry attempts, cooldown timers, and progress metrics to prevent infinite agent spawning loops. If progress stalls, System 2 escalates to human CPAs.

---

# 43. Operational Key Performance Indicators (KPIs)

* **Reconciliation Health**: $H_R$
* **Evidence Health**: $H_E$
* **Classification Health**: $H_C$
* **Global Field Energy**: $\mathcal{E}(G)$
* **Material Exposure**: $\sum_i \text{Amount}_i \times \text{Uncertainty}_i \times \text{Risk}_i$
* **Mean Time to Equilibrium**: $\text{MTTE} = t_{\text{clear}} - t_{\text{trigger}}$
* **Energy Reduction Rate**: $\Delta E$
* **Autonomy Ratio**: $\text{AR} = \frac{\text{Autonomously Resolved}}{\text{Total Resolved}}$
* **Intervention Efficiency**: $\text{IE} = \frac{\Delta E}{\text{Compute Cost} + \text{Action Cost}}$

---

# 44. Persistence Architecture Summary

| Store Component | Substrate | Purpose |
| :--- | :--- | :--- |
| **Event Store** | NATS JetStream / PostgreSQL | Immutable event history |
| **Fact & Relationship Graph** | ToroDB (`toro_core`) | Authoritative entities & typed edges |
| **Master Document Store** | ToroDB (`toro_core.documents`) | Source PDFs, images & OCR metadata |
| **Graph Computation Substrate** | PyG (Python Microservice) | In-memory tensor calculations & GFT |
| **Vector Memory** | pgvector (`ase_vector_memory`) | Semantic situation retrieval |
| **Telemetry Store** | PostgreSQL (`llm_turn_metrics`) | Field & intervention telemetry |

---

# 45. Accounting Product Experience

Mathematical graph field energy remains an internal control mechanism. The user interface exposes human-native accounting insights (e.g. *"3 bank transactions totaling 45,000 MAD require supporting invoices. Toro matched 2 automatically."*).

---

# 46. Strategic Enterprise Evolution

The Bookkeeping State Engine is the pioneer domain instance of Toro's universal ESE architecture. As additional domain state engines (Procurement, Payroll, Tax) are deployed, they compose into the global **Enterprise State Field**.

---

# 47. Final Master State Architecture

```text
                                TORO ENTERPRISE
                                       │
                             BOOKKEEPING ESE SERVICE
                          (`services/ese-bookkeeping`)
                                       │
              ┌────────────────────────┼────────────────────────┐
              ▼                        ▼                        ▼
            Events                  Evidence                 Sources
              │                        │                        │
              └────────────────────────┼────────────────────────┘
                                       ▼
                         Authoritative Fact Nodes
                      (`toro_core.enterprise_facts`)
                                       │
                                       ▼
                       Directional Graph Relationships
                   (`toro_core.enterprise_relationships`)
                                       │
                                       ▼
                        PyG Heterogeneous Graph Engine
                            (`HeteroData` Tensors)
                                       │
                    ┌──────────────────┴──────────────────┐
                    ▼                                     ▼
             Graph Features                         Field Signals
                    │                                     │
                    └──────────────────┬──────────────────┘
                                       ▼
                                   FieldState
                                       │
                                       ▼
                                  MaxwellState
                                       │
                      ┌────────────────┴────────────────┐
                      ▼                                 ▼
                   Observe                     HOLD / Ambiguous State
                                                        │
                                                        ▼
                                              System 2 Decision Tree
                                            (Expected Value Optimization)
                                                        │
                                                        ▼
                                               System 3 Execution
                                           (Demon / TAP Performative)
                                                        │
                                                        ▼
                                             ESE Re-Observation Loop
```

The foundational operational law:

> **Events are what happened. State is what Toro believes. The graph represents how those states relate. The field represents how coherent those states are. Maxwell determines when disturbances matter. System 2 decides the optimal action. System 3 executes to reduce disturbance. The resulting events update state.**
