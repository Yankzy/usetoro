# Autonomous Semantic Engine (ASE)

## What is the Autonomous Semantic Engine (ASE)?
The Autonomous Semantic Engine (ASE) is a proprietary, highly scalable artificial intelligence framework designed to automate complex financial and accounting workflows. At its core, it acts as an intelligent, automated ledger engine that categorizes raw, unstructured bank transactions into precise accounting classifications (such as mapping a purchase to a specific QuickBooks Online account).

## What Does it Do?
When a business connects its bank accounts, the raw transaction data is often messy and lacks accounting context. The ASE's job is to take thousands of these unclassified transactions and accurately determine:
1. **The Cash Direction:** Is money coming in or going out?
2. **The Macro Class:** Is this an Asset, Liability, Equity, Revenue, or Expense?
3. **The Account Type:** Which specific section of the Chart of Accounts does this belong to?
4. **The Entity:** Who is the exact Vendor or Customer?

It processes these transactions in bulk, matching or exceeding the accuracy of a human Certified Public Accountant (CPA), but at a fraction of the time and cost.

## Core Architecture
The ASE is built on a **Directed Acyclic Graph (DAG)** architecture. Instead of asking an AI to make one massive, complicated guess about a transaction, the ASE breaks the accounting process down into a series of small, specialized "nodes." 

* **Dynamic Routing:** Transactions flow through these nodes step-by-step. For example, a transaction is first routed by cash direction, then sent to a specialized node just for classifying "Outflow Expenses."
* **Batch Processing:** The engine groups similar transactions together and processes them in parallel batches, drastically reducing API costs and increasing throughput.
* **Human-in-the-Loop (HITL):** If the engine is ever unsure about a transaction, it places it in a "Holding Gate." It seamlessly hands the transaction over to a human accountant for review, learns from their decision, and resumes the automated flow.

## How it Uses LLMs for Reasoning
The ASE uses a highly efficient, hybrid approach to Large Language Models (LLMs):
1. **Specialized Brains:** The ASE engine stores highly specialized instructions (prompts) for different accounting scenarios. It acts as the "brain," dynamically pulling relevant historical context, company rules, and vendor hints.
2. **Generic Execution:** It bundles this context and sends it over a high-speed messaging queue (NATS) to a fleet of "dumb" generic agents. These agents simply execute the LLM call (e.g., to GPT-4o or GPT-5.4) and return the results.
3. **Mathematical Guardrails:** LLMs are known to hallucinate. The ASE protects against this by forcing the LLM to return a "probability distribution" (e.g., 98% confident it's an Expense, 2% confident it's a Liability). The ASE runs strict mathematical validation on the response to ensure the confidence scores equal exactly 100% (1.0). If the math is wrong, the ASE automatically rejects the answer and forces the LLM to try again.

## How it Uses the Database
The ASE is deeply integrated with a PostgreSQL database, making it fully stateful and auditable:
* **Memory & Context:** Before making a decision, the ASE queries the database to "remember" past human decisions, known vendors, and the specific company's Chart of Accounts.
* **Checkpointing:** As a transaction moves through the DAG, its state is continuously saved to the database. If a server crashes or a human needs to intervene, the transaction's exact location and reasoning are perfectly preserved.
* **Backtracking:** Because every decision is logged in the database, the ASE has the unique ability to "time travel." If the engine realizes it made a mistake down the line, it can seamlessly rewind its state in the database, undo the incorrect classifications, and take a different path.

## Why This Matters
The ASE architecture separates the "intelligence" (the engine and prompts) from the "compute" (the LLM calls). This makes the system incredibly resilient, mathematically predictable, and easily upgradable to newer AI models in the future, providing a massive competitive moat in the automated bookkeeping space.


The `ase` package implements Toro's **Autonomous Semantic Engine**, replacing linear batch processing pipelines with a concurrent, event-driven agentic architecture for financial classification.

## Architecture

Instead of treating transactions as static rows in a database that wait for a daily batch job, the ASE treats every unclassified transaction as a living **Transaction Micro-Agent** (`AutonomousSemanticEngineNode`). 

These micro-agents are born with maximum mathematical entropy (100% uncertainty) and traverse a Directed Acyclic Graph (DAG) of specialized accounting nodes (e.g., Macro Classification, Account Type Selection, Entity Extraction).

### 1. The Lifecycle (`node.go`)
Each agent manages its own state lifecycle running on an independent Goroutine loop:
`Triage → Think → Activate → Collapse`

Agents use a non-blocking `select` loop with a state channel, allowing them to wait efficiently (even for 48+ hours) if human intervention is required, without consuming CPU cycles.

### 2. DAG Batching (`dag.go`)
While agents are treated individually, querying the LLM for every single transaction is expensive and slow. To solve this, the `DAGNode` acts as a bus stop. 

**Categorized Batching:** There isn't just one global queue. Instead, there is a graph of distinct DAG Nodes representing specific logical boundaries (e.g., one DAG node for "INFLOW Macro Classification", another specifically for "REVENUE Account Type Selection"). Arriving agents register with the exact DAG node corresponding to their category and entropy level.

Once a specific DAG queue hits its `BatchSize` or the `FlushTimer` elapses, that DAG Node groups its waiting agents and dispatches them to the AI worker concurrently (the `Think` phase). Because they are waiting at the same node, the batch is guaranteed to be contextually identical. Agents waiting for an external reply (humans/webhooks) are isolated in `HOLD` states and are excluded from these batches.

### 3. Shannon Entropy & The State Collapse Guardrail (`node.go` & `store.go`)
The engine mathematically models its confidence using **Shannon Entropy**.
As the agent traverses the DAG, it accumulates candidate probabilities for different properties (Macro Class, Account, Entity). The unified confidence score $C$ is calculated as:

$$C = 1 - \frac{\sum_{k \in \text{properties}} H(k)}{\text{Total System Properties Required}}$$

**The Guardrail:** An agent is structurally barred at the database layer from transitioning to `READY_FOR_SYNC` (syncing to QuickBooks) unless $C \ge 0.98$. If confidence is too low, it transitions to a `HOLD` state.

### 4. Persistence & Caching (`store.go`)
- **Postgres:** Agents continuously persist their `CurrentState`, `UnifiedConfidence`, and classification properties to the `fignode.staging_transactions` table.
- **Redis:** Active agents are cached in Redis to survive server restarts, preventing in-flight transactions from being lost in memory.

### 5. Telemetry & The Virtual Workforce (`telemetry.go`)
The engine is deeply observable. Every state transition emits a NATS event (`ase.telemetry.*`). 
When an agent hits a `HOLD_AMBIGUOUS` or `HOLD_MISSING_CONTEXT` state, the telemetry module automatically routes a request for help to the "Virtual Workforce". The Conversational Agent directly messages the business owner via their preferred communication channel (with **Email** as the primary default, falling back to other channels) to ask for the required context. Once the human replies, the Conversational Agent parses the response, updates the database, and the transaction agent resumes its journey toward State Collapse.

### 6. Automated Backtracking & Feedback Compounding (`backtracking.go`)
When a human provides context that contradicts a decision made earlier in the DAG (e.g. "This is not an asset, it's a liability"), the ASE uses an autonomous backtracking agent. 

**State Rewinding:** The system injects the human's response and the agent's historical `ExecutionTrace` into the LLM. The LLM identifies the exact `DAGNodeID` where the erroneous decision was made. The agent then automatically deletes all classifications from that point onward, rewinds its state, and resumes processing from the corrected node.

**The Compounding Layer:** Simultaneously, the LLM extracts a generalized accounting rule and a specific `rule_keyword` from the human's feedback. This rule is permanently saved to the company's ledger memory (`toro_core.agent_memory_rules`).
On all future transactions, the DAG node batches perform a fast, case-insensitive keyword match. If a saved rule's keyword matches the transaction description, that instruction is dynamically injected into the LLM's prompt, overriding default assumptions and ensuring the system learns from past mistakes.

## How to Use ASE (Entry Points)

The engine is designed to be easily embedded in background workers (like a CDC pipeline that reads from QBO) or invoked directly via an API webhook. 

### 1. Initialize the Engine Infrastructure
You must create the `StateStore` (managing Redis and Postgres) and the `DAG` (the graph of specialized classification stops).

```go
// 1. Initialize the unified state store
store := ase.NewStateStore(redisClient, pgPool, logger)

// 2. Initialize the DAG and register processing nodes
dag := ase.NewDAG(logger)
// e.g. dag.RegisterNode(inflowMacroNode)
// e.g. dag.RegisterNode(outflowMacroNode)
```

### 2. Spawning a New Transaction Agent
For every net-new unclassified transaction that needs processing, you construct a micro-agent and launch it on its own Goroutine. 

```go
// 3. Create the micro-agent from a raw transaction
agent := ase.NewASENode(
    tenantID, 
    "ACH ELECTRONIC DEBIT STRIPE", 
    "OUTFLOW", 
    "1500.00",
)

// 4. Launch the agent's lifecycle (runs asynchronously)
go func() {
    if err := agent.Run(context.Background(), dag, store); err != nil {
        logger.Error("agent crashed", "node_id", agent.NodeID, "error", err)
    }
}()
```

### 3. Handling Resumptions (Webhooks & Wait States)
If an agent hits a `HOLD_MISSING_CONTEXT` state, the Goroutine safely exits and clears its memory footprint. When an external system (like a webhook from Slack or the Conversational Agent) receives the missing context, the agent must be resurrected.

To resume an agent:
1. Update its properties in the `fignode.staging_transactions` database and append the new context.
2. Reconstruct the `AutonomousSemanticEngineNode` struct from the database row.
3. Call the `AutomatedBacktrackAndResume` orchestrator function. The system will autonomously determine if it needs to rewind its state or simply resume forward, and re-enter the DAG routing loop automatically.
