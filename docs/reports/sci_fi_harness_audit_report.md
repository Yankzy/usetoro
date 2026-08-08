# Comprehensive Architecture Audit: TAP & ASE

**Document Reference:** `docs/reports/sci_fi_harness_audit_report.md`  
**Date:** August 6, 2026  
**Audited Systems:** [TAP (Toro Agent Protocol)](file:///Users/Yankz/programming/usetoro/tap) & [ASE (Autonomous Semantic Engine)](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase)  
**Architectural Framework:** The 5 Core Subsystems of an Enterprise Agentic Harness

---

## 🏛️ EXECUTIVE SUMMARY & RESTRUCTURED ARCHITECTURE

Previous iterations treated harness capabilities as a flat list of 10 sci-fi properties. That approach blurred **epistemological principles** (*"Never Guesses"*), **implementation techniques** (*"Graph Fourier Transform"*, *"pgvector"*), and **subsystem mechanics** (*"Reversible Work"*).

This updated audit reorganizes the TAP and ASE architecture into **5 Core Subsystems**:

```
+-----------------------------------------------------------------------------------+
|                         5. Enterprise Awareness System                            |
|        (Enterprise State Engine, Global State, Telemetry, Risk Pressure)          |
+----------------------------------------+------------------------------------------+
                                         |
                                         ▼
+----------------------------------------+------------------------------------------+
|                          2. Decision System                                       |
|        (Expected Value EV, Information Gain IG, Dynamic Action Costing)           |
+----------------------------------------+------------------------------------------+
                                         |
                                         ▼
+----------------------------------------+------------------------------------------+
|                          3. Execution System                                      |
|        (Workflow Orchestration, Reversibility, Negotiation, Verification)         |
+-----------------------------------------------------------------------------------+

=====================================================================================
                      SUPPORTING FOUNDATIONAL SUBSYSTEMS
=====================================================================================
  1. Knowledge System (Epistemology)          4. Learning System (Adaptation)
  • Fact Coordinates & Relational Graph       • Local Feedback Compounding & Rewinding
  • Situation Retrieval (over Documents)      • Global Network-Wide Anomaly Immunization
  • Shannon Entropy H & Provenance
```

---

## 📊 SUMMARY SCORECARD: THE 5 CORE SUBSYSTEMS

| Subsystem | Core Capability / Principle | Score | Current Code Implementation | Master PRD Link (`docs/prds/harness_10x/`) |
|---|---|:---: |---|---|
| **1. Knowledge System** | **Epistemology**: Never guesses; fact coordinates; native AlloyDB property graph; understands situations over documents. | **5.7 / 10** | [math.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/math.go#L7-L94) ($H$ & $C \ge 0.98$), [vector_store.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/vector_store.go) (ScaNN ANN). | [1_knowledge_system.md](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/1_knowledge_system.md) |
| **2. Decision System** | **Action Economics**: Expected Value ($EV$), Information Gain ($IG$), Dynamic Action Costing. | **7.5 / 10** | [policy_engine.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/policy_engine.go), [math.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/math.go#L96-L99) ($EV = P(S\mid a) \cdot IG - C$). | [2_decision_system.md](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/2_decision_system.md) |
| **3. Execution System** | **Durable Orchestration**: Reversibility, state replay, reality negotiation (TAP speech acts). | **6.5 / 10** | [node.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/node.go) (`ExecutionTrace`), [action_provider_workers.go](file:///Users/Yankz/programming/usetoro/go/internal/workers/action_provider_workers.go). | [3_execution_system.md](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/3_execution_system.md) |
| **4. Learning System** | **Adaptation**: Local feedback rewinding & global pattern propagation across network. | **3.5 / 10** | [backtracking.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/backtracking.go) (`AutomatedBacktrackAndResume`). Network sharing is 0% due to `realm_id` isolation. | [4_global_network_immune_system.md](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/4_global_network_immune_system.md), [5_evolutionary_route_optimizer.md](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/5_evolutionary_route_optimizer.md) |
| **5. Enterprise Awareness** | **Autonomic World Model**: Global state, risk pressure, entropy propagation, forecasting. | **3.0 / 10** | [telemetry.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/telemetry.go) (per-node metrics). Global state engine unbuilt. | [6_enterprise_state_engine.md](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/6_enterprise_state_engine.md), [GFT.md](file:///Users/Yankz/programming/usetoro/docs/prds/GFT.md) |

**Overall Architectural Maturity Score: 5.2 / 10**

---

## 🔍 SUBSYSTEM AUDIT & DECOUPLED RESPONSIBILITIES

### Subsystem 1: Knowledge System (Epistemology)
* **Goal**: How does the harness represent and verify reality?
* **Current Implementation**:
  * **Shannon Entropy & Guardrails**: In [math.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/math.go#L7-L94) and [node.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/node.go#L23-L27), Shannon entropy $H$ and unified confidence $C$ are mathematically enforced. If $C < 0.98$, the system refuses to sync choices downstream.
  * **Situation Retrieval vs. Document Retrieval**: In [vector_store.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/vector_store.go), AlloyDB ScaNN ANN indexes execute cosine similarity over memory rules and past transaction situations.
  * **Graph Architecture Decision**: As specified in Section 2 of [1_knowledge_system.md](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/1_knowledge_system.md), the system rejects Neo4j and pure in-memory graphs for Phase 1. It implements a **Native AlloyDB/PostgreSQL Property Graph** (`toro_core.enterprise_facts` & `toro_core.enterprise_relationships`) to preserve single-source-of-truth ACID guarantees and zero-infra overhead for shallow ($1 \rightarrow 3$ hop) operational traversals.
* **Architectural Refinement**: Reframe *"Memory Becomes Geometric"* to the timeless principle **"The harness understands situations, not documents."** Vector embeddings (`pgvector`/ScaNN) are recognized as one implementation technique for situation retrieval, not the core capability itself.

---

### Subsystem 2: Decision System
* **Goal**: Given the current state, which action yields the highest Expected Value?
* **Current Implementation**:
  * **Expected Value Math**: Implements $EV = (P(S \mid a) \cdot ExpectedIG(a)) - Cost(a)$ in [math.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/math.go#L96-L99) and [policy_engine.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/policy_engine.go).
  * **Cost Matrix Evaluation**: Evaluates action candidate sets (e.g. `search_document_store`, `queue_client_request`, `human_review`).
* **Strict Decoupling**: The Decision System contains **zero telemetry gathering** and **zero workflow execution code**. It purely accepts a state vector and candidate actions, returning the action with the optimal EV.

---

### Subsystem 3: Execution System
* **Goal**: How are decisions executed, verified, reversed, and negotiated with external actors?
* **Current Implementation**:
  * **State Rewinding & Tracing**: `ExecutionTrace` in [node.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/node.go#L40-L48) and `Backtrack()` in [backtracking.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/backtracking.go#L123-L179) provide selective step rewinding and RFC 6902 JSON patch auditability.
  * **Reality Negotiation**: [action_provider_workers.go](file:///Users/Yankz/programming/usetoro/go/internal/workers/action_provider_workers.go) and TAP performatives (CFP, PROPOSE, ACCEPT) handle external data acquisition over NATS.
* **Strict Decoupling**: The Execution System contains **zero decision theory math** and **zero global state modeling**. It strictly executes the action chosen by the Decision System.

---

### Subsystem 4: Learning System (Adaptation)
* **Goal**: How does the harness adapt to feedback locally and globally?
* **Current Implementation**:
  * **Local Learning**: `AutomatedBacktrackAndResume` in [backtracking.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/backtracking.go#L78-L88) extracts human correction rules (`CreateMemoryRule`) and injects them into future prompts.
* **Open Gap**: **Global Network Learning**. Multi-tenancy isolation (`realm_id`) currently prevents fraud patterns or rules learned in Tenant A from updating prior probabilities in Tenant B. A privacy-preserving zero-knowledge pattern sharing layer is required.

---

### Subsystem 5: Enterprise Awareness System (The Enterprise State Engine)
* **Goal**: Maintains a live, continuous world model of the enterprise. Answers *"What is happening right now?"*
* **Core Philosophy**:
  * **Autonomic Nervous System**: Not a management dashboard. Built primarily for consumption by the harness itself.
  * **Telemetry & Risk Fusion**: Aggregates supplier confidence, API health, entropy trends, evidence quality, and fraud pressure into an `EnterpriseStateVector`.
  * **Implementation Independence**: Graph Fourier Transform (GFT) ([GFT.md](file:///Users/Yankz/programming/usetoro/docs/prds/GFT.md)) is recognized as **one possible signal processing technique**, alongside Bayesian networks, Kalman filtering, or Graph Neural Networks (GNNs).
* **Strict Decoupling**: The Enterprise Awareness System **makes zero decisions** and **executes no work**. It purely computes and publishes operational state vectors to the Decision System.

---

## 🎯 REVISED PRD BACKLOG ROADMAP (`docs/prds/harness_10x/`)

1. [1_knowledge_system.md](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/1_knowledge_system.md) — System 1: Knowledge System
2. [2_decision_system.md](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/2_decision_system.md) — System 2: Decision System
3. [3_execution_system.md](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/3_execution_system.md) — System 3: Execution System
4. [4_global_network_immune_system.md](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/4_global_network_immune_system.md) — System 4: Global Learning System
5. [5_evolutionary_route_optimizer.md](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/5_evolutionary_route_optimizer.md) — System 4: Local Learning System
6. [6_enterprise_state_engine.md](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/6_enterprise_state_engine.md) — System 5: Enterprise Awareness System
7. [7_obsidian_graph_ui.md](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/7_obsidian_graph_ui.md) — Frontend Visualization (Obsidian Graph View UI)
