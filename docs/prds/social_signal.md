# **PRODUCT REQUIREMENTS DOCUMENT (PRD) v2.0**

**PROJECT:** Toro OS `Social_Signal` Primitive (Influence Graph & State Extraction)

### **1. EXECUTIVE SUMMARY**

The `Social_Signal` primitive is an advanced read-only Go Tool that queries an enterprise's Neo4j Influence Graph. It abandons rigid bureaucratic routing in favor of observing and suggesting high-efficiency, human-to-human workflows.

To prevent HR violations, suggestions are strictly framed as **"Peer Consultations"** based on data-backed expertise. To prevent the "Tragedy of the Commons" (burning out top performers), the primitive utilizes algorithmic **Human Rate Limiting**. Finally, when a human expert crosses a critical threshold of success, the system triggers **Automated State Extraction**, digitizing their methodology into a reusable AI primitive and shielding them from future manual requests.

---

### **2. NEO4J GRAPH SCHEMA & METRICS**

The database must natively support capacity limits and expertise tracking.

**Nodes:**

* `(:Employee {user_id: UUID, role: String, consultation_capacity: Integer})`
* *Note:* `consultation_capacity` acts as a token bucket (e.g., max 3 per week).


* `(:TaskType {name: String, category: String})`

**Edges (Relationships):**

* `[:COLLABORATES_WITH]` → Tracks human-to-human efficiency.
* `[:HAS_EXPERTISE_IN]` → Connects an `Employee` to a `TaskType`.
* *Properties:* `success_count` (Integer), `avg_velocity_hrs` (Float).



---

### **3. FUNCTIONAL PIPELINE: THE GO WORKER EXECUTION**

#### **Phase 1: The Throttled Query (Human Rate Limiting)**

When an AI Agent requests a social route, the Go Worker executes a Cypher query with strict thermodynamic constraints.

* **The Logic:** Find an intermediary with a high `success_count` in this `TaskType`, who is in the submitter's extended network, **AND** whose `consultation_capacity > 0`.
* **The Load Balancer:** If the #1 expert (Jennifer) has 0 capacity tokens left for the week, the database automatically filters her out and returns the #2 expert (David).
* **Token Deduction:** If the user accepts the tip, the Go Worker deducts 1 token from the expert's bucket. Tokens automatically replenish via a weekly chron job.

#### **Phase 2: The UI Rendering (HR-Safe "Consultation")**

The JSON output from the Go Worker is parsed by the LLM Agent under strict system prompts. The AI is forbidden from using bypass terminology.

* **Allowed Framing:** *Consultation, Advice, Subject Matter Expert, Formatting Review.*
* **UI Output:** *"💡 Tip: You may want to consult David before submitting. He has successfully finalized 8 of these contracts this month and knows exactly how Finance likes them formatted."*
* **Action:** The user manually messages David off-platform.

---

### **4. AUTOMATED STATE EXTRACTION (THE SME SHIELD)**

This is the ultimate endgame of the primitive. When the system identifies a biological node operating at peak efficiency, it extracts that intelligence to protect the human's bandwidth.

**The Trigger Condition:**
A background Go Worker continually monitors the `[:HAS_EXPERTISE_IN]` edges in Neo4j. If an employee's `success_count` on a specific task hits **20 consecutive successful executions**, the "Extraction Event" fires.

**The Extraction DAG:**

1. **Historical Analysis:** A specialized AI Agent is spun up to analyze the exact diffs (changes) the expert made across those 20 historical payloads. It identifies the patterns (e.g., "Jennifer always capitalizes the vendor name, always attaches a W-9, and always adds 'NET30' to the terms").
2. **Primitive Generation:** The system dynamically compiles those rules into a new deterministic JSON schema/prompt wrapper. It effectively creates a new virtual tool: `Jennifer_Contract_Validator`.
3. **The UX Shift (Shield Activation):** From this point forward, when a junior employee drafts an IT contract, the AI no longer suggests bothering Jennifer.
Instead, the UI output becomes:
*"💡 Tip: Jennifer L. is the undisputed expert on this. Instead of bothering her, I analyzed her last 20 successful contracts and applied her exact formatting rules to your draft. Click here to review the changes before formal submission."*

---

### **5. DATA INTEGRITY & SECURITY**

* **Read-Only:** The `Social_Signal` primitive cannot write to Postgres. It only informs the LLM's context window.
* **Decay Function:** A monthly Go Worker sweeps the Neo4j database. If an expert has not successfully completed a specific task in 90 days, their `success_count` degrades, ensuring the AI only extracts and mimics *current* institutional knowledge.