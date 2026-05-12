# **PRODUCT REQUIREMENTS DOCUMENT (PRD)**

**PROJECT:** Toro OS Influence Graph (The Enterprise Telemetry Engine)

### **1. EXECUTIVE SUMMARY**

The **Influence Graph** is a passive, background data engine that maps the informal, peer-to-peer reality of an enterprise. It operates by silently observing task handoffs, approvals, and cross-platform `@mentions` via the Toro OS NATS JetStream. It translates these operational exhaust fumes into a mathematical property graph within Neo4j. This graph serves as the foundational intelligence layer for downstream AI primitives, capable of tracking true institutional knowledge, measuring human-to-human collaboration velocity, and enforcing thermodynamic decay on obsolete relationships.

---

### **2. SYSTEM ARCHITECTURE (THE TELEMETRY PIPELINE)**

The graph is built entirely out-of-band. It does not block core workflows.

1. **The Event Emitter:** Every Go Worker in the Toro OS ecosystem emits a lightweight metadata payload to a dedicated NATS subject: `toro.telemetry.events`.
* *Example:* `Task_123 (IT_Contract) updated from Draft to Approved by USR-999.`


2. **The Graph Builder Go Worker:** A dedicated, persistent Go Worker subscribes to `toro.telemetry.events`. It acts as the ETL (Extract, Transform, Load) bridge between Postgres state changes and Neo4j graph topology.
3. **The Neo4j Database:** The final storage layer, optimized for traversing highly connected data and executing shortest-path algorithms.

---

### **3. NEO4J SCHEMA DEFINITION**

The schema isolates the actors, the work, and the kinetic energy between them.

**A. Nodes**

* `(:Employee)`
* `user_id` (UUID - Maps to canonical Toro OS ID)
* `department` (String)
* `consultation_capacity` (Integer - Weekly token bucket for downstream AI load balancing)


* `(:TaskType)`
* `name` (String - e.g., "Vendor_Contract", "Expense_Report")
* `base_median_velocity_hrs` (Float - The company-wide average time to complete this task)



**B. Edges (Relationships)**

* `[:REPORTS_TO]` → The rigid, formal hierarchy (Imported once via HR systems/Active Directory).
* `[:INTERACTED_ON]` → A temporary edge. Formed when User A tags or routes a task to User B.
* `[:COLLABORATES_WITH]` → A permanent, weighted edge. Formed only when an interaction leads to a successful outcome.
* *Properties:* `success_rate` (Float 0-1), `avg_velocity_hrs` (Float), `interaction_count` (Integer).


* `[:HAS_EXPERTISE_IN]` → The highest-tier edge. Connects an `Employee` to a `TaskType`.
* *Properties:* `total_completions` (Integer), `rejection_rate` (Float).



---

### **4. EDGE FORMATION & PROMOTION ALGORITHMS**

The Go Worker uses strict mathematical triggers to create and upgrade edges.

**Step 1: The Spark (Temporary Interaction)**

* *Trigger:* User A (Submitter) sends an `Expense_Report` to User B (Manager), or User A `@mentions` User B in the context of the task.
* *Action:* Go Worker writes `(User_A)-[:INTERACTED_ON {task_id: 123, timestamp: XYZ}]->(User_B)`.

**Step 2: The Conversion (Collaboration Proof)**

* *Trigger:* The system observes `Task_123` reaching the `Completed` state.
* *Action:* The Go Worker deletes the temporary `[:INTERACTED_ON]` edge. It creates (or increments) a permanent `[:COLLABORATES_WITH]` edge between User A and User B. It recalculates the `avg_velocity_hrs` for their relationship.

**Step 3: The Promotion (Expertise Designation)**

* *Trigger:* A nightly chron job evaluates all `[:COLLABORATES_WITH]` edges against the `TaskType` baselines.
* *Action:* If User B has successfully participated in `10+` Expense Reports, and their `avg_velocity_hrs` is `30%` faster than the `base_median_velocity_hrs`, and their `rejection_rate` is `< 5%`, the system wires a `[:HAS_EXPERTISE_IN]` edge from User B directly to the `Expense_Report` node.

---

### **5. THE ENTROPY ENGINE (THERMODYNAMIC DECAY)**

To ensure the graph reflects the *current* reality of the company, old relationships must physically wither. Institutional knowledge decays if not actively used.

**The Decay Chron Job:**
A specialized Go Worker runs every Sunday at 00:00 UTC.

1. **Collaboration Decay:** It scans all `[:COLLABORATES_WITH]` edges. If the `last_interaction_date` is older than 60 days, the `interaction_count` weight is reduced by 10%. If the count hits 0, the edge is deleted.
2. **Expertise Revocation:** It scans all `[:HAS_EXPERTISE_IN]` edges. If an Expert has not successfully touched that specific `TaskType` in 90 days, their expertise edge is severed. They must earn it back through new interactions.

---

### **6. SECURITY & DATA PRIVACY CONSTRAINTS**

To ensure total HR compliance and prevent surveillance concerns:

* **Metadata Only:** The Telemetry Worker is strictly prohibited from ingesting, reading, or storing the actual text bodies of emails, Slack messages, or task comments. It only ingests the *metadata* (Sender ID, Receiver ID, Task ID, Timestamp, State Change).
* **Air-Gapped Telemetry:** The NATS `toro.telemetry.events` subject is isolated. LLM Agents cannot subscribe to this raw feed, preventing hallucinations based on incomplete graph construction.