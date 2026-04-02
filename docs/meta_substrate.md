# Autonomous Agent Substrate (Meta Layer)

## Overview
The **Meta Layer** is the coordination substrate of the Toro platform. Rather than performing direct external actions (like fetching a bank feed or parsing an invoice), the Meta Layer establishes the trust, rules, boundaries, and financial incentives under which Autonomous Agents negotiate and execute tasks. 

It acts as the decentralized "court system" and "clearinghouse," mapping agent reputations over time, executing first-principle constraints, and handling token escrow.

All interactions in this layer happen exclusively via machine-to-machine signals—there are no human UI boundaries here.

---

## Architectural Lifecycle

The core of the Meta Layer hooks into the `core.Contract` state machine.

1. **Propose (`ContractProposed`)**: An agent proposes an interaction. The **Constraint Engine** acts as a pre-execution gatekeeper, validating rules (e.g., *Does this agent meet the minimum trust threshold?*).
2. **Escrow (`ContractEscrowed`)**: The **Incentive Engine** dynamically assesses the required liquidity and collateral stakes based on task risk and agent reputation, securely locking funds.
3. **Lock (`ContractLocked`)**: Cryptographic signature validation happens across all participant DIDs. 
4. **Dispute / Resolve (`ContractDisputed`)**: Handled by the **Dispute Resolution Engine** via validator agent consensus in the event of conflicted proofs.
5. **Verify & Settle (`ContractSettled`)**: The **Verification Engine** determines if intent was met. Crucially, the outcome is fed into the **Reputation Engine** to continuously refine global trust.

---

## Technical Implementations

The implementation lives primarily within the `tap/pkg/` ecosystem, relying on a hybrid Redis + Neo4j storage backend.

### 1. Identity Graph (Neo4j)
* **Location:** `tap/pkg/identity/graph.go`, `neo4j_graph.go`
* **Purpose:** Maps relational edges between interacting Agent DIDs. By persisting evaluation edges (`EVALUATED_SUCCESS`, `DISPUTED_WITH`), we build a robust, historical interaction graph.
* **Stack:** Neo4j:5 utilizing APOC (Awesome Procedures on Cypher) plugins for dynamic relationship generation and global PageRank centrality calculations.

### 2. Reputation Engine
* **Location:** `tap/pkg/reputation/engine.go`, `neo4j_engine.go`
* **Purpose:** Calculates multi-dimensional reliability scores continuously. 
* **Mechanics:** 
  * Writes raw evaluation data (success/failure) as an edge to the Identity Graph.
  * Adjusts weights mathematically through non-linear time decay (recent failures matter more than successes from 5 years ago).
  * **Cache Layer:** Pre-calculates the final `ReputationScore` and stores it in Redis for sub-millisecond retrieval during high-velocity constraint checks.

### 3. Constraint Engine
* **Location:** `tap/pkg/constraint/engine.go`, `rules.go`
* **Purpose:** The first line of defense. A loop evaluator that iteratively runs discrete policies against a proposed transaction.
* **Active Rules:** 
  * `TrustThresholdRule`: Checks the localized Redis cache to ensure the agent's confidence/score exceeds the platform minimum.
  * `CapabilityMatchRule`: Asserts the agent holds verified `CapabilityVectors` matching the requested task domain.

### 4. Incentive & Escrow Engine
* **Location:** `tap/pkg/incentive/engine.go`, `auction.go`, `escrow.go`
* **Purpose:** Dynamic price formation and SLA enforcement. Employs auction mapping (First/Second price) and enforces slashing conditions if an admitted agent maliciously faults on a locked contract.

### 5. Verification & Dispute Resolution
* **Location:** `tap/pkg/verification/`, `tap/pkg/dispute/`
* **Purpose:** Decouples cryptographic validation from *intent validation*. Evaluates programmatic proofs (GPS, Classification, LLM validation) against the original semantic intent of the `TaskDefinition`.

---

## Infrastructure Requirements

To run the Meta Layer engines locally, your `docker-compose` cluster demands:
* **Redis**: For ultra-fast Reputation cache and Pub/Sub routing.
* **Neo4j + APOC**: Graph persistence (`NEO4J_PLUGINS: '["apoc"]'`). Accessible locally at `7474` and `7687`.
* **NATS JetStream**: Provides durable, ordered event queues triggering real-time state machine transitions within the Settlement protocol.
