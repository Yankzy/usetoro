---
description: Core rules for distinguishing Agents, Tools, and Workers in workflows
---

---
---

# Defining the Workflow Building Blocks: Agents vs. Tools vs. Workers

When implementing any architecture or workflow within the Toro ecosystem, it is critical to properly classify the three structural tiers of the event-driven AI ecosystem. 

Mixing the responsibilities of these components leads to tangled coupling, tight scaling bottlenecks, and non-deterministic behavior. Strictly adhere to these definitions when proposing, planning, or writing code:

## 1. Agents (Event-Driven LLM Routines)
**Agents** map to the cognitive and decision-making logic of the application (e.g., `CleanupAgent`).

- **Role**: Execute open-ended tasks, reason about ambiguous data, and execute context evaluation.
- **Networking**: They maintain persistent JetStream push-subscriptions to wake up, evaluate context, and emit Redux state changes. 
- **Rule of Thumb**: If the process relies on an LLM or requires decision-making based on unstructured context to mutate state, it is an Agent.

## 2. Workers (Event-Driven Deterministic Pipelines)
**Workers** are functionally pure, background pipelines meant to handle infrastructure processing reliably without cognitive loops (e.g., `EnrichmentWorker`).

- **Role**: Execute guaranteed data mutations and state synchronizations.
- **Networking**: They strictly react to downstream JetStream subjects (e.g., responding to `cleanup.inserted`) and sit within a centralized Dispatcher pattern.
- **Rule of Thumb**: If it performs a strict, pre-defined, deterministic operation without LLM inference (like Change Data Capture or webhook processing), it is a Worker. Do not add LLM logic to workers.

## 3. Tools (Synchronous Go Functions)
**Tools** are self-contained capabilities exposed directly to the AI runtime. 

- **Role**: Provide strict, synchronous execution of bounded tasks or data retrieval.
- **Networking**: Tools are the *only* components that bypass JetStream networking. They are executed directly in-memory by an Agent's LLM runtime during a reasoning loop.
- **Rule of Thumb**: If it is a synchronous function explicitly called by an Agent to query an external source or complete a side-effect, it is a Tool.

---
### Summary Checklist for AI Developers:
* Is it an LLM routine listening to JetStream? -> **Agent**
* Is it executed synchronously in-memory by an Agent? -> **Tool**
* Is it a deterministic background pipeline running without an LLM? -> **Worker**