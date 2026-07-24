# Product Requirements Document (PRD)

**Project Name:** Digital Commercial Engine (ToroDB Sales Platform)

**Target Market:** Moroccan B2B SMEs (CHRs, Packaging, Wholesale, Corporate Services)

**Document Version:** 1.0

**Author:** Product & Architecture Team

---

## 1. Executive Summary & Objective

The **Digital Commercial Engine** combines ground-level human relationship building with autonomous AI execution. A local Moroccan Business Development Representative (BDR / *Commercial Terrain*) visits local suppliers, closes the zero-risk performance partnership, and collects their operational catalogs, pricing tables, and rules.

Once ingested into **ToroDB** (the enterprise knowledge memory), autonomous AI agents execute targeted B2B outreach via Email and WhatsApp, handle inbound inquiries in Darija/French/English, generate formal quotes (*devis*), and route hot, ready-to-sign deals back to the supplier for execution.

---

## 2. Target Personas & Stakeholders

| Role | Entity | Key Responsibilities & Objectives |
| --- | --- | --- |
| **Field BDR** | Local Moroccan Team Member | Conducts face-to-face visits (*terrain*), pitches the zero-upfront commission offer, signs partnership agreements, collects PDFs/SOPs/catalogs. |
| **Partner Supplier** | Moroccan Business Owner (*Patron*) | Provides catalog, pricing rules, and minimum margins. Receives verified orders (*Bons de Commande*) and pays success-based commissions. |
| **Target B2B Buyer** | Purchasing Directors, Hotel Managers, Ops Managers | Receives personalized offers, requests quotes, asks product questions, and negotiates terms via Email/WhatsApp. |
| **Digital Commercial Agent** | Automated AI Worker | Ingests ToroDB context, executes outbound campaigns, parses buyer intent, generates precise *devis*, and manages negotiations within preset bounds. |

---

## 3. End-to-End Operating Workflow

```
[ Local Moroccan BDR ] ──► Visits Supplier in-person ──► Signs Zero-Risk Contract
                                                                 │
                                                    Collects Catalogs/SOPs
                                                                 │
                                                                 ▼
[ ToroDB Engine ] ◄────── Ingests PDFs, Margins, Rules & Pricing Tables
        │
        ▼
[ AI Commercial Agent ] ─► Outbound Email / WhatsApp Outreach (Enriched B2B Lists)
        │
        ├── Inbound Buyer Inquiry ──► AI Checks ToroDB ──► Generates Devis / Answers Qs
        │
        └── Deal Reaches "Ready to Sign" ──► Hands Off to Partner Supplier for Execution
                                                                 │
                                                       Commission Earned (10-15%)

```

---

## 4. Key Functional Requirements

### Module 1: Field Onboarding & Document Intake (Human Phase)

* **Mobile Intake Interface:** A lightweight web application for the field BDR to register a new partner business on the spot.
* **Document Scanner & Collector:** Ability to upload or photograph:
* Product catalogs (PDF/Images).
* Rate cards, wholesale tier discounts, and minimum order quantities (MOQ).
* Delivery SLAs, regional coverage (e.g., *Casablanca, Rabat, Marrakech*), and payment terms (*au comptant*, 30 days).
* Company legal details (*ICE, Registre de Commerce*) for contract generation.


* **Partnership Agreement Generator:** Auto-generates a standardized Moroccan legal commission agreement for the business owner to sign digitally or on paper.

---

### Module 2: ToroDB (Enterprise Knowledge Brain)

* **Vector & Structured Ingestion:** Parses unstructured PDFs, Excel price sheets, and French/Arabic text into structured rules.
* **Constraint Engine:** Defines strict boundaries the AI agent cannot violate during negotiation:
* *Floor Price:* Minimum allowed price per unit.
* *Volume Thresholds:* Required quantity to trigger custom discounts.
* *Geographic Rules:* Restricted delivery zones or minimum order values per city.


* **Audit Trail:** Logs every database query made by the AI agent when generating quotes to ensure full transparency.

---

### Module 3: Autonomous Commercial Agent

* **Multi-Channel Dispatch:**
* **Email Engine:** Personalizes cold B2B outreach in business French and English.
* **WhatsApp Business Agent:** Intercepts incoming messages and sends proactive follow-ups in French, English, and Moroccan Darija (Latin/Arabic script).


* **Intent & Context Parser:**
* Categorizes replies (e.g., *Request for Quote*, *Price Objection*, *Delivery Inquiry*, *Unsubscribe*).
* Automatically handles common Moroccan business objections (e.g., requesting sample products, asking for bulk payment terms).


* **Automated Devis (Quote) Generator:** Generates a formal, branded PDF *devis* containing line items, VAT (TVA), delivery terms, and validity dates drawn straight from ToroDB.

---

### Module 4: Deal Handoff & Commission Ledger

* **Hot-Lead Escalation:** When a buyer confirms intent to purchase (*"Send the contract"* or *"Send the bank details for deposit"*), the system alerts the BDR and the partner supplier via WhatsApp notification.
* **Attribution & Commission Tracker:**
* Records the deal status (*Pitched -> Devis Sent -> Order Placed -> Invoice Paid*).
* Calculates platform commission automatically based on settled invoices.



---

## 5. Non-Functional & Localized Requirements

* **Language Support:** Native handling of Business French, English, and Romanized/Standard Arabic/Darija.
* **Low-Bandwidth Mobile Friendly:** Ground intake app must operate reliably over 4G mobile networks throughout industrial zones.
* **Data Security & Privacy:** Isolation of supplier data—ToroDB instance for Supplier A must never leak pricing rules or client lists to Supplier B.

---

## 6. Phase 1 Implementation Roadmap

1. **Sprint 1 (Infrastructure & Field App):** Build lightweight Mobile Document Intake Form & setup ToroDB ingestion pipeline for PDFs and Excel sheets.
2. **Sprint 2 (Core Agent Logic):** Connect LLM to ToroDB with strict JSON constraint enforcement for quote generation and Darija/French response capabilities.
3. **Sprint 3 (Messaging Layer):** Hook up Email sequencer and WhatsApp API endpoints for automated message parsing and thread management.
4. **Sprint 4 (Pilot Launch):** Deploy local BDR in Casablanca (e.g., targeting packaging or office supply wholesalers) to onboard the first 5 partner suppliers.