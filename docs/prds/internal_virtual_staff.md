# Product Requirement Document (PRD)

## Module: Internal Virtual Growth Staff (VGS) Core Engine

**Memo No.:** 0001A

**System Target:** Native Platform Onboarding & First 100 ProAdvisor Acquisitions

---

## 1. Document Overview & Strategic Mandate

This document establishes the functional and technical requirements for the platform's internal **Virtual Growth Staff (VGS)**. Rather than relying on standard external marketing tech or human Business Development Representatives (BDRs), the platform will dogfood its own architectural primitives—specifically **NATS JetStream**, type-safe **Directed Acyclic Graphs (DAGs)**, and layout-agnostic parsing modules—to identify, engage, track, and onboard our first 100 QuickBooks ProAdvisors.

### 1.1 Core Objectives

* **Zero-Overhead Scale:** Execute a hyper-targeted outreach and conversion engine with zero internal human sales staff.
* **Contextual Conversion Execution:** Apply the platform's foundational secret—**the eradication of generic spam in favor of surgical, localized economic utility**—to prove the capability of our autonomous agents.
* **Infrastructure Stress-Testing:** Expose latent architectural boundaries, state latencies, or API edge cases within our platform by acting as our own primary high-throughput customer.

---

## 2. Core Functional Architecture (The Virtual Staff Directory)

The growth organization consists of three discrete virtual employees executing concurrent, multi-instance task loops. Every target prospect triggers a unique, stateful execution instance across the NATS backplane.

```
[Agent 1: Prospecting Scout] ──► [Agent 2: Contextual Outbound Builder] ──► [Agent 3: Conversion Sandbox Conductor]
   (Scrapes, enriches, matches, segments)                   (Crafts Personalization Math)               (Tracks Interaction Telemetry)

```

### 2.1 Agent 1: The Prospecting Scout

* **Trigger:** Automated cron initialization or batch operator seeds.
* **Functional Pipeline (DAG Instance):**
1. **Footprint Extraction:** Scrapes the QuickBooks ProAdvisor Directory, public registry APIs, and professional networks to ingest raw profile strings.
2. **Firmographic Classification:** Passes the un-redacted profile text through the layout-agnostic parser to normalize names, location densities, and reported firm sizing metrics.
3. **Semantic Focus Mapping:** Evaluates the firm’s public orientation. It isolates high-value keywords signifying structural bottlenecks (e.g., *"specializing in high-volume inventory retail,"* *"e-commerce bookkeeping solutions"*).


* **Output:** Publishes a highly structured, scored prospect profile to the secure NATS topic: `growth.leads.qualified`.

### 2.2 Agent 2: The Contextual Outbound Builder

* **Trigger:** Subscribes to `growth.leads.qualified`.
* **Functional Pipeline (DAG Instance):**
1. **Deficit Matrix Hydration:** Consumes the scout payload and extracts the firm’s estimated transaction volume based on identified client verticals.
2. **Economic Loss Simulation:** Executes a localized mathematical estimation tracking structural resource drain. It computes the average hours wasted ($T_{\text{wasted}}$) by the target firm during manual monthly document collection epochs:



$$T_{\text{wasted}} = N_{\text{clients}} \times \left( \alpha \cdot \mu_{\text{receipts}} + \beta \cdot \mu_{\text{invoices}} \right)$$

Where $N_{\text{clients}}$ represents estimated client count, $\mu$ represents monthly average document frequencies per vertical, and $\alpha, \beta$ are localized time-coefficient weights of manual matching friction.

* **Message Customization Synthesis:** Packages this structural calculation into a hyper-personalized outreach vector. The hook skips standard feature descriptions and leads exclusively with verified, pre-simulated mathematical solutions.
* **Output:** Stages outbound engagement payloads and provisions an isolated, dedicated testing sandbox matching the target's identity.

### 2.3 Agent 3: The Conversion Sandbox Conductor

* **Trigger:** Instantiated the moment a target clicks their personalized access route, streaming events to `growth.sandbox.events`.
* **Functional Pipeline (DAG Instance):**
1. **Real-Time Telemetry Tracking:** Listens to the sandbox user interaction stream. It tracks click paths, page latency, and data-drop events.
2. **Interaction Scoring:** Evaluates interaction depth against a type-safe **Aha! Moment Threshold**. If a user successfully uploads a messy sample document and watches our parsing engine structure it in under 10 seconds, the engine scores high-intent affinity.
3. **Founder Escalation Interrupt:** Triggers an immediate execution switch if high affinity or integration page friction is recorded. It pushes an interactive card straight to the founder's email to initiate human close mechanics.



---

## 3. Data & Communication Protocol Specifications

To maintain type-safe execution states across our asynchronous NATS backend, the VGS engine uses strict protocol data schemas.

### 3.1 Global Growth Pipeline Schema (Go Struct)

```go
package internalgrowth

import (
	"encoding/json"
	"time"
)

type TargetStatus string

const (
	StatusDiscovered  TargetStatus = "DISCOVERED"
	StatusContacted   TargetStatus = "CONTACTED"
	StatusActiveDemo  TargetStatus = "ACTIVE_DEMO"
	StatusEscalated   TargetStatus = "ESCALATED"
	StatusConverted   TargetStatus = "CONVERTED"
)

type GrowthPipelineInstance struct {
	ProspectID           string       `json:"prospect_id"`
	ProAdvisorName       string       `json:"proadvisor_name"`
	FirmName             string       `json:"firm_name"`
	TargetVertical       string       `json:"target_vertical"`
	CurrentStatus        TargetStatus `json:"current_status"`
	AlmanacPublicKey     string       `json:"almanac_public_key"`
	
	// Operational Metric Layer
	ClientCountEstimate  int64        `json:"client_count_estimate"`
	CalculatedLeakingHrs float64      `json:"calculated_leaking_hrs"`
	SandboxInstanceURL   string       `json:"sandbox_instance_url"`
	
	// Telemetry Affinity Matrices
	InteractionCount     int          `json:"interaction_count"`
	AhaMomentTriggered   bool         `json:"aha_moment_triggered"`
	LastEventTimestamp   int64        `json:"last_event_timestamp"`
	
	// Routing Control State
	ActiveDAGInstanceID  string       `json:"active_dag_instance_id"`
	CurrentNodeIndex     int          `json:"current_node_index"`
}

```

### 3.2 Declarative Instance DAG Blueprint Definition (JSON Payload)

Every spawned growth instance clones this execution layout to handle the lifecycle orchestration from prospect discovery to close:

```json
{
  "blueprint_id": "vgs_proadvisor_acquisition_v1",
  "version": "2026.07.13",
  "nodes": [
    {
      "id": "node_101_profile_scrape",
      "topic": "growth.engine.scout.ingest",
      "timeout_ms": 30000,
      "retry_policy": { "attempts": 3, "backoff_factor": 2 }
    },
    {
      "id": "node_102_deficit_modeling",
      "topic": "growth.engine.builder.simulate",
      "timeout_ms": 15000,
      "retry_policy": { "attempts": 2, "backoff_factor": 1.5 }
    },
    {
      "id": "node_103_outbound_dispatch",
      "topic": "growth.engine.builder.dispatch",
      "timeout_ms": 60000,
      "retry_policy": { "attempts": 1, "backoff_factor": 1 }
    },
    {
      "id": "node_104_sandbox_telemetry",
      "topic": "growth.engine.conductor.track",
      "timeout_ms": 0,
      "retry_policy": { "attempts": 0, "backoff_factor": 0 }
    }
  ],
  "edges": [
    { "from": "node_101_profile_scrape", "to": "node_102_deficit_modeling" },
    { "from": "node_102_deficit_modeling", "to": "node_103_outbound_dispatch" },
    { "from": "node_103_outbound_dispatch", "to": "node_104_sandbox_telemetry" }
  ]
}

```

---

## 4. Operational Guardrails & Logic Boundaries

To ensure the platform protects its strategic orientation during the customer acquisition push, the engine operates within three strict logic barriers:

### 4.1 The Personalization Priority Metric

The engine blocks any outreach messaging where the calculated `CalculatedLeakingHrs` score falls below a threshold of **15 hours saved per month**. If a target firm does not display a baseline profile density indicating high-volume tracking problems, the instance shifts immediately to `StatusHold`. This ensures our system never acts as a low-context, high-frequency spam generator.

### 4.2 The Founder Escalation Framework

Agent 3 enforces an immediate interrupt loop to balance automated scale with human precision. The moment a ProAdvisor matches the escalation rule parameters:

```go
if pipeline.InteractionCount >= 5 && pipeline.AhaMomentTriggered && !pipeline.CurrentStatus == StatusConverted {
    // Re-route NATS traffic to prioritize real-time manual dashboard push
    TriggerFounderHandoff(pipeline)
}

```

The system halts automated messaging sequences and routes full backend diagnostic controls directly to the founder's desktop interface to finalize enterprise onboarding negotiations.

### 4.3 Total Execution Isolation

All tracking state nodes, pipeline histories, and target profile metrics logged by the VGS engine exist strictly within an isolated internal enterprise workspace tenant. It uses the exact data access security, token checking parameters, and cryptographic signature verification boundaries that our future corporate clients will rely on, proving the robustness of our architecture from day one.