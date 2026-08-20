# Product Requirements Document (PRD)

## 3. Communication System (Game Theory Reality Negotiation & Engagement Layer)

**Document Reference:** `docs/prds/harness_10x/3_communication_system.md`  
**Status:** Approved Master Architecture v2.0  
**Owner:** Outbound Communication & Game Theory Engineering  
**Subsystem:** System 3 of the 5 Core Harness Subsystems  
**Target Architecture:** Toro Enterprise Runtime (`tap/cmd/test_holding_email`, NATS JetStream, TAP Performatives, Game Theoretical Mechanism Design)

---

# 1. Executive Summary & Core Philosophy

The **Communication System (System 3)** is Toro's smart outward reality negotiation and engagement engine.

### Core Philosophy: Multi-Agent Game Theoretical Mechanism Design
System 3 models both **AI agents** and **human recipients** (CPA clients, business owners, vendors, accountants) as rational, utility-maximizing intelligent agents.

```text
                                  HOLD STATE / AMBIGUITY
                                             │
                                             ▼
                                    COMMUNICATION SYSTEM
                              (`3_communication_system.md`)
                                             │
                             Game-Theoretic Mechanism Design
                             (Payoff Matrix & Tax Thresholds)
                                             │
                         ┌───────────────────┴───────────────────┐
                         ▼                                       ▼
             LOW-STAKES / LOW-VALUE                  HIGH-STAKES / HIGH-VALUE
          Below Tax Audit Thresholds              Above Tax Audit Thresholds
             (IRS $75 / DGI 500 MAD)                (Material Financial Impact)
                         │                                       │
                         ▼                                       ▼
             Cooperative Signaling Game             Sequential Signaling Game
             - 1-Click Confirmation                 - Reciprocity & Proof Incentive
             - ZERO PDF Upload Demanded             - Single-Touch Document Capture
                         │                                       │
                         └───────────────────┬───────────────────┘
                                             │
                                             ▼
                                  TAP Performative Stream
                                   (NATS JetStream Ingress)
                                             │
                                             ▼
                                    90%+ Human Engagement
                                     & Fast State Clearing
```

---

# 2. The Naive Communication Anti-Pattern vs. Game-Theoretic Smart Communication

### 2.1 The Naive Communication System (Anti-Pattern)
In traditional accounting automation systems, whenever a transaction enters a `HOLD` state (e.g., unlinked bank movement or unclassified vendor expense), the system naively sends a blunt, high-friction template email:

> *"Please log into the portal, search your records, and upload a valid PDF receipt/invoice for transaction #402 ($35.00 MAD)."*

#### Why the Naive Model Fails:
The human recipient evaluates the expected utility of responding vs. ignoring:

$$U(\text{ignore}) > U(\text{search \& upload PDF})$$

Because searching for a PDF invoice for a $35 expense incurs high cognitive and operational friction ($Cost > Benefit$), the rational human recipient ignores the email. Response rates stall at **20–30%**, leaving bookkeeping state permanently frozen in `HOLD`.

---

### 2.2 The Game-Theoretic Communication System (System 3)
System 3 eliminates unnecessary friction by computing dynamic **Payoff Matrices** derived from transaction materiality, tax audit thresholds, and human friction costs.

#### Key Principle:
> **Demands for evidence must match the game-theoretical equilibrium of the recipient.**

---

# 3. Game Theoretical Taxonomy & Communication Strategies

System 3 dynamically selects from three primary game theoretical communication strategies:

## 3.1 Game Strategy 1: Low-Stakes Micro-Confirmation (Cooperative Signaling Game)
* **Trigger Condition**: Transaction amount is below statutory audit thresholds (e.g. IRS $75 USD threshold or Moroccan DGI 500 MAD threshold) and risk density is low.
* **Mechanism**: Zero-friction binary confirmation. **Never demand PDF document uploads for low-stakes transactions.**
* **User Experience**: Single-touch email/WhatsApp button:

> *"Toro noticed a $35.00 MAD expense at 'Morocco Office Supplies'. Can you confirm this was for Office Equipment? [YES, CONFIRM] [NO, CATEGORIZE DIFFERENTLY]"*

* **Payoff Structure**: User friction cost is near zero ($< 2$ seconds). Human response rate increases to **90%+**.

---

## 3.2 Game Strategy 2: High-Stakes Audit Proof (Sequential Signaling & Reciprocity Game)
* **Trigger Condition**: Transaction amount exceeds audit threshold, affects tax deductions materially, or involves an unverified counterparty.
* **Mechanism**: Asymmetric payoff incentive structure. System 3 pre-extracts supplier context from ToroDB ([`toro_core.enterprise_facts`](file:///Users/Yankz/programming/usetoro/sql/schema/040_create_toro_core_knowledge_system.sql#L6-L16)), highlights potential tax savings, and permits single-tap camera/photo capture via WhatsApp or Email.
* **User Experience**:

> *"Confirming Invoice for Vendor ABC ($45,000 MAD) will unlock $6,750 MAD in deductible VAT savings. Tap below to attach a receipt snapshot:"*

---

## 3.3 Game Strategy 3: Information Asymmetry Mitigation (Bargaining & Candidate Selection Game)
* **Trigger Condition**: Missing vendor identity or ambiguous GL account destination.
* **Mechanism**: System 3 queries ToroDB graph heuristics to pre-filter the top 3 candidate GL accounts or supplier profiles, reducing open-ended search friction to candidate selection.
* **User Experience**:

> *"Was this $450 MAD transaction with TechCorp for: [1. Software Subscriptions] [2. IT Hardware] [3. Consulting Services]?"*

---

# 4. Codebase & Protocol Integration

The Communication System interfaces directly with Toro's core execution and database layers:

```text
                          SYSTEM 2 DECISION ENGINE
                        (`2_decision_system.md`)
                                   │
                     Selected Action EV Optimization
                                   │
                                   ▼
                         COMMUNICATION SYSTEM
                    (`3_communication_system.md`)
                                   │
          ┌────────────────────────┼────────────────────────┐
          ▼                        ▼                        ▼
  Holding Session DB      TAP Performative Engine   NATS JetStream Ingress
`013_ase_session_email_holds.sql` `test_holding_email` `ese.bookkeeping.event.*`
```

### 4.1 Holding Session Persistence
Persists active holding outreach sessions in PostgreSQL ([`013_ase_session_email_holds.sql`](file:///Users/Yankz/programming/usetoro/sql/schema/013_ase_session_email_holds.sql)):
* `session_id` (UUID): Primary key.
* `realm_id` & `user_id`: Tenant and client references.
* `hold_reason`: Structural reason for hold state.
* `communication_game`: Selected game strategy (`MICRO_CONFIRMATION`, `HIGH_STAKES_PROOF`, `CANDIDATE_SELECTION`).
* `payoff_matrix`: Configured friction parameters.

### 4.2 TAP Protocol Performative Messaging
Outbound messages dispatch using **Toro Agent Protocol (TAP)** performatives over NATS JetStream:
* `CFP` (Call For Proposal): System 3 requests confirmation or context.
* `PROPOSE`: System 3 presents pre-filtered candidate options.
* `ACCEPT_PROPOSAL` / `INFORM`: Recipient responds with a 1-click decision.
* `PROOF`: Recipient attaches document evidence when required.

Reference implementation: [`test_holding_email/main.go`](file:///Users/Yankz/programming/usetoro/tap/cmd/test_holding_email/main.go) and Action Providers ([`action_provider_workers.go`](file:///Users/Yankz/programming/usetoro/go/internal/workers/action_provider_workers.go)).

### 4.3 State Resolution Hand-off
When the recipient responds, [`ase_resolution_worker.go`](file:///Users/Yankz/programming/usetoro/go/internal/workers/ase_resolution_worker.go) processes the incoming payload, clears the `HOLD` state, updates ToroDB facts, and resumes DAG execution.

---

# 5. Key Operational KPIs

| KPI Metric | Formula / Definition | Target Threshold |
| :--- | :--- | :--- |
| **Human Engagement Rate** | $\text{ER} = \frac{\text{Responded Sessions}}{\text{Total Holding Sessions}}$ | $\ge 90\%$ |
| **Mean Time to Resolution (MTTR)** | Average hours from outreach to state clearance | $< 2.0$ Hours |
| **Friction Index** | $\text{FI} = \frac{\text{1-Click Confirmations}}{\text{Document Upload Demands}}$ | $\ge 4.0$ |
| **Audit Compliance Rate** | Material transactions possessing required proof | $100\%$ |
