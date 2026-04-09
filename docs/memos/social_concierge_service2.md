 **INTERNAL ENGINEERING & OPS MEMO: TORO OS**
**DATE:** April 8, 2026
**TO:** Engineering (Backend/UI) & Operations
**SUBJECT:** MASTER ARCHITECTURE: Multi-Tenant B2B2B White-Label Growth Engine & HITL Proxy

**1. EXECUTIVE SUMMARY**
Toro OS is transitioning from a single-player accounting tool into a **Multi-Tenant B2B2B Operating System**. We are adopting the "GoHighLevel + Jasper AI" playbook. We are empowering our CPAs to white-label our AI marketing and HITL (Human-in-the-Loop) engagement services and resell them directly to their SMB clients (plumbers, dentists, e-commerce) as a "Done-For-You Growth Engine." This shifts Toro OS from an operational expense for the CPA into a massive, uncancelable profit center.

**2. THE 3-TIER RBAC DATABASE SCHEMA (MULTI-TENANCY)**
To execute this safely, the database and UI layers must be strictly siloed using Role-Based Access Control (RBAC). The system architecture requires three distinct physical UI portals:

* **Tier 1: The SuperAdmin (Toro OS Internal HQ)**
    * **Users:** Toro OS Engineers and the Moroccan HITL "Engagement Sniper" operators.
    * **Access:** Global. Operators log into a secure, abstracted proxy Kanban board. They see anonymized marketing tasks and incoming social media comments across all CPA agencies and sub-accounts. They never see raw passwords.
* **Tier 2: The Agency Admin (The CPA Portal)**
    * **Users:** The CPA Firm owners. 
    * **Access:** They see an aggregate dashboard of all their SMB clients. They can provision new "Growth Engine" sub-accounts, set their own resale pricing, and view total ROI generated across their portfolio. 
    * **White-Labeling:** The CPA uploads their own firm's logo and sets a custom domain mapping (e.g., `portal.smithcpa.com`).
* **Tier 3: The Sub-Account (The SMB End-User Portal)**
    * **Users:** The Plumber, the Dentist, the E-commerce owner.
    * **Access:** Strictly limited to their own financial ledger, their own social media queue, and their own performance metrics. 
    * **Branding:** The Toro OS logo is completely invisible. The SMB only sees the CPA's branding. To the SMB, the CPA built this powerful software.

**3. LEDGER-DRIVEN AI TRIGGERS (THE JASPER HYBRID)**
Our competitive advantage is context. Standard marketing AI guesses; our AI acts on ground-truth financial data.
* **The Trigger Engine:** The Go backend continuously monitors the SMB sub-account ledgers. If it detects a specific financial pattern (e.g., "Revenue down 20% in Q3" or "High volume of crypto transactions"), it automatically triggers a webhook to the LLM.
* **The Generation:** The AI drafts a hyper-targeted promotional email blast and social media post designed to fix that exact financial anomaly (e.g., "End of Q3 Discount Drive").
* **The Approval Loop:** The AI draft is pushed to the Tier 3 (SMB) or Tier 2 (CPA) dashboard for one-click approval.

**4. THE INBOUND HITL PROXY (THE ENGAGEMENT SNIPER)**
When the outbound AI marketing generates engagement, the Moroccan operational team handles the fulfillment to bypass API bot restrictions.
* **OAuth Sandboxing:** The SMB authenticates their Facebook/LinkedIn accounts via OAuth into the Tier 3 portal. 
* **Event-Driven UI:** When a customer comments on the SMB's post, the official platform APIs push a webhook directly to the Tier 1 SuperAdmin proxy board.
* **AI Exoskeleton Fulfillment:** The Moroccan operator clicks "Draft Reply," the AI generates a context-aware response based on the SMB's specific business data, the operator verifies it against hallucinations, and clicks "Publish." The Go server pushes it back through the API.

**5. SECURITY & COMPLIANCE MANDATE**
Strict tenant isolation must be enforced at the database level using Postgres Row-Level Security (RLS). A query executed by a Tier 3 user must structurally be incapable of accessing data from another Tier 3 sub-account or Tier 2 agency.
