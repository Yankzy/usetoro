# Product Requirements Document (PRD)

## 7. Obsidian-Style Interactive Graph UI & Frontend Visualization

**Document Reference:** `docs/prds/harness_10x/7_obsidian_graph_ui.md`  
**Status:** Approved Specification v1.0  
**Owner:** Frontend & User Experience Engineering  
**Subsystem:** Frontend & Visualization Layer

---

# 1. Executive Summary & Mental Model

The **Obsidian-Style Graph UI** is the primary visual interface for human managers, auditors, and engineers to observe the Enterprise State Engine.

### Mental Model: Obsidian Graph View
Just as Obsidian renders notes (`.md` files) and wiki-links (`[[Link]]`) as an interactive force-directed graph, the ESE Frontend renders enterprise entities (`enterprise_facts`) and relations (`enterprise_relationships`) as a live interactive network.

---

# 2. Key UI Components & Visualizations

```
+-----------------------------------------------------------------------------------+
|  [Search: Supplier X]  [Depth: 2 Hops]  [Filter: AP / AR]  [Entropy Threshold: 0.5] |
+-----------------------------------------------------------------------------------+
|                                                                                   |
|                   (Invoice #284) ──🟢── (PO #102)                                  |
|                         │                                                         |
|                         🔴 ISSUED_BY                                              |
|                         │                                                         |
|                  🔴 (Supplier ABC) ──🔴── (Bank Account XYZ)                       |
|                         │                                                         |
|                         🟡 APPROVED_BY                                            |
|                         │                                                         |
|                   🟡 (Employee Z)                                                 |
|                                                                                   |
+-----------------------------------------------------------------------------------+
|  SELECTED NODE: Supplier ABC                                                       |
|  • Entropy: 0.82 (HIGH RISK)  • Confidence: 18%  • Evidence Completeness: 45%      |
|  • Trigger Reason: Recent Unverified Bank Details Change                          |
+-----------------------------------------------------------------------------------+
```

## 2.1 Visual Risk Heatmap Encoding
* **🟢 Green Nodes / Links**: Low Shannon Entropy ($C \ge 0.98$). Safe, automated execution.
* **🟡 Yellow Nodes / Links**: Moderate Shannon Entropy. Supporting evidence or verification required.
* **🔴 Red Nodes / Links**: High Shannon Entropy Peak. Accumulating operational risk, fraud alert, or unverified changes.

---

# 3. API Contract (`/api/v1/ese/graph`)

The UI queries the backend via REST or WebSocket streaming:

```http
GET /api/v1/ese/graph?realm_id=r_toro&fact_uri=fact:accounting:invoice:284&depth=2
```

### JSON Response Specification
```json
{
  "nodes": [
    { "id": "inv_284", "label": "Invoice #284", "type": "invoice", "entropy": 0.08, "status": "HEALTHY" },
    { "id": "supp_abc", "label": "Supplier ABC", "type": "supplier", "entropy": 0.82, "status": "HIGH_RISK" }
  ],
  "edges": [
    { "from": "inv_284", "to": "supp_abc", "relation": "ISSUED_BY", "weight": 1.0 }
  ]
}
```
