# Product Requirements Document (PRD)

## 4. Obsidian-Style Interactive Graph UI & Frontend Visualization Subsystem

**Document Reference:** `docs/prds/harness_10x/4_obsidian_graph_ui.md`  
**Status:** Approved Master Architecture v2.0  
**Owner:** Frontend, User Experience & Core Visualization Engineering  
**Subsystem:** System 5 Visualization & Human Observatory Layer  
**Target Architecture:** Toro Enterprise Frontend (`services/ese-bookkeeping` Python Service / PyTorch Geometric / ToroDB / WebGL Force Graph)

---

# 1. Executive Summary & Core Philosophy

The **Obsidian-Style Interactive Graph UI** is the primary visual observatory for human managers, CPAs, auditors, and platform engineers to observe, inspect, and debug the **Enterprise State Engine (System 5)**.

### Mental Model: Obsidian Graph View
Just as Obsidian renders personal knowledge bases (`.md` notes) and bi-directional links (`[[wiki-links]]`) as an interactive, force-directed network graph, the Toro ESE Frontend renders enterprise state entities ([`toro_core.enterprise_facts`](file:///Users/Yankz/programming/usetoro/sql/schema/040_create_toro_core_knowledge_system.sql#L6-L16)) and directional relationships ([`toro_core.enterprise_relationships`](file:///Users/Yankz/programming/usetoro/sql/schema/040_create_toro_core_knowledge_system.sql#L28-L39)) as a dynamic, living force-directed graph.

```text
                                TORO ENTERPRISE SYSTEM
                                          │
                     ┌────────────────────┴────────────────────┐
                     ▼                                         ▼
            ESE PYTHON SERVICE                        TORO DB (PostgreSQL)
       (`services/ese-bookkeeping`)                      (`toro_core`)
           PyG HeteroData Graph                       Fact & Relation Graph
                     │                                         │
                     └────────────────────┬────────────────────┘
                                          │
                            REST & WebSocket Delta Stream
                             `/api/v1/ese/graph/stream`
                                          │
                                          ▼
                         OBSIDIAN-STYLE GRAPH UI (Frontend)
                                (WebGL / 2D Canvas)
                                          │
                     ┌────────────────────┴────────────────────┐
                     ▼                                         ▼
           ACCOUNTANTS & CPAs                         CORE ENGINEERS
     - Bookkeeping Health Overview              - PyG HeteroData Debugging
     - Unverified Transaction Clusters          - GFT Spectral Signals (S_i)
     - Missing Document / Receipt Links         - Feature Tensor Inspection
     - HOLD State & Demon Resolution            - k-hop Neighborhood Extractions
```

---

# 2. Dual-Persona Value Proposition

The ESE Graph UI is not a generic administrative dashboard. It serves two distinct user personas:

### 2.1 For Accountants, CPAs & Financial Managers
* **Visual Bookkeeping Health**: Instantly identify reconciled vs. unresolved transaction subgraphs across the chart of accounts.
* **Disturbance Spotting**: High-entropy or high-risk transaction clusters illuminate in red/yellow heatmaps, highlighting missing evidence or unverified vendor account changes.
* **`HOLD` State Visibility**: Nodes paused in `HOLD` or `AMBIGUOUS` states display clear visual badges showing pending System 2 Decision Tree actions ([`2_decision_system.md`](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/2_decision_system.md)) and automated client outreach sessions ([`test_holding_email/main.go`](file:///Users/Yankz/programming/usetoro/tap/cmd/test_holding_email/main.go)).
* **Audit Provenance**: Click any entity node to inspect original source documents stored in ToroDB ([`toro_core.documents`](file:///Users/Yankz/programming/usetoro/sql/schema/040_create_toro_core_knowledge_system.sql#L51-L68)).

### 2.2 For Platform Engineers & Core Developers
* **PyG Topology Verification**: Visually inspect the heterogeneous graph topology loaded by the Python ESE microservice (`services/ese-bookkeeping`) to verify node store and edge store structures (`HeteroData`).
* **Spectral Field Energy Inspection**: Inspect local Dirichlet graph energy $S_i$ and Shannon entropy $H(X)$ values computed over graph neighborhoods.
* **Feature Vector Debugging**: Inspect the underlying 11-dimensional numerical node feature vectors $\mathbf{x}$ fed to PyG graph convolutions.
* **Neighborhood Isolation**: Filter and isolate k-hop subgraphs ($k \in [1, 5]$) to test message-passing boundaries and propagation dynamics.

---

# 3. Component Layout & Visual Interface

```text
+---------------------------------------------------------------------------------------------------+
|  [Search Node: URI / Label] [Depth: 2 Hops] [Entropy Min: 0.50] [Filter Types v] [Preset: Default] |
+---------------------------------------------------------------------------------------------------+
| PHYSICS CONTROLS  |                                                                               |
| Gravity:   [──■──] |                      (Invoice #inv_983) ──🟢── (GL #6100)                     |
| Charge:    [─■───] |                             │                                                |
| Distance:  [───■─] |                             🔴 ISSUED_BY                                     |
| Friction:  [──■──] |                             │                                                |
| Collide:   [─■───] |                      🔴 (Supplier ABC) ──🔴── (Bank Account #402)             |
|                    |                             │                                                |
| DISPLAY OPTIONS    |                             🟣 HOLD (Pending Client Email)                    |
| (x) Node Labels    |                             │                                                |
| (x) Dynamic Glow   |                      🟣 (Transaction #tx_402)                                |
| ( ) Direction Arrow|                                                                              |
+--------------------+------------------------------------------------------------------------------+
| INSPECTOR PANEL: Selected Node -> Supplier ABC (fact:bookkeeping:supplier:supp_abc)              |
| • Entity Type: supplier          • Overall Confidence: 0.18 (UNVERIFIED)  • Entropy H(X): 0.82    |
| • Dirichlet Energy S_i: 14.82    • Material Exposure: $45,000 MAD        • Maxwell Severity: HOLD   |
| • ToroDB Fact ID: 9f8a...32b     • Connected Documents: 2 (1 Invoice, 0 W-9)                      |
| • Decision Tree Action: TAP Call Performative -> Request Updated W-9 Verification                |
+---------------------------------------------------------------------------------------------------+
```

---

# 4. Color & Heatmap Visual Encoding

Graph nodes and directional links use continuous color encoding derived from ESE 20 State Artifacts ([`8_bookeeping_state.md`](file:///Users/Yankz/programming/usetoro/docs/prds/harness_10x/8_bookeeping_state.md)):

| Color / Visual State | Condition / Threshold | Semantic Meaning | Action / Behavior |
| :--- | :--- | :--- | :--- |
| **🟢 Green Node / Edge** | Confidence $C \ge 0.98$, Entropy $H(X) \le 0.10$ | Safe, reconciled, fully verified equilibrium state. | Fully automated execution; no intervention required. |
| **🟡 Yellow Node / Edge** | $0.50 \le C < 0.98$, Moderate Entropy | Unresolved candidate match or missing non-critical document. | Monitored by ESE; background Demons investigating. |
| **🔴 Red / Pulsing Node** | $C < 0.50$, High Dirichlet Energy $S_i > 10.0$ | High entropy peak, unverified counterparty, or accounting error. | Active Maxwell disturbance; Demon intervention triggered. |
| **🟣 Purple Node / Badge** | `ReconciliationStatus == 'HOLD'` or `AMBIGUOUS` | Node paused in `HOLD` state for System 2 Decision Tree resolution. | Outbound client outreach or human CPA escalation active. |
| **⚪ Gray / Dimmed Node** | Outside active k-hop filter radius | Contextual background node. | Dimmed to maintain focus on selected subgraph. |

---

# 5. Obsidian-Parity Graph Features & Physics Controls

To deliver the exact user experience loved by Obsidian users, the graph UI incorporates full physics force controls, instant search, and group styling:

### 5.1 Force Simulation Parameters
Built on a WebGL 2D/3D force-directed physics engine (`d3-force` / `force-graph` WebGL canvas):
* **Center Gravity Force**: Adjusts pull toward canvas center $[0.0, 2.0]$.
* **Repulsive Charge Strength**: Adjusts N-body node repulsion $[-1000, -10]$.
* **Link Spring Distance**: Sets equilibrium edge distance $[10px, 300px]$.
* **Friction & Velocity Dampening**: Controls particle motion stabilization $[0.1, 0.9]$.
* **Collision Radius Padding**: Prevents node overlapping $[5px, 50px]$.

### 5.2 Interactive Navigation & Filtering
* **Instant Fuzzy Node Search**: Typing in the search bar highlights matching node labels/URIs and dims non-matching nodes in real time.
* **k-Hop Depth Isolation Slider**: Dragging the hop depth slider ($k \in [1, 5]$) dynamically prunes the visible canvas to show only entities within $k$ steps of the selected focus node.
* **Entropy & Energy Range Sliders**: Filters visible nodes based on minimum Shannon entropy $H(X)$ or Dirichlet local energy $S_i$.
* **Entity Type Toggles**: Toggles visibility for individual ToroDB entity types (`company`, `bank_account`, `bank_transaction`, `invoice`, `receipt`, `payment`, `supplier`, `customer`, `gl_account`, `evidence`).
* **Maxwell Severity Filter**: Filters by severity level (`NORMAL`, `WATCH`, `INVESTIGATE`, `HOLD`, `ESCALATED`).

### 5.3 Inspector Side Panel
Selecting any node opens the detail inspector panel:
* **Canonical Metadata**: Entity URI, ToroDB `fact_id`, realm ID, namespace, and timestamp.
* **State Vector Metrics**: `existence`, `identity_confidence`, `evidence_completeness`, `verification_confidence`, `overall_confidence`, `consistency`, `freshness`, `materiality`, `uncertainty`, `risk`.
* **Field Energy & Spectral Signals**: Local Dirichlet energy $S_i$, neighborhood energy $\mathcal{E}(N(i))$, and spectral frequency classification.
* **Master Document Attachments**: Embedded preview of associated proof of purchase documents from `toro_core.documents` ([`040_create_toro_core_knowledge_system.sql`](file:///Users/Yankz/programming/usetoro/sql/schema/040_create_toro_core_knowledge_system.sql#L51-L68)).
* **Decision Tree & Demon Trace**: Displays the System 2 Expected Value equation trace ($EV = P(S \mid a) \cdot ExpectedIG(a) - Cost(a)$) and active ASE DAG Engine Demon logs ([`ase_bridge_worker.go`](file:///Users/Yankz/programming/usetoro/go/internal/workers/ase_bridge_worker.go)).

---

# 6. Backend Data Integration & API Contracts

The Frontend UI interfaces directly with the Python ESE Microservice (`services/ese-bookkeeping`) and Go API Gateway.

```text
                       OBSIDIAN GRAPH UI FRONTEND
                                   │
                ┌──────────────────┴──────────────────┐
                ▼                                     ▼
         REST Data Fetch                       WebSocket Delta Stream
    `GET /api/v1/ese/graph`                `ws://.../api/v1/ese/graph/stream`
                │                                     │
                └──────────────────┬──────────────────┘
                                   │
                                   ▼
                       ESE PYTHON SERVICE / GO GATEWAY
                                   │
                ┌──────────────────┴──────────────────┐
                ▼                                     ▼
      ToroDB Fact & Relation Graph             NATS State Events
    (`toro_core.enterprise_facts`)          (`ese.bookkeeping.state.*`)
```

## 6.1 REST Graph Snapshot API

```http
GET /api/v1/ese/graph?realm_id=r_toro&center_uri=fact:bookkeeping:supplier:supp_abc&depth=2&entropy_min=0.20
```

### JSON Response Schema
```json
{
  "realm_id": "r_toro",
  "timestamp": "2026-08-10T10:25:00Z",
  "query": {
    "center_uri": "fact:bookkeeping:supplier:supp_abc",
    "depth": 2,
    "entropy_min": 0.20
  },
  "metrics": {
    "total_nodes": 4,
    "total_edges": 3,
    "global_field_energy": 18.45,
    "active_holds": 1
  },
  "nodes": [
    {
      "id": "fact:bookkeeping:supplier:supp_abc",
      "fact_id": "9f8a4e10-32b1-4c8d-9e0a-112233445566",
      "label": "Supplier ABC",
      "entity_type": "supplier",
      "uri": "fact:bookkeeping:supplier:supp_abc",
      "entropy": 0.82,
      "dirichlet_energy": 14.82,
      "confidence": 0.18,
      "materiality": 45000.0,
      "status": "HOLD",
      "hold_reason": "Unverified counterparty bank detail change",
      "state_features": {
        "existence": 1.0,
        "identity_confidence": 0.95,
        "evidence_completeness": 0.45,
        "verification_confidence": 0.18,
        "consistency": 0.30,
        "freshness": 0.99,
        "materiality": 0.85,
        "uncertainty": 0.82,
        "risk": 0.78
      }
    },
    {
      "id": "fact:bookkeeping:transaction:tx_402",
      "fact_id": "1b2c3d4e-5f6a-7b8c-9d0e-112233445566",
      "label": "Bank Txn #402 ($45,000 MAD)",
      "entity_type": "bank_transaction",
      "uri": "fact:bookkeeping:transaction:tx_402",
      "entropy": 0.75,
      "dirichlet_energy": 11.20,
      "confidence": 0.25,
      "materiality": 45000.0,
      "status": "HOLD",
      "hold_reason": "Pending client email confirmation"
    }
  ],
  "edges": [
    {
      "id": "rel_101",
      "from": "fact:bookkeeping:transaction:tx_402",
      "to": "fact:bookkeeping:supplier:supp_abc",
      "relation_type": "ASSOCIATED_WITH",
      "weight": 0.85,
      "confidence": 0.85
    }
  ]
}
```

## 6.2 WebSocket Real-Time Delta Streaming API

```websocket
ws://toro-platform.local/api/v1/ese/graph/stream?realm_id=r_toro
```

When state changes occur in the Python ESE microservice or NATS JetStream events fire (`ese.bookkeeping.state.*`), real-time graph delta messages push to connected clients:

```json
{
  "event_type": "STATE_DELTA",
  "timestamp": "2026-08-10T10:25:05Z",
  "delta": {
    "updated_nodes": [
      {
        "id": "fact:bookkeeping:transaction:tx_402",
        "entropy": 0.05,
        "dirichlet_energy": 0.12,
        "confidence": 0.99,
        "status": "RECONCILED",
        "hold_reason": null
      }
    ],
    "added_edges": [
      {
        "id": "rel_102",
        "from": "fact:bookkeeping:transaction:tx_402",
        "to": "fact:bookkeeping:invoice:inv_983",
        "relation_type": "MATCHED_TO",
        "weight": 0.99
      }
    ]
  }
}
```

---

# 7. Performance & Rendering Benchmarks

To ensure fluid interaction even for large enterprise graph topologies:

* **Canvas Render Rate**: Mandatory **60 FPS** during force simulation and user panning/zooming over graphs containing up to **10,000 nodes** and **25,000 edges**.
* **Level of Detail (LOD) Scaling**: Automatically simplifies node rendering (hiding labels, simplifying glyphs) when zoomed out beyond threshold scale limits.
* **Spatial Quadtree Indexing**: Sub-millisecond node selection and hover collision detection using 2D quadtrees / 3D octrees.
* **WebGL Acceleration**: Uses WebGL shader rendering for high-density particle links and energy glow effects.

---

# 8. Summary of Architectural Alignment

```text
                  OBSIDIAN GRAPH UI
                          │
       ┌──────────────────┴──────────────────┐
       ▼                                     ▼
CPAs & Accountants                    Core Engineers
- Bookkeeping Health                  - PyG HeteroData Debugging
- Disturbance Spotting                - Spectral Signals S_i
- HOLD State & Demons                 - Feature Tensors & k-hop

                          │
                          ▼
            ESE PYTHON SERVICE (`services/ese-bookkeeping`)
                          │
                          ▼
             TORO DB (`toro_core`)
```

The ESE Graph UI delivers exact Obsidian-style visual graph exploration while anchoring every node and edge directly in Toro's real Python ESE microservice and ToroDB core knowledge substrate.
