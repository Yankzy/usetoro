By combining the ToroDB execution harness with AlloyDB Omni and a decentralized messaging protocol, you are building the exact antithesis of centralized SaaS communication like Slack. You are building an **Agentic Operating System**, where the company's historical DNA and real-time execution live in the same isolated, self-hosted box.

Traditional platforms treat AI as an add-on that searches unstructured data. This architecture treats AI as a foundational peer that turns conversations directly into deterministic actions.

Here is the complete Product Requirements Document (PRD) for the decentralized, AI-native workspace.

---

## Product Requirements Document: ToroChat
## Status
- [x] Draft
- [ ] Review
- [ ] Approved
- [ ] Executed

**Document Version:** 5.0 (Decentralized Workspace & Seed Context)

**Target Architecture:** Golang (Stream Harness), AlloyDB Omni (Embedded Database), Peer-to-Peer Sync, LLM API

**Core Objective:** Build a decentralized, local-first communication workspace where every company runs its own isolated instance of ToroDB. The workspace is seeded with foundational SOPs, treating AI agents as equal peers capable of turning team chat into structured execution.

### 1. Product Vision: The "Seeded" Workspace

ToroChat is a BYOBrain (Bring Your Own Brain) communication protocol. Instead of renting a generic workspace on a centralized server, a company downloads and runs its own ToroChat node.

Before the first employee sends a message, the workspace is **"seeded"** with the founder's original memos, internal wiki, standard operating procedures (SOPs), and product roadmaps. This seed data acts as the immutable DNA of the AI. As employees chat, make decisions, and debate, the local AI agents continuously cross-reference the live chat against the seed documents to enforce operating principles, onboard new hires, and automate complex workflows.

### 2. Deployment & Data Sovereignty

To guarantee that proprietary corporate knowledge—from strategic pivots to private financial discussions—never leaks to a centralized cloud, ToroChat relies on **AlloyDB Omni**.

* **Isolated Nodes:** Each company deploys the unified ToroDB container image (AlloyDB Omni + Go execution harness) on their own infrastructure (AWS, GCP, bare metal, or local Kubernetes clusters).
* **Enterprise Security:** The system leverages AlloyDB Omni's Transparent Data Encryption (TDE) to protect the workspace's data-at-rest. The AI has full access to the company's deepest secrets because those secrets never leave the company's own hardware.
* **Scale:** AlloyDB Omni's integrated ScaNN vector extensions allow the workspace to scale memory up to billions of vector rows with lightning-fast semantic retrieval.

### 3. Core Functional Pillars

#### 3.1. The Genesis Seed (Context Initialization)

* **Ingestion Pipeline:** During workspace setup, the founder uploads PDFs, Notion exports, and Markdown files representing the company's core SOPs and memos.
* **Ordered Synthesis:** The ToroDB Go harness does not just embed this text; it synthesizes it into relational rules. If a founder's memo says, *"All server deployments require QA approval,"* the LLM translates this into a strict DAG constraint within AlloyDB.
* **Contextual Guardrails:** When a junior engineer asks a question in the `#engineering` channel, the AI agent replies using the exact terminology and architectural preferences outlined in the founder's seed documents.

#### 3.2. Human-Agent Parity (First-Class Peers)

Agents in ToroChat are not reactive slash-command bots; they possess cryptographic identities and act as autonomous participants in the workspace.

* **Continuous Listening:** Agents monitor specific channels for state changes. If a user types, *"We agreed to drop the price to $50,"* the agent automatically updates the CRM state in the database without needing to be explicitly `@mentioned`.
* **Agent-to-Agent (A2A) Negotiation:** Agents communicate invisibly over the protocol. If Employee A's agent needs a legal document from Employee B's agent, the agents negotiate the exchange in the background and surface the final document directly in the human chat thread.

#### 3.3. Chat-to-DAG Execution

Because the chat interface is built directly on top of the ToroDB execution harness, conversations trigger deterministic workflows.

| Chat Event | ToroDB Harness Action | AlloyDB Omni State Change |
| --- | --- | --- |
| **User Uploads CSV** | Go stream processor chunks the file and triggers the LLM to map columns. | Writes clean, typed data to a relational table. |
| **Manager Types "Approved"** | Go harness extracts the entity (Invoice #104) and verifies the user's role authorization. | Updates `status = approved` and triggers the downstream DAG payment node. |
| **Team Debates a Bug** | LLM synthesizes the thread to identify the root cause and agreed solution. | Writes a structured post-mortem to the columnar engine for real-time reporting. |

### 4. Technical Architecture: The Peer-to-Peer State

To achieve decentralization while maintaining the speed of a modern chat app, ToroChat utilizes a hybrid state model:

* **The UI Client (Frontend):** A lightweight desktop/web application that renders the chat interface and connects directly to the company's self-hosted ToroDB node via WebSockets.
* **The Ingestion Harness (Middleware):** The Go-based stream processor intercepts every chat message. It determines if a message is just "noise" (simple human chatter) or a "state change" (a decision, an file upload, an approval).
* **The Omni Database (Backend):** State changes are synthesized by the LLM and committed to structured relational tables. Unstructured chatter is embedded using AlloyDB Omni's parallelized ScaNN indexes for semantic search.
* **Real-Time Analytics:** Because AI synthesis generates massive amounts of structured operational data, the platform utilizes AlloyDB's in-memory columnar engine. This allows the founder to instantly query dashboards like *"Show me the velocity of resolved customer complaints this week"* directly from the chat data, without needing a separate data warehouse.

### 5. Key Metrics & Success Criteria

1. **Agent Autonomy Rate:** The percentage of structured tasks (approvals, calendar bookings, data entry) completed by AI agents without human intervention.
2. **Seed Adherence:** Measured by the LLM evaluator verifying that agent responses and DAG constraints strictly align with the founder's original uploaded memos.
3. **Query Latency:** The time it takes the ScaNN index to retrieve semantic context from a 5-year-old chat thread. Minimum threshold: $< 50ms$.