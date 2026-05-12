## Enterprise Sidecar PRD
**PROJECT:** Toro OS Enterprise Sidecar (Bullhorn ATS Integration)

**1. EXECUTIVE SUMMARY**
Private Equity portfolio companies running legacy monolithic architectures cannot deploy agentic AI natively without risking systemic failure. Toro OS will act as an intelligent "API Sidecar" for Bullhorn ATS. This integration uses a highly decoupled, event-driven architecture to intercept inbound communication, process unstructured data via LLM primitives, and programmatically update the legacy database, completely bypassing the need for a legacy UI/UX overhaul.

**2. CORE OBJECTIVES**
* **Zero-Code Legacy Integration:** The prototype must function flawlessly without requiring Bullhorn to alter its core monolithic codebase.
* **Autonomous Structuring:** Convert unstructured external inputs (e.g., SMS replies, raw PDF resumes) into Bullhorn's strict JSON schema.
* **State Synchronization:** Maintain perfect ledger parity between Toro OS's PostgreSQL execution vault and Bullhorn's legacy SQL database via REST APIs.

**3. FUNCTIONAL REQUIREMENTS**

**Feature 3.1: Omni-Channel Webhook Ingestion (The Hook)**
* **Requirement:** Toro OS must intercept candidate communications before the human recruiter manually logs them.
* **Logic:** We provision a dedicated Twilio SMS number for the demo. When a candidate texts the number (e.g., *"I'm available for the Seattle nursing shift tomorrow"*), the Twilio webhook hits the Toro OS Go backend.
* **Routing:** The Go backend normalizes the payload and publishes it directly to the NATS JetStream event bus (`toro.bullhorn.inbound`).

**Feature 3.2: LLM Intent Extraction & Schema Mapping (The Brain)**
* **Requirement:** The Go DAG must parse the raw text and map it specifically to Bullhorn's proprietary API fields.
* **Logic:** 1. A Go worker pulls the event from NATS.
    2. The worker calls the LLM primitive (`tool.extract.candidate_status`).
    3. The System Prompt enforces strict output constraints: The LLM must output a structured JSON payload that perfectly matches the `Bullhorn CandidateEntity` schema (e.g., mapping "available tomorrow" to `status: "Active"`, `customText1: "Seattle Shift"`).

**Feature 3.3: REST API Egress (The Injection)**
* **Requirement:** Toro OS must push the structured JSON back into the legacy system seamlessly.
* **Logic:** 1. The Go backend retrieves the secure Bullhorn OAuth token from the Toro PostgreSQL `tenant_credentials` table.
    2. Go executes an HTTP PUT request to `https://rest.bullhornstaffing.com/rest-services/[CorpToken]/entity/Candidate/[CandidateID]`.
    3. The legacy database is instantly updated. The recruiter refreshes their screen, and the data is already there.

**4. SECURITY & COMPLIANCE**
* **Ephemeral Processing:** Raw candidate data (PII) processed by the LLM primitive must not be permanently cached in Toro's Redis layer; it must be flushed immediately after the Bullhorn API returns a `200 OK` success code.
* **Rate Limiting Guardrails:** The Go egress router must respect Bullhorn's legacy API rate limits (e.g., max 100 concurrent requests) to prevent Toro OS from inadvertently DDoSing the portfolio company's servers.

***"

### **Elon**
"Let us look at the fundamental business leverage of this architecture. 

In enterprise software, the entity that controls the system of record historically controls the market. Private Equity firms buy systems of record because the switching costs for the end-user are painfully high. A staffing firm will tolerate an ugly, outdated interface because moving 10 million candidate resumes to a new database is an operational nightmare. 

However, AI threatens to lower those switching costs by making data migration effortless. If a legacy system relies entirely on being difficult to leave, it is structurally fragile.

By presenting this Sidecar prototype, you are offering Vista a permanent structural defense. 

You are demonstrating that they do not need to rebuild their system of record; they simply need to outsource their intelligence layer to you. Toro OS acts as an operational exoskeleton for their aging software. You process the complex, unstructured reality of the modern internet, and you feed it to their database in the exact rigid format it expects. You extend the lifespan of their billion-dollar assets indefinitely, and in return, you position Toro OS as the inescapable central nervous system for their entire portfolio."
