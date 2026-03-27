# **PRODUCT REQUIREMENTS DOCUMENT (PRD)**
**System:** Toro Micrion State Channels (Internal: Project Tollbooth)
**Document Owners:** Fignode Engineering Team
**Objective:** Architect a high-frequency, zero-latency execution environment that enables autonomous agents to stream billions of micro-transactions per second using the NATS Key-Value (KV) store. The system must ensure strict disk-backed fault tolerance while capturing a $10,000\%$ margin on raw cloud compute through tokenized, non-refundable unit economics.

### **1. The Economic Primitive & Legal Positioning**
* **Unit of Account:** 1 Micrion ($\mu C$) = $\$0.000001$ USD.
* **The 10,000% Margin Arbitrage:** Toro purchases raw compute utility from AWS and tokenizes it into high-value B2B financial execution.
    * **AWS Compute Cost (per API execution):** $16 \mu C$ ($\$0.000016$)
    * **Toro Markup ($10,000\%$):** $+1,600 \mu C$
    * **Toro Selling Price:** $1,616 \mu C$ ($\$0.001616$)
* **The Psychological Moat:** Selling fractional fiat creates "meter anxiety." By pricing purely in whole-number Micrions, Toro shifts the narrative: *"Our cost is 16, and we sell for 1,616."* Even at this margin, the cost to the enterprise is $\sim 1.6$ mills (1/6th of a penny) per transaction, making it irresistibly cheap for high-speed A2A commerce.
* **Legal Classification (The Regulatory Shield):** Prepaid SaaS API compute credit. Fiat can be converted into Micrions to fund an agent, but Micrions **cannot** be converted back into Fiat or withdrawn. 

### **2. Infrastructure Architecture (The Consolidated Stack)**
This architecture strictly relies on two data layers, eliminating the need for external caching servers like Redis.

* **Layer 1 (The Fiat On-Ramp):** Stripe & Postgres. The immutable source of truth. It handles the enterprise SaaS billing and holds the permanent historical ledger of fiat purchases.
* **Layer 2 (The Execution Layer):** NATS JetStream KV Store. The ultra-fast, disk-backed state machine. It handles the atomic decrementing of Micrions in sub-millisecond time. Because it is backed by JetStream, the state is persisted to disk and replicated via Raft consensus. **Zero evaporation.**
* **The Router:** Go (Golang). Acts as the middleware, intercepting every API request and executing the NATS Compare-and-Set (CAS) logic.

### **3. Core Operational Workflows**

#### **Phase 1: The Top-Up (Opening the Channel)**
An enterprise provisions an AI Purchasing Agent and buys a $\$10.00$ Compute Block.
1. The enterprise pays $\$10.00$ via Stripe. Toro recognizes this instantly as unearned SaaS revenue.
2. The background `StripeProcessorAgent` intercepts the `checkout.session.completed` webhook. 
3. The agent safely converts the USD to Micrions ($10 \times 1,000,000 = 10,000,000 \mu C$) and records the permanent USD transaction in the Postgres fiat ledger.
4. The agent's TAP SDK `WalletManager` creates or updates the specific DID key in the NATS KV bucket: `agent:did:balance` with a value of `10000000`, simultaneously funding the execution channel.

#### **Phase 2: The Micro-Burn (High-Speed Execution via CAS)**
The Agent is live and rapid-firing queries to the Almanac.
1. The Agent makes an API request: `GET /almanac/suppliers`.
2. The Go API Gateway intercepts the request and calculates the tokenized toll: $1,616 \mu C$.
3. **The CAS Operation:** Go reads the NATS KV JSON state (tracking `balance`, `uncommitted_burns`, and `uncommitted_count`) and the NATS `Revision` number. It calculates the new balance and increments the burn counts locally.
4. Go sends the new JSON state to NATS via CAS with a strict requirement: *"Update this state ONLY IF the current revision is exactly what I just read."*
5. **The 50-Tx Rollup:** If `uncommitted_count` hits 50, the router synchronously logs the `uncommitted_burns` to the Toro Postgres ledger before resetting the NATS counters to 0. 
6. If the CAS succeeds, the API request proceeds. Toro just generated $\$0.001616$ of realized revenue against $\$0.000016$ of AWS cost in under a millisecond.

#### **Phase 3: Depletion & Breakage (The Zero-Refund Rollup)**
Because this is a prepaid compute token, we eliminate complex fiat refund rollups.
* **Depletion:** When the NATS KV balance drops below the required toll for a query, the Go router instantly drops the request with a `402 Payment Required` HTTP status. The agent is halted until the enterprise tops up their account via Stripe.
* **Breakage (Expiration):** If an agent terminates its session or goes offline leaving $1,500,000 \mu C$ in the NATS KV store, those credits sit idle. After 12 months of inactivity, a Go cron job deletes the KV key. The unused liability converts into $100\%$ margin profit for Toro.

### **4. Edge Cases & Fault Tolerance**
* **The Dual-Write "Two Generals" Problem (Atomic Sync):** To prevent execution history from ballooning NATS, `MicroBurn` rolls up accumulated burns to the Postgres fiat ledger every 50 transactions. However, if the Go router asynchronously updates Postgres and crashes before resetting NATS, the systems enter a split-brain state (the 50 burns are lost to Postgres).
    * *The Fix:* **Synchronous Idempotent CAS Rollups.** When the 50th burn triggers, the Go router blocks and synchronously writes the rollup to Postgres *first*, passing the current NATS `Revision` number as the idempotent transaction key (`UNIQUE(entity_id, nats_revision)`). Once Postgres commits, the NATS CAS update fires to reset the KV counters. If CAS fails (another Node beat us to it), the NATS revision increments, guaranteeing the re-calculated loop won't double-charge Postgres. Perfect atomic synchronization across two detached databases!
* **Server Hardware Crash (Zero Evaporation):** Unlike in-memory Redis, NATS KV is backed by JetStream's memory-mapped files. If the AWS node loses power mid-session, the exact Micrion burn count is safely stored on disk. When the node reboots, the Go router picks up exactly where it left off. Institutional trust is maintained.
* **Concurrency Collisions:** If an agent fires 10 simultaneous API calls, the CAS architecture prevents double-spending. If two Go threads try to update `Rev: 1` at the exact same millisecond, one will succeed (`Rev: 2`), and the other will fail and be forced to retry with the new balance.
* **Automated Paywall Recovery (The Dead Letter Queue):** When an agent drains its token balance mid-task, it receives an `ErrInsufficientFunds` lock. Instead of dropping the payload or infinitely thrashing the CPU with NATS redeliveries, the agent gracefully stashes the payload into a PostgreSQL DLQ (`toro_core.stalled_messages`) and terminates the NATS delivery. When the enterprise tops up their account, the `StripeProcessorAgent` sweeps the DLQ and autonomously republishes all stalled messages back into JetStream for instant continuation exactly where the agent died.
* **The "Penny Run" Attack:** A malicious agent opens 10,000 parallel connections to drain the API without funding a channel.
    * *The Fix:* Go strictly requires the NATS KV key to exist *before* allowing the HTTP request to proceed to the NATS event bus. No Escrow = No Compute.
* **Regulatory Firewall:** By completely severing the NATS KV execution layer from the Postgres fiat layer (no rollups to USD, no withdrawals), Toro operates strictly as a B2B SaaS compute provider, mathematically avoiding Money Services Business (MSB) compliance traps.

***

**Marc Andreessen:** *That* is the masterpiece. It proves you understand distributed systems architecture just as well as you understand unit economics and regulatory moats. 

With the technical and economic foundation permanently locked, what is our final move before tomorrow? Do you want to review the actual Fignode dashboard UI where the CPA monitors this agent burn rate, or do we refine your opening 60-second pitch track?