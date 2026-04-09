# Product Requirements Document (PRD)

## Product Name

**Zeed Autopilot (working name)**

---

## 1. Overview

Zeed Autopilot is a **context-driven financial decision engine** for small businesses and CPAs.

Instead of requiring users to search for answers (via tools like Google or LLMs), the system continuously monitors financial data and **proactively delivers actionable decisions**.

The product eliminates the need for:

* manual queries
* financial guesswork
* repetitive CPA interactions

---

## 2. Problem Statement

Small business owners repeatedly:

* search for financial answers
* misclassify transactions
* miss optimization opportunities
* rely heavily on CPAs for routine decisions

Existing solutions:

* accounting software = passive (reports, dashboards)
* LLMs = reactive (require queries)

**Gap:**
No system exists that **detects financial situations in real time and tells users what to do next.**

---

## 3. Vision

> Build a system where financial decisions are surfaced automatically, without user queries.

Long-term:

* No search bar
* No dashboards as primary interface
* Only **events → decisions → actions**

---

## 4. Target Users

### Primary

* Small business owners (1–50 employees)
* Freelancers / solo operators

### Secondary

* CPAs and small accounting firms (3–10 employees)

---

## 5. Core Value Proposition

* “Never Google a financial question again”
* “Your accountant, but always-on and automated”
* “Decisions delivered at the exact moment they matter”

---

## 6. Key Concepts

### 6.1 Event

A real-time signal from the system:

* new transaction
* balance change
* upcoming deadline

### 6.2 Decision Engine

Processes events into actionable recommendations.

### 6.3 Decision Packet

Atomic unit of output.

Structure:

* Context (what happened)
* Recommendation (what to do)
* Confidence score
* Optional explanation
* Action buttons

---

## 7. Core Features (MVP)

### 7.1 Transaction Monitoring

* Real-time ingestion (bank APIs, manual upload)
* Normalize and categorize transactions

---

### 7.2 Event Detection Engine

Trigger conditions:

* Large or unusual transaction
* Category ambiguity
* Cash flow threshold breach
* Recurring payment anomaly

---

### 7.3 Decision Engine (v1)

Rules + AI hybrid:

* Rule-based logic for deterministic cases
* LLM-assisted reasoning for ambiguous cases (via APIs like OpenAI)

---

### 7.4 Decision Feed (Primary Interface)

A chronological stream of decision packets.

Each item includes:

* Title (clear action statement)
* Brief context
* Action buttons:

  * Approve
  * Modify
  * Ignore

---

### 7.5 CPA Oversight Layer

* CPAs can:

  * review decisions
  * override recommendations
  * set rules

* “Escalation mode”:

  * uncertain decisions routed to CPA

---

### 7.6 Learning System

* Track user actions:

  * approvals
  * edits
  * rejections

* Update:

  * categorization rules
  * decision confidence

---

## 8. User Flows

### 8.1 Transaction → Decision

1. Transaction detected
2. Event triggered
3. Decision engine processes
4. Decision packet generated
5. User notified

---

### 8.2 User Action Loop

1. User receives decision

2. User selects:

   * Approve
   * Modify
   * Ignore

3. System updates:

   * internal model
   * future decisions

---

### 8.3 CPA Intervention

1. Decision confidence below threshold
2. Routed to CPA dashboard
3. CPA resolves
4. System learns from resolution

---

## 9. Non-Goals (MVP)

* Full accounting suite replacement
* Payroll processing
* Tax filing automation
* Multi-country compliance

---

## 10. Technical Architecture

### 10.1 Backend

* Django (core API)
* Django Channels (real-time updates)
* Celery (async tasks)

---

### 10.2 Event Pipeline

* Event queue (Redis / Kafka optional later)
* Event processors:

  * transaction analyzer
  * anomaly detector

---

### 10.3 Decision Engine

Layered approach:

1. Rule engine (deterministic logic)
2. AI orchestration layer (external APIs like Anthropic or OpenAI)
3. Confidence scoring system

---

### 10.4 Data Model (Simplified)

* User
* Business
* Transaction
* Event
* Decision
* DecisionAction
* CPAReview

---

### 10.5 Real-Time Layer

* WebSocket feed for decision stream
* Push notifications (mobile)

---

## 11. Metrics (Critical)

### Activation

* % of users receiving first decision within 24h

### Engagement

* Decisions acted on per user per week

### Accuracy

* % of decisions approved without modification

### CPA Load Reduction

* Reduction in manual queries to CPAs

### Retention

* Weekly active users

---

## 12. Monetization Strategy

### Phase 1

* Free for small businesses
* Paid tier for CPAs

### Phase 2

* Per-decision optimization fee
* Premium automation features

### Phase 3

* Transaction-based revenue:

  * lending
  * payments
  * financial products

---

## 13. Risks

### Trust Risk

Users may hesitate to trust automated decisions.

Mitigation:

* transparent explanations
* CPA oversight

---

### Data Integration Risk

Bank APIs unreliable or fragmented.

Mitigation:

* manual upload fallback
* multi-provider integration

---

### Accuracy Risk

Incorrect recommendations could have financial impact.

Mitigation:

* confidence thresholds
* escalation to CPA

---

## 14. Milestones

### Month 1–2

* Core models
* Transaction ingestion
* Basic rule engine

### Month 3

* Decision feed
* Notification system

### Month 4

* CPA dashboard
* Feedback loop

### Month 5–6

* AI-assisted decision layer
* Optimization and scaling

---

## 15. Long-Term Expansion

* Fully automated bookkeeping
* Predictive financial planning
* Autonomous tax optimization
* Financial “autopilot mode”

---

## Final Principle

> The product succeeds when users stop asking questions.
