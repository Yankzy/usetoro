# Technical Product Requirement Document (PRD)

## Project: Universal Context-Routing Engine & Company World Model Core

**Document Version:** 4.0.0

**Confidentiality Status:** Restricted Enterprise Access

**Core Architectural Mandate:** Replace human information-routing protocols with automated, local graph-scheduling infrastructure.

---

## 1. Product Vision & Strategic Positioning

The current corporate hierarchy is an obsolete 2,000-year-old information routing protocol. Historically, middle management layers were created to handle human communication limitations, where an individual leader could effectively maintain a span of control over only three to eight direct relationships.

This platform replaces human intermediaries with a machine-readable nervous system, treating every email, chat log, transaction, deployment update, and corporate artifact as a continuous signal to assemble a living "Company World Model".

```
  [ Raw Unstructured Enterprise Streams ]
   (Slack, Email, Stripe, GitHub, Docs)
                     │
                     ▼
  ┌─────────────────────────────────────┐
  │     Kodiak Universal Ingestion      │
  └──────────────────┬──────────────────┘
                     │
                     ▼
  ┌─────────────────────────────────────┐
  │     AI Sensor Layer (The Brain)     │
  └──────────────────┬──────────────────┘
                     │
                     ▼
  ┌─────────────────────────────────────┐
  │ Local Topological Sort (Conductor)  │
  └──────────┬───────────────────┬──────┘
             │                   │
             ▼                   ▼
     [ Shadow Ledger ]   [ Corporate Mesh ]

```

### 1.1 Dual-Front Market Execution

The core engine drives two distinct, sovereign commercial frontends through header-based API routing gates:

* **nodebookkeeping.com (The Transactional Retail Hook):** A self-service sandbox targeting freelancers and micro-SMBs. Users perform single-use document injections (bank statements, receipt batches, chat exports) to generate instant, mathematically balanced trial reports for **$10 per run**. This serves as a friction-free user capture and training pipeline.
* **fignode.com (The Institutional Corporate Workspace):** A premium enterprise automation environment commanding a flat license fee of **$2,500 per month** per organization. It continuously monitors background webhooks from linked operational data lines, compiling real-time intelligence for executives, CFOs, and venture partners.

---

## 2. Infrastructure Input Specification: The Universal Context Token

To achieve scalability across hundreds of millions of corporate data points, the ingestion pipeline flattens the distinction between a financial ledger entry and an operational communication message. Every raw material item hitting the gateway is instantly mapped into a structured entity named the **Context Token**.

### 2.1 The Data Representation Contract

Every asset inside the system must be completely defined upon ingestion. This eliminates the computational overhead of re-reading raw files through a large language model context window every time a user asks a question, shifting query costs from an expensive $O(n)$ retrieval run down to a fast, cost-stable algorithmic join.

Every Context Token is composed of a rigid **Graded Tuple**:

* **The Identifier:** A unique, unalterable system fingerprint generated from the raw payload hash.
* **The Namespace Anchor:** A strict tenant isolation pointer matching the organization's unique workspace key (such as a QuickBooks Online connection string or a dedicated session token).
* **The Channel Source:** The original communication platform format (e.g., Stripe, Slack, Google Mail, GitHub, Jira).
* **The Normalized Entity:** The verified counterparty or structural focus text (e.g., "Vendor: Adobe", "Project: Alpha").
* **The Invariant Numerical Value:** The primary transaction amount, quantitative variable, or code delta change size (set to 0.00 if the event is purely qualitative narrative text).
* **The Time Envelope:** The exact, verified historical date and hour the event physically took place in the real world.
* **The Dependency Vector:** An explicit array containing the system fingerprints of the prerequisite parent events that must execute before this token can settle.

---

## 3. The Local Graph Execution Engine

Large-scale corporate data processing breaks down if an architecture tries to build and sort one giant, interconnected global graph database at runtime. Instead, this engine divides enterprise data into localized clusters containing only **2 to 5 nodes** per business event family.

The system scales to hundreds of millions of records by running thousands of independent, lightning-fast topological calculations concurrently in separate memory channels.

```
       [ Incoming Transaction / Event Batch ]
                         │
                         ▼
       [ Extract 2-5 Node Local Sub-Graph ]
                         │
                         ▼
        [ Calculate Local Node In-Degrees ]
                         │
                         ▼
       Is In-Degree = 0? ──► No  ──► [ Route to Context Gap Table ]
               │
              Yes
               ▼
   [ Execute Target Ledger / Mesh Write ]
               │
               ▼
  [ Decrement Dependent Child Node Counts ]

```

### 3.1 Step 1: The AI Sensor Layer (The Dependency Painter)

When an unstructured data stream hits the NATS JetStream log buffer, the AI Sensor Layer analyzes the text blocks. It does not touch the production database or make execution decisions. Its sole operational directive is to **discover relationships and assign the dependency array** inside the Graded Tuple.

> **Operational Scenario:** The ingestion gateway receives an automated Stripe clearing notice showing $1,200 has left the corporate bank account. The AI Sensor analyzes the transaction string, recognizes the text payload matches a routine vendor expense pattern, and declares: *"This is a cash outflow event. I predict it mathematically depends on an open, verified accounts payable obligation node."* It appends the target invoice id into the token's dependency array.

### 3.2 Step 2: The Local Topological Sort Engine (The Traffic Conductor)

Once the dependency arrows are drawn by the AI Sensor, the Go engine isolates the 2-to-5 node cluster and calculates the exact `InDegree` (the number of incoming prerequisite constraints pointing at a node) for every vertex in the micro-graph.

The engine executes a localized version of **Kahn's Algorithm** to enforce chronological bookkeeping safety:

* **Source Queue Processing:** Any node inside the local cluster with an initial `InDegree = 0` (such as a vendor invoice or a management policy email) is pushed to the immediate execution queue.
* **Atomic State Collapse:** The engine pops the zero-In-Degree node, updates the database table, and decrements the dependency counter of its linked child node by 1.
* **Dependent Release:** Once the child node's `InDegree` drops to 0, it enters the execution queue, allowing the Stripe cash settlement entry to write to the ledger with perfect historical accuracy.

### 3.3 Step 3: Automated Context Gap Handling

If the engine scans the database and discovers that an AI-declared prerequisite parent node does not exist (e.g., a Stripe payment posted over the weekend, but the human user hasn't uploaded the vendor invoice), the engine applies an explicit structural penalty:

* The system sets the node's `InDegree` to 1, blocking it from entering the active Kahn processing loop.
* The orphaned vertex is cleanly routed to a temporary holding matrix marked as a **Context Gap**.
* It sits frozen in a safe waiting pattern, preventing the general ledger from creating unbalanced or messy suspense records.

---

## 4. Multi-Tenant Relational Storage Partitioning

To ensure absolute enterprise privacy and instant multi-table queries without full database scans, the storage core separates structured ledger data from unstructured narrative text across two relational tables inside AlloyDB Omni.

### 4.1 Table 1: The Shadow General Ledger (SGL)

This table acts as the unshakeable financial anchor of the corporate model. It strictly rejects any loose, unallocated, or single-sided entries, enforcing standard US GAAP double-entry rules at the schema constraint boundary.

| Column Name | Data Type | Constraint / Validation Rule |
| --- | --- | --- |
| **entry_id** | UUID | Primary Key (Auto-Generated) |
| **qbo_realm_id** | VARCHAR(50) | Not Null (Primary Tenant Boundary Index) |
| **session_id** | UUID | Nullable (Populated for nodebookkeeping.com runs) |
| **account_code** | VARCHAR(10) | Not Null (e.g., '1010' Cash, '2010' Liability) |
| **debit** | NUMERIC(14,2) | Default 0.00 (Must be mutually exclusive with credit) |
| **credit** | NUMERIC(14,2) | Default 0.00 (Must be mutually exclusive with debit) |
| **recorded_at** | TIMESTAMP WITH TZ | Not Null (Chronological Sorting Key) |
| **description** | TEXT | Not Null (Raw Source Context Summary) |

### 4.2 Table 2: The Corporate Knowledge Mesh

This table records the narrative, operational, and non-financial context streams of the company, acting as the machine-readable nervous system that maps out why work is happening across teams.

* **artifact_id:** Unique object key.
* **qbo_realm_id:** Primary tenant identifier, allowing high-speed relational joins straight across to the financial SGL table.
* **channel_source:** The identifier tag tracking the ingestion origin (Slack, Email, GitHub).
* **normalized_entity:** The target project, client, or cost center tag used for high-level dashboard filtering.
* **context_narrative:** Clean, raw conversation text stripped of processing metadata.
* **citation_link_hash:** A cryptographic audit index linking back to the precise page, slide, or chat timestamp where the event originated.

---

## 5. Functional Brand Workflows & Operational Gates

```
  [ Local Context Gap Detected (In-Degree > 0) ]
                        │
         ┌──────────────┴──────────────┐
         ▼                             ▼
   [ Route Request: ]            [ Route Request: ]
  nodebookkeeping.com               fignode.com
         │                             │
         ▼                             ▼
 [ Freeze Processing Queue ]   [ Package Event Payload ]
         │                             │
         ▼                             ▼
 [ Render Visual Blocker ]     [ Stream to Accountant Dashboard ]
         │                             │
         ▼                             ▼
 [ Display $10 Up-Sell Gate ]  [ CPA Confirms Account Code ]
                                       │
                                       ▼
                               [ Decrement In-Degree ]

```

### 5.1 The nodebookkeeping.com Execution Gate

* **The Trigger:** A retail user uploads an unstructured batch of files, and the Local Topological Sort engine encounters an unresolved dependency loop or a missing invoice constraint (`InDegree > 0`).
* **The Operational Guardrail:** The backend completely locks the execution loop for that specific cluster. No automated scripts are allowed to guess the account category.
* **The UI Experience:** The web interface renders a visual blocker showing the exact mismatched bank line and prompts an up-sell notification: *"We identified an unallocated financial event. Upload the missing context document manually, or upgrade to a partner accounting firm on Fignode to automate your complete company tracking."*

### 5.2 The fignode.com Enterprise Workflow

* **The Trigger:** A background webhook detects a data variance or a tracking gap inside a permanently linked client asset channel.
* **The Operational Guardrail:** The system completely avoids the overhead of internal customer service teams. It compiles the unsequenced transaction data, packages the event into a clean notification payload, and pushes it directly into the managing CPA's desktop workspace queue.
* **The UI Experience:** The accountant reviews the pre-compiled optimization suggestions generated by the database, selects the correct ledger code, and submits the entry. The system drops the token's dependency count to zero, triggers the Kahn execution loop, and logs a balanced entry directly into both the Shadow General Ledger and the client's external accounting software simultaneously.

---

## 6. End-to-End System Performance Metrics

To maintain a responsive platform across both retail and high-end firm domains, the core coordination layer enforces three strict performance boundaries:

* **Macro Cash Summarization:** Level-1 dashboard cash position updates must return data to the frontend in under **15 milliseconds** by querying the isolated ledger tables without scanning historical raw context logs.
* **Localized Sequence Resolution:** Sorting and validating a 2-to-5 node local transaction cluster must take less than **2 milliseconds** of core engine runtime processing power.
* **Cross-Portfolio Corporate Queries:** Complex organizational questions requiring code-driven joins across the `shadow_general_ledger` and `corporate_knowledge_mesh` tables must deliver completely cited, hallucination-free answers to the user interface within **18 seconds**, entirely bypassing raw document re-reading loops.