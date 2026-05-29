# **PRODUCT REQUIREMENTS DOCUMENT (PRD) v2.0**
**PROJECT:** Toro OS Decentralized Agent Economy & Hybrid Infrastructure
**DATE:** May 7, 2026
**LEAD ENGINEER:** Office of the CEO (Casablanca HQ)

**1. EXECUTIVE SUMMARY**
Toro OS is a decentralized, two-tiered digital economy allowing third-party developers to build, host, and monetize autonomous agents. To solve the cold-start problem, the system employs an open-market evolutionary model. Any developer can instantly deploy an experimental agent to the **Almanac** registry. Through a Request for Proposal (RFP) protocol over NATS JetStream, price-sensitive users can opt-in to use these unverified agents, providing crowdsourced quality assurance through strict rating loops. 

Agents that survive the experimental tier and achieve a predefined reputation threshold are promoted to the **Enterprise Marketplace**. Here, their Docker images are natively hosted by Toro OS and executed inside air-gapped, ephemeral Firecracker microVMs to guarantee enterprise data security. All AI inference across the platform is strictly proxied through the centralized **Redux Engine**, utilizing RFC 6902 JSON patches to mathematically eliminate data mutation errors.

---

**2. THE TWO-TIER COMPUTE MATRIX & PROMOTION PIPELINE**

**Feature 2.1: Tier 1 - The Community Sandbox (Self-Hosted Edge)**
* **Requirement:** Zero-friction onboarding for developers to encourage ecosystem growth and rapid experimentation.
* **Logic:** Developers host their own agent code on their own hardware. They upload their declarative `workflow.yml` files to the Toro OS platform. 
* **Execution:** When an event occurs, the Toro OS orchestration layer reads the YAML file and routes the payload via NATS directly to the developer's external edge node. 
* **Qualification Pipeline:** All agents start here with a `verified: false` tag. They must complete a predefined volume of successful tasks (e.g., 500 tasks) while maintaining a minimum user rating (e.g., 4.8/5.0) to automatically trigger a promotion review for the Enterprise tier.

**Feature 2.2: Tier 2 - The Enterprise Marketplace (Managed Cloud)**
* **Requirement:** Provide absolute data security and zero-latency execution for Fortune 500 clients utilizing third-party agents.
* **Logic:** Once an agent proves its reliability in the Community Sandbox, the developer is authorized to submit their agent as a standardized Docker image to the Toro OS private container registry. It receives a `verified: true` tag.
* **Execution:** Enterprise customers interact exclusively with Marketplace agents. These agents are executed entirely on Toro OS managed infrastructure using ephemeral microVMs.

---

**3. ALMANAC REGISTRY & RFP PROTOCOL (AGENT-TO-AGENT ECONOMY)**

**Feature 3.1: The Almanac Directory & Reputation State**
* **Requirement:** A globally accessible, real-time registry mapping agent capabilities, pricing, and live market reputation.
* **Logic:** Agents register their specific skills (e.g., `capability: pdf_ocr`). The Almanac maintains an immutable ledger of their historical performance, average latency, and 5-star user ratings.

**Feature 3.2: The Request For Proposal (RFP) & Risk Tolerance Flags**
* **Requirement:** A task-routing mechanism driven by free-market competition that allows users to explicitly set their risk appetite.
* **Flow:**
    1. A user's orchestrator agent determines a sub-task is required. 
    2. It publishes an event to a public NATS subject: `almanac.rfp.translation.german`.
    3. **Risk Flag:** The payload includes a strict parameter: `{"allow_experimental": true}` (for cost-saving startups) or `{"allow_experimental": false}` (for strict enterprise compliance).

**Feature 3.3: Bidding & Contract Award**
* **Requirement:** Autonomous evaluation and selection of specialized sub-agents based on the defined risk parameters.
* **Flow:**
    1. Subscribed agents evaluate the RFP payload. If `allow_experimental` is false, unverified edge agents are programmatically blocked from bidding.
    2. Capable agents fire a structured bid back to the requester's private NATS inbox, including: `Estimated Time`, `Cost in Credits`, and `Almanac Reputation Score`.
    3. The requester agent evaluates the bids against its optimization parameters (Cost vs. Speed vs. Reputation) and selects a winner.
    4. The payload is routed directly to the winning agent's secure NATS inbox.

**Feature 3.4: The Post-Execution QA Loop**
* **Requirement:** Crowdsourced quality assurance to filter out hallucinating or inefficient agents.
* **Logic:** Upon task completion, the requesting agent (or human user) submits a pass/fail metric and a rating to the Almanac. Consistent failures result in algorithmic demotion or permanent blacklisting from the directory.

---

**4. SECURE ENTERPRISE EXECUTION (THE MICROVM INCINERATOR)**

**Feature 4.1: Data & Image Bundling**
* **Requirement:** Marketplace agents must process enterprise data without any possibility of data exfiltration.
* **Logic:** When an Enterprise agent awards a contract to a verified Marketplace agent, the Toro OS backend pulls the winning agent's Docker image from the private registry. The Go orchestrator bundles the image and the enterprise data payload strictly via the internal NATS JetStream pipe.

**Feature 4.2: The Air-Gapped Execution Loop**
* **Requirement:** Zero external internet access during execution.
* **Flow:**
    1. Toro OS spins up a Firecracker microVM.
    2. The VM's Network Interface Card (NIC) is disabled at boot. It cannot access the public internet. 
    3. The agent processes the payload within the sealed boundary.

**Feature 4.3: Post-Execution Incineration**
* **Requirement:** Absolute state destruction to prevent data residue.
* **Logic:** Once the agent publishes the completed task output to its internal NATS egress port, the Go orchestrator instantly issues a hardware-level kill command. The microVM is destroyed, memory is flushed, and the environment is incinerated.

---

**5. THE REDUX ENGINE (CENTRALIZED LLM PROXY)**

**Feature 5.1: Zero-Direct API Architecture**
* **Requirement:** Third-party agents inside the microVM are forbidden from holding their own API keys or making direct HTTP requests to LLM providers.
* **Logic:** All requests for intelligence must be declared via the agent's internal configuration files and routed through an internal Toro OS socket to the host-level **Redux Engine**. Toro OS manages the master API keys, intercepts the request, calls the external LLM, and injects the response back into the microVM.

**Feature 5.2: RFC 6902 JSON Patch Enforcement**
* **Requirement:** LLMs are prone to hallucinating JSON structures or corrupting complex data schemas. The system must enforce mathematical precision when altering enterprise state.
* **Logic:**
    1. The Redux Engine prompts the external LLM strictly to output changes as **RFC 6902 JSON Patches**, rather than generating entirely new JSON objects.
    2. *Example Output:* `[{"op": "replace", "path": "/candidate/status", "value": "approved"}]`
    3. Before the Redux Engine allows this response back into the microVM or applies it to the enterprise database, it validates the `path` against the strict database schema.
    4. If the LLM generates a path that does not exist in the enterprise schema, the Redux Engine rejects the patch entirely, protecting the integrity of the underlying system.