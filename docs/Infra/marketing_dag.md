# Product Requirement Document (PRD)

## Module: Core Marketing Operating System & Master DAG Core Framework

---

## 1. Executive Summary & Strategic Value Proposition

The Core Marketing Operating System (CMOS) transforms traditional, manual marketing agency workflows into a decentralized, programmable execution substrate. By leveraging a state-carrying Directed Acyclic Graph (DAG) backed by NATS JetStream, the CMOS automates coordination across cross-functional agency personas: Account Managers, Software Developers, Creative Directors, Copywriters, and Media Buyers.

### 1.1 Core Features Offered to Marketing Agencies

* **The Cross-Platform Automated Ingestion Matrix:** Eliminates manual reporting collection overhead by utilizing the system's layout-agnostic parsing engine. Agencies can drop any unstructured performance report (CSV, Excel, PDF) into a client node to execute automatic processing, sanitization, and database ingestion.
* **Context-Complete Task Routing (The DAG Workspace):** Eliminates operational context searching. Workers (humans or autonomous bots) are only alerted when a node receives its complete upstream payload dependencies, presenting them with a unified interface containing exactly the tools and telemetry required to execute their specific task.
* **Programmable Pipeline Customization (Pruning & Extensions):** Provides agencies with a customizable master template. Engineering-heavy agencies can extend the graph by injecting proprietary microservice nodes via the platform's SDK, while creative boutiques can prune technical nodes seamlessly without invalidating downstream state dependencies.
* **Native Escrow Micro-Settlement Ledger:** Automates internal agency labor tracking. The platform's Token Payment Engine holds a workflow budget in escrow at the graph's instantiation, automatically releasing fractional payouts to individual internal nodes upon verified cryptographic state progression.

### 1.2 The Engine of Agency Dependency (The Addiction Vector)

Agencies become dependent on CMOS because it structurally scales their operational capacity:

* **Operational Leverage:** A single human media buyer or account manager can manage a portfolio $5\times$ larger due to automated context compilation, asset pre-checks, and background task routing.
* **Zero-Friction Integrations:** Developers bypass writing custom Web2 API connection code; they write simple, typed microservice nodes that connect natively to the NATS JetStream plane.
* **Margin Protection:** The platform eliminates human workflow errors (such as over-budget ad distribution or untracked link failures) through automated runtime guardrails.

---

## 2. The Decentralized Telemetry Data Layer Architecture

The CMOS implements an edge-splitting ingestion loop. It simultaneously updates an agency's private, isolated tenant database while extracting abstract data streams to feed the platform's system-wide macro oracle.

### 2.1 Edge-Splitting Anonymization Protocol

The SDK local runtime memory buffer acts as an air-gap validator. When an ingestion or analytics node executes, the payload splits within volatile memory. The raw client data updates the agency's private dashboards, while the telemetry branch processes the metrics through the Cognitive Enrichment Engine (CEE) anonymizer before network emission.

```
                    [Raw Node Execution State / Data Ingest]
                                       │
                                       ▼
                       [SDK Local Memory Runtime Buffer]
                                      / \
                                     /   \
          (Branch A: Private Tenant)       (Branch B: Public Network Protocol)
                     │                                     │
         [Agency Client Database]               [CEE Anonymizer Node]
       (Retains PII, Brand Names,              (Vaporizes Identities, IDs,
         Creative Copy, Tokens)                  Specific Geo Strings)
                                                           │
                                                           ▼
                                            [Telemetry Metadata Stream]
                                              (Emitted to NATS Backplane)

```

### 2.2 Telemetry Mapping Matrix

To ensure compliance and network safety, the transformation from raw private data to public network token arrays follows a strict one-way conversion:

| Raw Private Data Field | CEE Anonymization Action | Emitted Network Telemetry Field |
| --- | --- | --- |
| `ClientBrandName: "AlphaApparel"` | Hash + Dictionary Mapping | `IndustrySectorID: 14` (D2C Retail) |
| `CampaignName: "US_Summer_Sale_2026"` | Structural Classifier Extraction | `VectorType: RETARGETING_OUTCURVE` |
| `ExactSpend: $14,251.42` | Continuous Normalization Log Bracket | `ValueTier: 06` ($10k–$25k Magnitude) |
| `ConversionCount: 644` | Fractional Ratio Derivation | `TrueConversionVelocity: 0.0451` |
| `CreativeText: "Buy 1 Get 1 Free!"` | Natural Language Metric Scoring | `SemanticIntentDensity: 0.82` (Promotion) |

### 2.3 System-Wide Intelligence & The Macro Oracle

The emitted anonymized telemetry strings are collected globally across all active agency nodes into the platform's distributed master ledger. This macro data graph computes real-time cross-platform efficiency indicators.

When any node inside an agency's customized graph requires context optimization (such as budget scaling decisions), it queries the network's Macro Oracle using a standard token clearing fee. The oracle returns cross-platform benchmarks computed across thousands of anonymized entities, enabling real-time, data-driven optimization without exposing private company assets.

---

## 3. Legal & Regulatory Compliance Framework (Terms of Service Guidelines)

To protect the platform, the operating agencies, and their ultimate enterprise clients, the Software License Agreement and Terms of Service (ToS) must integrate data governance parameters. This framework ensures that data collection is treated as an asset-yielding software optimization property.

### 3.1 Legal Counsel Architectural Directives

#### 1. De-Identified Software Telemetry License

The ToS must specify that the platform does not ingest, own, or store proprietary client assets or Personally Identifiable Information (PII). It must define the network data stream strictly as **Abstract Machine Telemetry Data** generated by the runtime execution loops of the distributed SDK. This establishes that the platform is tracking software execution statistics, which falls outside standard client NDA restrictions.

#### 2. Omnichannel System Optimization Consent

The agreement must contain an explicit clause granting mutual operational consent:

> *"The executing node operator grants the platform protocol a non-exclusive, perpetual, royalty-free, fully anonymized license to process mathematical metadata performance transformations. This processing is executed solely to optimize global network routing, train contextual automated validation nodes, and maintain the systemic intelligence of the distributed ecosystem."*

#### 3. Co-Staking & Value Return Alignment

To prevent client resistance, the legal framework must outline the **Co-Staking Dividend System**. The contract establishes that by maintaining an active telemetry node connection, the agency earns utility tokens based on the data volume processed by their infrastructure. The document provides structural guidelines enabling the agency to pass a portion of these token yields back to their clients as a direct credit against their agency service retainers, turning data collection into a shared economic incentive.

---

## 4. Master Marketing DAG Technical Specifications

The Master Marketing DAG is an event-driven, state-carrying graph executed asynchronously over NATS JetStream. It enforces type-safe state transitions across all nodes while allowing runtime pruning or extension via the platform SDK.

### 4.1 Global Protocol Structural Contracts

```go
package contracts

import (
	"crypto/ed25519"
	"time"
)

type DAGState string

const (
	StatePending    DAGState = "PENDING"
	StateProcessing DAGState = "PROCESSING"
	StateCompleted  DAGState = "COMPLETED"
	StateHold       DAGState = "HOLD"
)

type ExecutionContext struct {
	GraphInstanceID   string            `json:"graph_instance_id"`
	OrganizationID    int64             `json:"organization_id"`
	ClientID          string            `json:"client_id"`
	CurrentNodeIndex  int               `json:"current_node_index"`
	PrunedNodeIndices []int             `json:"pruned_node_indices"`
	TokenEscrowWallet string            `json:"token_escrow_wallet"`
	ExecutionHistory  []StateLog        `json:"execution_history"`
	MetadataContext   map[string]string `json:"metadata_context"`
}

type StateLog struct {
	NodeID       string    `json:"node_id"`
	Timestamp    int64     `json:"timestamp"`
	StateReached DAGState  `json:"state_reached"`
	OperatorKey  string    `json:"operator_key"` // Almanac Cryptographic Public Key
	Signature    []byte    `json:"signature"`    // Sign(NodeID + Timestamp + StateReached)
}

```

---

### 4.2 Granular Node Specification Directory

#### Node 1.0: Strategy & Briefing Intake

* **NATS Subscription Topic:** `marketing.dag.v1.intake`
* **Responsible Persona:** Account Manager
* **Input Schema Requirement:**
```go
type StrategyIntakeInput struct {
	Context   ExecutionContext `json:"context"`
	TargetKPI struct {
		TargetCPA     float64 `json:"target_cpa"`
		MonthlyBudget float64 `json:"monthly_budget"`
		VerticalCode  int     `json:"vertical_code"`
	} `json:"target_kpi"`
	BrandGuidelinesURL string `json:"brand_guidelines_url"`
}

```


* **Processing Core Logic:** The node maps core business targets and establishes performance thresholds. It initializes the baseline parameters required for downstream Shannon Entropy uncertainty calculations.
* **Output Payload Contract:**
```go
type StrategyIntakeOutput struct {
	Context             ExecutionContext `json:"context"`
	StandardizedTargetID string           `json:"standardized_target_id"`
	ConstraintVector    []float64        `json:"constraint_vector"` // Transformed vector weights for LLM validation
	Timestamp           int64            `json:"timestamp"`
}

```



#### Node 2.0: Technical Environment & Attribution Architecture

* **NATS Subscription Topic:** `marketing.dag.v1.engineering`
* **Responsible Persona:** Agency Software Developer
* **Input Schema Requirement:** Consumes `StrategyIntakeOutput` directly via NATS JetStream stream.
* **Processing Core Logic:** The developer node configures server-to-server tracking endpoints, provisions custom landing page runtimes, and verifies cryptographic public keys inside the Almanac registry for the target tenant domain.
* **Output Payload Contract:**
```go
type EngineeringSetupOutput struct {
	Context          ExecutionContext `json:"context"`
	ServerRoutingURL string           `json:"server_routing_url"` // Server-Side Attribution Capture Endpoint
	VerificationTag  string           `json:"verification_tag"`  // DNS TXT handshake cryptographic proof
	IsTrackingActive bool             `json:"is_tracking_active"`
}

```



#### Node 3.0: Creative Factory & Asset Engine

* **NATS Subscription Topic:** `marketing.dag.v1.creative`
* **Responsible Persona:** Copywriter / Creative Director
* **Input Schema Requirement:** Consumes `EngineeringSetupOutput`.
* **Processing Core Logic:** Generates ad copy variations, text hooks, and connects multimedia raw asset binaries. This node passes text data inputs directly through an LLM tool-calling layer to verify structural formatting compliance before asset output.
* **Output Payload Contract:**
```go
type CreativeAssetOutput struct {
	Context    ExecutionContext `json:"context"`
	AdVariants []struct {
		VariantID string   `json:"variant_id"`
		TextHook  string   `json:"text_hook"`
		AssetURLs []string `json:"asset_urls"`
		Category  string   `json:"category"`
	} `json:"ad_variants"`
}

```



#### Node 4.0: Compliance Gateway & Client Review

* **NATS Subscription Topic:** `marketing.dag.v1.compliance`
* **Responsible Persona:** Client Approver / Compliance Bot
* **Input Schema Requirement:** Consumes `CreativeAssetOutput`.
* **Processing Core Logic:** Halts automatic workflow execution. The system builds an isolated web view containing the outputs from Nodes 1.0, 2.0, and 3.0. The human reviewer or automated regulatory checking bot must sign the execution state with their private key to satisfy the node constraint.
* **Output Payload Contract:**
```go
type ComplianceApprovalOutput struct {
	Context            ExecutionContext `json:"context"`
	ApprovedManifestID string           `json:"approved_manifest_id"`
	ClientSignature    []byte           `json:"client_signature"` // Cryptographic verification of human/bot signoff
	ReleaseAuthorized  bool             `json:"release_authorized"`
}

```



#### Node 5.0: Media Ingestion & Network Deployment

* **NATS Subscription Topic:** `marketing.dag.v1.deployment`
* **Responsible Persona:** Media Buyer / Automated Deployment Bot
* **Input Schema Requirement:** Consumes `ComplianceApprovalOutput`.
* **Processing Core Logic:** An active deployment bot reads the verified manifest, matches the server URLs from Node 2.0 and the assets from Node 3.0, and executes automated programmatic configurations against target ad platforms.
* **Output Payload Contract:**
```go
type DeploymentExecutionOutput struct {
	Context          ExecutionContext `json:"context"`
	NetworkEngineIDs map[string]string `json:"network_engine_ids"` // Target Ad platform active tracking IDs
	LiveBudgetValue  float64           `json:"live_budget_value"`
	LaunchTimestamp  int64             `json:"launch_timestamp"`
}

```



#### Node 6.0: Cognitive Telemetry Ingestion (The System Anchor)

* **NATS Subscription Topic:** `marketing.dag.v1.telemetry`
* **Responsible Persona:** Cognitive Enrichment Engine (CEE)
* **Input Schema Requirement:** Consumes `DeploymentExecutionOutput` along with streaming server ledger data logs.
* **Processing Core Logic:** This node acts as a non-prunable system boundary. It executes continuous metric collection, pulling live ad platform spend data alongside true server-side conversions.

The node calculates the system's operational certainty via Shannon Entropy ($H$). It uses the probability distribution weights ($P(x_i)$) assigned by the LLM classification nodes over performance trends:

$$H(X) = -\sum_{i=1}^n P(x_i) \log_2 P(x_i)$$

* If $H(X) \le 0.50$, the system automatically writes optimization parameters (such as budget scaling tasks) back to Node 5.0 and forks a telemetry packet to the network database.
* If $H(X) > 0.50$, the node sets the state to `StateHold` and dispatches an asynchronous Hound Agent to resolve metric anomalies.
* **Output Public Telemetry Contract:**
```go
type GlobalMacroTelemetryStream struct {
	NetworkMessageID string    `json:"network_message_id"`
	SectorID         int       `json:"sector_id"`          // Abstract Industry Key
	NormalizedVolume int       `json:"normalized_volume"`  // Scaled Value Magnitude
	ObservedBlendedROAS float64 `json:"observed_blended_roas"`
	EfficiencyDelta  float64   `json:"efficiency_delta"`
	SystemEntropy    float64   `json:"system_entropy"`
}

```



---

### 4.3 Pruning & Extension Execution Engine Mechanics

The CMOS engine parses the `ExecutionContext` payload before routing messages to the next NATS channel topic destination.

```go
func RouteNextDAGNode(ctx *ExecutionContext, currentPayload interface{}) string {
	ctx.CurrentNodeIndex++

	// Evaluate if the upcoming node index is present in the Pruned array
	for _, prunedIndex := range ctx.PrunedNodeIndices {
		if ctx.CurrentNodeIndex == prunedIndex {
			// Record a state skip log for auditing
			LogSkipState(ctx.GraphInstanceID, ctx.CurrentNodeIndex)
			
			// Recursively increment to bypass the pruned node execution loop
			return RouteNextDAGNode(ctx, currentPayload)
		}
	}

	// Resolve the target NATS topic mapped to the verified Node index
	return GetNATSTopicFromIndex(ctx.CurrentNodeIndex)
}

```

Through this framework, if an agency chooses to prune the Engineering phase (Node 2.0), the runtime engine intercepts the execution context at the completion of Node 1.0. It detects the prune command for Index 2, updates the routing state history, skips the engineering channel topic, and delivers the strategy output envelope directly to the Creative Factory channel (Node 3.0) without stalling or breaking downstream type constraints.