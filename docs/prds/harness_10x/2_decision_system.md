# Product Requirements Document (PRD)

## 2. Decision System (Action Economics)

**Document Reference:** `docs/prds/harness_10x/2_decision_system.md`  
**Status:** Approved Specification v1.0  
**Owner:** Decision Theory & Optimization Engineering  
**Subsystem:** System 2 of the 5 Core Harness Subsystems

---

# 1. Executive Summary & Core Responsibility

The **Decision System** is the rational optimization brain of the harness. 

### Core Responsibility
Given the current state published by the Enterprise Awareness System, **which action yields the highest Expected Value ($EV$)?**

### Strict Decoupling Rule
The Decision System contains **zero telemetry gathering code** and **zero workflow execution code**. It accepts the current state vector and a set of candidate recovery/execution actions, returning the action that maximizes mathematical utility.

---

# 2. Expected Value ($EV$) & Action Cost Matrix

The Decision System evaluates candidate actions using the Expected Value equation:

$$EV(a) = \left( P(S \mid a) \times ExpectedIG(a) \right) - Cost_{\text{dynamic}}(a)$$

Where:
* $P(S \mid a)$: Historical probability of success for action $a$.
* $ExpectedIG(a)$: Expected Information Gain (Shannon entropy reduction).
* $Cost_{\text{dynamic}}(a)$: Dynamic economic cost modulated by the published `EnterpriseStateVector`.

---

# 3. Dynamic Cost Modulation by Enterprise State Vector

The Decision System receives the `EnterpriseStateVector` published by ESE:

```json
{
  "automation_level": 0.92,
  "evidence_threshold": 0.87,
  "verification_depth": 2,
  "human_review_bias": 0.08,
  "external_validation_bias": 0.31,
  "retry_budget": 4,
  "simulation_depth": 2
}
```

### Action Cost Landscape Shift

| Candidate Action | Base Cost | Cost in Low Entropy State | Cost in High Entropy State |
|---|:---:|:---:|:---:|
| **Continue Automatically** | `1.0` | **`1.0`** (Selected) | `200.0` (Penalized) |
| **Search Situation Vector Memory** | `2.0` | `5.0` | **`2.0`** (Recommended) |
| **Ask Supplier via TAP** | `5.0` | `100.0` | **`4.0`** (Recommended) |
| **Escalate to Human CPA** | `20.0` | `500.0` | **`12.0`** (Viable) |

Without changing DAG workflow code, the Decision System shifts action choices dynamically based on enterprise state economics.
