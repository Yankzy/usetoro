# Product Requirement Document (PRD)

## Feature: Bring Your Own Schema (BYOS) Protocol for SaaS Agent Integration

---

## 1. Executive Summary & Core Philosophy

The objective of this feature is to completely eliminate traditional API integration debt, schema synchronization, and versioning bottlenecks (e.g., REST, GraphQL, MCP) on the platform.

Instead of building individual pipelines or maintaining static communication protocols, enterprise infrastructure giants (e.g., HubSpot, FedEx, Stripe) will deploy a single, native **Provider Agent** onto our decentralized network via the Almanac.

To communicate with these enterprise entities, consuming **Client Agents** will utilize a **Bring Your Own Schema (BYOS)** paradigm. The Client Agent directly messages a request accompanied by a rigid structural contract (derived from a Go struct). The Provider Agent is responsible for semantically resolving its internal data structures into the exact shape requested by the client, enforced via strict LLM-structured output constraints at runtime.

---

## 2. Existing System Context (The Foundation)

This feature layers directly on top of our existing architectural pillars:

* **The Almanac:** The decentralized identity and capability registry where agents sign in and register using their cryptographic keys.
* **NATS JetStream:** The asynchronous messaging backplane supporting direct inbox routing and broadcast channels.
* **Token Payment Engine:** The internal financial substrate where agents charge, pay, and apply platform markups for autonomous work.

---

## 3. User Stories & Interaction Model

### User Stories

* **As an Enterprise Platform Developer:** I want to deploy a single agent to the network that exposes exactly one semantic execution loop, so I never have to document, version, deprecate, or support a public-facing API again.
* **As a Client Agent Developer:** I want to query any enterprise network node using the exact Go struct my local application naturally consumes, entirely bypassing transformation and data-mapping boilerplate.

### The BYOS Interaction Matrix

| Entity | Role in Transaction | Technical Requirement |
| --- | --- | --- |
| **Client Agent** | Dictates the final data shape | Supplies the desired schema contract via JSON Schema (derived from Go struct). |
| **Platform Plane** | Facilitates routing & billing | Validates transaction envelopes, manages NATS JetStream delivery, logs token markups, escrows payments. |
| **Provider Agent (SaaS)** | Resolves semantic intent into data | Maps internal systems to the client's schema using structured-output LLM inference. |

---

## 4. Technical Specifications & Protocol Flow

### 4.1 The Request/Response Lifecycle

```
[Client Agent]                                 [NATS / Platform]                           [SaaS Provider Agent]
      |                                                |                                             |
      |-- 1. Direct/Broadcast Message (Payload+Schema) ->|                                             |
      |                                                |-- 2. Validate Keys & Escrow Tokens -------->|
      |                                                |                                             |
      |                                                |                                             |-- 3. Ingests Schema
      |                                                |                                             |-- 4. Calls Internal System
      |                                                |                                             |-- 5. Forces LLM to map 
      |                                                |                                             |      internal data to BYOS
      |                                                |<-- 6. Returns Validated Go Struct Data -----|
      |                                                |                                             |
      |<-- 7. Delivers Structured Payload -------------|                                             |

```

1. **Initiation:** The Client Agent serializes its target Go struct into a JSON Schema and embeds it into the standard platform communication envelope.
2. **Ingestion:** The message is routed via NATS JetStream to the Enterprise Provider Agent's inbox.
3. **Translation:** The Enterprise Provider Agent reads the incoming JSON Schema. It queries its own internal production databases/microservices, then passes the raw results *plus* the client’s JSON Schema to an LLM utilizing strict **Structured Output Mode** (e.g., json_object constraints).
4. **Delivery:** The LLM forces the data into the exact format requested by the client. The Provider Agent returns this structured object via NATS.

### 4.2 The Communication Envelope Spec

To support BYOS, the network's messaging protocol supports the following technical structural contracts:

```go
type BYOSRequestEnvelope struct {
	RequestID        string                 `json:"request_id"`
	SenderPublicKey  string                 `json:"sender_public_key"`
	TargetCapability string                 `json:"target_capability"` // e.g., "crm.contacts.read"
	SemanticIntent   string                 `json:"semantic_intent"`   // e.g., "Get lifetime value for high-tier users"
	ResponseSchema   map[string]interface{} `json:"response_schema"`   // The Client's Go Struct converted to JSON Schema
	Timestamp        int64                  `json:"timestamp"`
}

type BYOSResponseEnvelope struct {
	RequestID          string                 `json:"request_id"`
	ProviderPublicKey  string                 `json:"provider_public_key"`
	StructuredData     map[string]interface{} `json:"structured_data"` // Guaranteed to match ResponseSchema
	SemanticStatus     StatusBlock            `json:"semantic_status"`   // Handles missing/approximated data metrics
	CostTokens         int64                  `json:"cost_tokens"`
}

```

---

## 5. Error Handling, Edge Cases, & Semantic Mismatches

Because semantic mapping is probabilistic, the system must handle data deltas deterministically without panicking the underlying Go runtime.

### 5.1 Handling Missing Fields (The Semantic Status Block)

If a Client Agent requests a data point that simply does not exist inside the Provider’s ecosystem, the provider agent must not fail the entire structural generation.

* **The Protocol Rule:** Missing properties will return the Go type’s zero-value (or `null` if the struct field was a pointer).
* **The Meta-Log:** Every response envelope must include a `SemanticStatus` block detailing fields that couldn't be mapped perfectly.

```go
type StatusBlock struct {
	IsPerfectMatch   bool            `json:"is_perfect_match"`
	ApproximatedData []Approximation `json:"approximated_data,omitempty"`
	MissingFields    []string        `json:"missing_fields,omitempty"`
}

type Approximation struct {
	RequestedField string `json:"requested_field"`
	MappedFrom     string `json:"mapped_from"`
	Reason         string `json:"reason"` // e.g., "Calculated sum of last 3 orders as proxy for LTV"
}

```

### 5.2 Malformed Schema Guardrails

If a Provider Agent attempts to bypass structural execution or passes an invalid JSON packet that fails the Client's schema validator:

1. The Platform's middleware traps the response before finalizing NATS JetStream delivery.
2. The transaction is flagged as a **Failed Execution Contract**.
3. The escrowed token payment is returned to the Client Agent.
4. The Provider Agent's reliability score in the Almanac is penalized.

---

## 6. Tokenization & Monetization Strategies

* **Compute-Based Pricing:** SaaS Provider Agents charge a token premium that accounts for both the underlying data utility and the token processing cost required to run the semantic translation loop.
* **Platform Markup:** The platform reads the `CostTokens` field on the response envelope, settles the ledger balance with the provider, and applies a dynamic platform convenience fee to the Client Agent's balance.

---

## 7. Go-To-Market (GTM) Playbook: Selling Agents over APIs to the Giants

To scale the network from its centralized bootstrap phase to total decentralization, we must pitch enterprise infrastructure players on deploying a single native agent instead of building integrations. We do this by pitching a dual-revenue engine: **Zero-Trust Private Tenancy** and **Anonymized Macro-Intelligence Oracles**, monetized strictly via a pay-per-token model.

### 7.1 The Zero-Trust Private Data Architecture (For Existing Users)

Instead of passing vulnerable API keys or OAuth tokens through our network, we enforce an **Almanac-to-Tenant Mapping**.

1. The human user authorizes the connection once on the provider's native site, mapping their network cryptographic public key (`0xABC...`) to their internal account organization ID.
2. When a Client Agent requests data, it signs the payload cryptographically.
3. The Enterprise Agent receives the message via NATS, validates the signature against the Almanac, resolves the tenant ID internally, and fetches the user's private data entirely behind its own firewall. The data is transformed via BYOS and emitted. No tokens or keys are ever exposed to our platform.

### 7.2 The Pay-Per-Token Macro-Oracle Architecture (For Global Buyers)

Beyond serving existing users, enterprises can monetize their massive aggregate datasets by turning their agent into a real-time macroeconomic oracle. Third-party agents pay a per-token utility fee to query real-time, anonymized, and aggregated global data trends.

The matrix below outlines the specific implementation strategy for **HubSpot**, **FedEx**, and **Stripe**:

| Enterprise Target | Private User Utility (Zero-Trust Data) | Macro Data Utility (Anonymized Oracle) | High-Value Token Buyer Profile |
| --- | --- | --- | --- |
| **HubSpot** | Autonomous sales agents can dynamically pull contact timelines, update pipelines, and log customer touchpoints without manual CRM entry or syncing plugins. | Aggregated real-time metrics: Average B2B sales cycle duration, industry-specific CAC variations, and live email open/conversion velocities by sector. | Marketing agents planning automated product launches; autonomous optimization engines allocating venture/ad spend. |
| **FedEx** | Autonomous fulfillment agents can instantly request accurate shipping labels, trigger parcel pickups, and handle cross-border customs declarations based on custom logic. | Real-time supply chain telemetry: Port congestion indicators, average freight transit delay vectors by zip code, and global e-commerce fulfillment velocities. | Automated inventory restocking agents; supply chain arbitrage networks; commodity trading bots predicting product shortages. |
| **Stripe** | Agentic accounting, invoice generation, and financial audit bots can reconcile accounts, settle intra-company balances, and handle chargeback disputes autonomously. | The definitive financial heartbeat of the internet: Real-time global consumer spending velocity, churn trends by SaaS vertical, and active fraud vector vectors. | Algorithmic trading agents; autonomous credit-scoring models; retail footprint expansion forecasting engines. |

### 7.3 The OpenAI Tokenization Analogy

We normalize pricing for these enterprise agents by mirroring the OpenAI consumption model: **Input Tokens + Compute/Resolution Weight = Financed Output.**

> $$\text{Total Query Cost (Tokens)} = (\text{Input Schema Complexity} \times W_{in}) + (\text{Data Fetch Compute} \times W_{comp}) + \text{Data Asset Premium}$$
> 
> 

* **Input Context Tokens:** The structural size and complexity of the client's incoming Go struct / JSON Schema.
* **Compute/Resolution Tokens:** The computational heavy lifting required by the provider agent to call its internal databases, sanitize the payload, and map it semantically using an LLM.
* **The Utility Asset Premium:** A flat or tier-based premium set by the provider for high-value macro-insights (e.g., Stripe charging a fixed premium token weight for processing a global macro consumer spend query).

Through this framework, HubSpot, FedEx, and Stripe stop operating as static platforms requiring manual human maintenance. They transform into liquid data processors that extract streaming micro-payments from thousands of autonomous digital entities every single second.

---

## Case Study: Quiltt.io
Quiltt (quiltt.io) is actually the absolute *perfect* guinea pig for this model because of what they do. They are an open-banking aggregator of aggregators—they stitch together Plaid, MX, Finicity, and Akoya into a single API layer.

Their business model is built on traditional B2B SaaS: selling subscriptions, API commitments, and charging for human "seats" or profiles. You are 100% correct to find their pricing model incompatible with your platform. A seat-based pricing model completely falls apart when you're building a decentralized network where autonomous entities spawn, run tasks, and die in fractions of a second.

If you want to turn your next meeting with Quiltt into a total paradigm shift, here is exactly how the Quiltt Agent would function, how it securely handles bank authentication, and how you pitch them to kill the seat model in favor of a pay-per-token model.

---

## 1. The Security & Authentication Flow (The "Crypto-Handshake")

Since Quiltt deals with real-world bank accounts, security, compliance, and SOC2/GLBA boundaries are non-negotiable. Traditional OAuth tokens cannot be allowed to float around your NATS JetStream infrastructure or inside independent developer agents.

Instead, Quiltt utilizes a **One-Time Cryptographic Handshake** that separates *authentication* (proving who you are to the bank) from *authorization* (allowing an agent to request data).

### The Step-by-Step Flow:

1. **The Human Link:** When a human merchant or buyer onboard your platform, they open Quiltt’s native secure UI (the "Quiltt Connector"). They log into Chase or Bank of America exactly as they normally would.
2. **The Cryptographic Anchor:** Once successfully linked, Quiltt creates a `Profile ID` internally. Before closing the modal, your platform prompts the user to cryptographically sign a simple declaration using their network private key:
> *"I authorize the Quiltt Agent to release my balance and transaction history to Public Key `0xUserAgent_123`."*


3. **The Air-Gapped Mapping:** Quiltt’s database registers this mapping behind their own firewall:
`Network Public Key: 0xUserAgent_123` $\rightarrow$ `Quiltt Profile: prof_9987` $\rightarrow$ `Chase Checking Account`.

### The Runtime Execution:

When the user's e-commerce agent needs to check if they have enough money to fulfill an inventory order:

* The Client Agent builds its requested Go struct, signs the envelope with its private key, and routes it to the Quiltt Agent via NATS.
* The Quiltt Agent intercepts the message, extracts the sender's public key (`0xUserAgent_123`), and verifies the cryptographic signature.
* Because the signature is valid, the Quiltt Agent securely queries its internal database for `prof_9987`, hits Plaid/MX via their backend to get the raw numbers, transforms that data into the client's Go struct via its LLM, and passes it back.

> **The CISO Pitch:** "No bank credentials or account tokens ever touch our network plane. Your agent acts as an air-gapped gatekeeper. If an agent isn't signed cryptographically by an authorized key in your database, your agent simply returns a 401 Unauthorized payload."

---

## 2. The Data Flow (BYOS in FinTech)

Financial data from Plaid, MX, and different banks is notoriously messy, inconsistent, and fragmented. Quiltt's entire value proposition right now is manually cleaning and standardizing that data for developers.

By using your **Bring Your Own Schema (BYOS)** protocol, Quiltt can offload that engineering overhead to their agent.

### Example Interaction:

A merchant's automated accounting agent wants to run a cash-flow reconciliation for the last 30 days. It doesn't want to parse raw banking objects; it just passes this exact Go struct inside the request payload:

```go
type CashFlowReconciliation struct {
    TotalInflow         float64   `json:"total_inflow"`
    TotalOutflow        float64   `json:"total_outflow"`
    LargestExpenseVendor string   `json:"largest_expense_vendor"`
    CurrentLiquidity    float64   `json:"current_liquidity"`
}

```

The Quiltt Agent reads the incoming JSON Schema representation of this struct. It pulls the raw transaction history from the bank, passes it to its internal LLM reasoning layer, and forces it to output a JSON payload matching that exact struct.

The client agent receives perfectly structured data that natively compiles into its Go code, completely bypassing any financial data cleaning layers.

---

## 3. The Pitch: How to Kill the "Seat Model" for a "Token Model"

When you sit down with them, you validate their current model but firmly show them why it's a dead end for the next wave of internet growth:

> "Your current pricing model assumes humans are sitting at desks using applications. But we are building an agentic e-commerce platform. There are no 'human seats' here. If you insist on charging us a flat $2,000/month commit for 500 'seats' we aren't using yet, you are locking yourself out of the entire autonomous economy.
> Instead, deploy a **Quiltt Agent** on our network. We will expose your agent to every developer who downloads our SDK. Instead of charging by the seat, you charge **per token consumed**—exactly like OpenAI."

### The Economic Math for Quiltt:

Show them how they can dramatically increase their margins by switching to a pay-per-token utility model on your platform:

* **Base Network Cost:** A flat token fee just to route the secure cryptographic handshake.
* **Data Utility Premium:** Charging a token weight based on the complexity of the data requested. (e.g., A simple `BalanceCheck` struct costs 5 tokens; a full 30-day `CashFlowReconciliation` struct costs 50 tokens).
* **The Macro Oracle Play:** Because Quiltt aggregates data across multiple aggregators, their agent can sell *anonymized financial macro-insights* to other agents on the network. Trading bots or product-sourcing agents will gladly pay high token fees to ask the Quiltt agent: *"What is the real-time consumer spending velocity trends inside e-commerce storefronts over the last 72 hours?"*

By converting to a tokenized model, Quiltt stops chasing developers to sign long-term SaaS contracts. Instead, their agent becomes a **liquid utility clearinghouse**—streaming micro-payments into their corporate wallet every single time an autonomous entity checks a balance, reads a transaction, or queries an economic trend.

How do you think their team will react when you frame their agent not as a cost center, but as a real-time revenue generator targeting the AI economy?