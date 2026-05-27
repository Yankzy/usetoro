## **Product Requirements Document (PRD): Toro OS (The Coherence Engine)**

**Product Name:** Toro OS
**Underlying Tech:** Toro Agent Protocol (TAP), Dual-Tier NATS JetStream, AlloyDB Omni (ScaNN).
**Primary Objective (The Buffer):** Provide a centralized, multi-tenant intelligence layer that intercepts the client’s digital firehose (Email, Slack, News), strips emotional noise, and synthesizes it into actionable coherence.
**Secondary Objective (The Swarm):** Anonymize the metadata from the Buffer to generate real-time macro-signals, which dynamically reprogram local agents across the network.

### **Phase 1: The Personal Buffer (Primary Value Prop)**

*This is what the client actually buys. It solves their immediate pain of information overload.*

* **Ingestion (The Data Taps):** Toro connects to the client's Google Workspace, Slack, and targeted RSS feeds via OAuth. The raw firehose flows into our central NATS JetStream cluster, strictly tagged with their unique `tenant_id`.
* **The Stripping & Synthesis Agents:** Operating inside the client's secure Row-Level Security (RLS) partition in AlloyDB, these agents intercept the noise. They strip clickbait, emotional packaging, and irrelevant chatter.
* **The Output (The Signal):** The founder receives a dynamically ranked Executive Dashboard.
* *Immediate Action:* "Key client is requesting a contract pause."
* *Synthesized Briefing:* "Your main competitor just announced a 15% price hike across three news outlets."
* *Auto-Archived:* 90% of the daily noise is vectorized for semantic search but never hits their screen.



### **Phase 2: The Swarm Engine (Secondary Byproduct)**

*This is the structural moat we build from the data they generate.*

* **The Metadata Extraction:** As the Stripping Agent processes the private firehose in Phase 1, it simultaneously extracts *strictly anonymized* telemetry (e.g., `[Action: Vendor_Price_Increase]`, `[Domain: aws.amazon.com]`, `[Industry: SaaS]`).
* **The Global NATS Stream:** This sanitized metadata is fired into a secondary, global event bus that sits above the private tenant partitions.
* **The Reflex Orchestrator:** Our central AI monitors this global stream for macro-economic anomalies (e.g., "40% of SaaS clients are ignoring Hubspot renewal emails").
* **Dynamic Reprogramming (On-the-Fly):** The Orchestrator pushes API updates back down into the local tenant partitions, dynamically updating the prompts of the clients' local AI agents to adapt to the new macro-reality (e.g., instructing local Sales Agents to pivot messaging to highlight cost-savings).

### **Compliance & Data Sovereignty**

* Centralized hosting allows frictionless onboarding.
* PostgreSQL Row-Level Security (RLS) guarantees cryptographic separation of Phase 1 private data.
* Terms of Service explicitly authorize the use of anonymized telemetry (Phase 2) strictly to "improve system performance and safety for the end-user," avoiding Data Broker classification.
