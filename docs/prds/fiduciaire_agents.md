# PRODUCT REQUIREMENT DOCUMENT (PRD)

## Project: Specialized AI Teammates for Moroccan Fiduciaires (*Cabinet Comptable*) & Standalone Data Clerk Engine

---

## 1. Executive Summary & Core Philosophy

### 1.1 The "Trojan Horse" Go-To-Market Strategy
In the Moroccan accounting landscape (*Cabinets Comptables / Fiduciaires*), operations are strictly hierarchical, paper-heavy, and governed by relentless regulatory deadlines (monthly TVA filings, quarterly IR/IS installments, and monthly CNSS *Damancom* declarations). Firm leadership (*L'Expert-Comptable*) is naturally risk-averse when it comes to external-facing automation, fearing client miscommunication or regulatory compliance breaches.

Attempting to sell an external, autonomous agent that directly communicates with clients from Day 1 encounters massive friction. The **Trojan Horse Strategy** flips this dynamic:
1. **Phase 1 (Internal Co-Pilots):** Deploy 5 specialized AI "teammates" directly into the hands of internal staff (*l'aide-comptable*, *le chargé de clientèle*, *le gestionnaire de paie*, *l'auditeur*, and *le chef de mission*). This immediately removes 60–80% of daily manual grunt work inside the firm.
2. **Phase 2 (Trust Acceleration):** Once internal staff and management experience zero-error pre-keying, automated compliance validation, and instant document linking, they develop deep trust in the AI's cognitive capabilities.
3. **Phase 3 (Platform Expansion & A2A Network):** Moving to the autonomous platform phase becomes a frictionless pitch:
   > *"Your internal agents currently spend 30% of their day emailing and calling your clients' secretaries. Let's deploy an external agent for your Fiduciaire on our platform. Your SME clients can deploy their own agents, and the two platforms will autonomously exchange invoices, resolve reconciliation gaps, and verify tax compliance in the background."*

---

## 2. Practical Ingress: Postmark Inbound Email Workflow

> [!TIP]
> **Client-Facing Reality:** Business owners (*les patrons*) will not log into complex portals to upload invoices. They forward emails or attach photos from their phone.

### The Postmark Inbound Pipeline
1. **Dedicated Inbound Alias:** Each Fiduciaire (or client company) receives a dedicated inbound email address (e.g. `factures-clientA@inbound.usetoro.com` or `saisie@fiduciaire-example.ma`).
2. **Postmark Webhook Trigger:** When an SME client emails or forwards invoices, receipts, or PDF bank statements, Postmark receives the message and immediately posts a structured JSON payload to Toro's `ingress_interceptor.go` endpoint.
3. **Attachment & Payload Extraction:** Toro extracts raw PDF/image attachments, sender metadata, email subject, and body text.
4. **NATS JetStream Dispatch:** Formats a TAP Envelope on `events.inbound_email.received` and dispatches it to the **Standalone Data Clerk Engine (`sub_dags/standalone_data_clerk.yml`)**.
5. **Auto-Confirmation Reply via Postmark:** Upon completion, the system sends an automatic receipt email back to the sender detailing processed documents and flagging any unreadable attachments.

```
┌──────────────────────────┐
│  Client / Patron Email   │ (Sends invoice PDF / Receipt image)
└────────────┬─────────────┘
             │
             ▼
┌──────────────────────────┐
│  Postmark Inbound Engine │ (Inbound Webhook HTTP POST JSON)
└────────────┬─────────────┘
             │
             ▼
┌──────────────────────────┐
│ Ingress Interceptor / TAP│ (Extracts attachments & dispatches NATS event)
└────────────┬─────────────┘
             │
             ▼
┌──────────────────────────┐
│ Standalone Data Clerk    │ (Runs OCR, Normalization & ToroDB Alias Matching)
│         Sub-DAG          │
└────────────┬─────────────┘
             │
             ▼
┌──────────────────────────┐
│ Sage / Ciel Pre-Keying   │ (1-Click CSV Import File for L'Aide-Comptable)
└──────────────────────────┘
```

---

## 3. Executive MVP Recommendation: Standalone Data Clerk Engine

> [!IMPORTANT]
> **Recommended Initial MVP Demo:** **Postmark Inbound Email ➔ Standalone Data Clerk Engine (*L'Agent Saisie*)**

### Architectural Pivot: Cross-Domain Sub-DAG Design
Data entry is not an accounting-only problem. Ingestion and field extraction from unstructured documents (PDFs, scans, photos, forms) is a universal bottleneck spanning:
* **Accounting / Fiduciaire:** Invoices, receipts, bank statements ➔ Sage/Ciel ledger pre-keying via Postmark email ingress.
* **Human Resources (HR):** CIN national IDs, passports, diplomas, resumes ➔ Employee profile creation.
* **Healthcare / Medical:** Lab reports, prescriptions, insurance forms ➔ Patient EHR intake.
* **Legal & Notary:** Contracts, property deeds, court summons ➔ Case file indexing.
* **Logistics & Trade:** Bill of lading, customs declarations, shipping manifests ➔ Transport tracking.

Instead of hard-coding *L'Agent Saisie* strictly inside accounting logic, the **Data Clerk is architected as a standalone, domain-agnostic Sub-DAG (`sub_dags/standalone_data_clerk.yml`)**. Any domain workflow can invoke this standalone engine as a composable child DAG to execute OCR, key-value normalization, entity resolution, and confidence validation.

---

## 4. Architecture & Sub-DAG Engine Orchestration

All 5 agents run natively on Toro's **Autonomous Semantic Engine (ASE)** (`go/internal/erp/ase`). The ASE treats incoming work items not as passive database rows, but as stateful Goroutine micro-agents executing directed graphs (DAGs) backed by Redux state validation, NATS JetStream event routing, and Redis in-memory storage.

### Sub-DAG Composition Topology

```
                      ┌───────────────────────────────────────────┐
                      │        Parent Domain Workflow DAG         │
                      │  (Accounting / HR / Legal / Healthcare)   │
                      └─────────────────────┬─────────────────────┘
                                            │
                                            ▼
                      ┌───────────────────────────────────────────┐
                      │   Standalone Data Clerk Engine (Sub-DAG)  │
                      │  [1. OCR Layout Parsing & Structuring]    │
                      │  [2. Key-Value & Amount Normalization]   │
                      │  [3. Entity Alias DB Resolver (ToroDB)]   │
                      │  [4. Math & Redux Schema Validation]      │
                      └─────────────────────┬─────────────────────┘
                                            │
                 ┌──────────────────────────┼──────────────────────────┐
                 ▼                          ▼                          ▼
      ┌──────────────────────┐   ┌──────────────────────┐   ┌──────────────────────┐
      │  Accounting Ingest   │   │     HR Intake        │   │    Legal / Medical   │
      │  (Invoices ➔ Sage)   │   │  (IDs ➔ Employee)    │   │  (Deeds ➔ Case File) │
      └──────────────────────┘   └──────────────────────┘   └──────────────────────┘
```

---

## 5. Detailed Specifications of the 5 Specialized Teammates

---

### Agent 1: Ingestion & Pre-Keying Agent (*L'Agent Saisie*)

* **Human Peer:** *L'Aide-Comptable / Agent de Saisie*
* **Daily Pain:** Spending 60% of the day manually typing data from paper invoices, receipts, and PDF bank statements into Sage or Ciel.
* **Core Function:** Triggered via **Postmark Inbound Email**, invokes the **Standalone Data Clerk Engine Sub-DAG**, normalizes financial values to MAD, performs vendor alias resolution using ToroDB vector lookup, pre-codes line items against client CoA rules, and generates 1-click Sage import files (.CSV/.XLSX).

```
[ingress: Postmark Inbound Webhook]
           │
           ▼
[parent: pcm_ingestion_saisie]
           │
           ▼
[sub_dag_call: standalone_data_clerk]
    ├── [ocr_layout_parser] ──────── (Raw text extraction & confidence scoring)
    ├── [key_value_normalizer] ───── (Date format, currency to MAD, tax totals)
    └── [entity_alias_resolver] ──── (ToroDB Vector Hydrator: "IAM" ➔ "Maroc Telecom SA")
           │
           ▼
[pre_coding_mapper] ──────────────── (Client CoA Rule Match / Historical Vendor Memory)
           │
           ▼
[sage_export_formatter] ──────────── (Output: Sage 100 / Ciel Import CSV)
           │
           ▼
[postmark_confirmation_reply] ────── (Sends summary email confirmation back to client)
```

* **DAG Configuration & Reused Code:**
  * **Sub-DAG File:** `sub_dags/standalone_data_clerk.yml` (Universal OCR & Entity Extraction)
  * **Parent DAG File:** `dags/pcm_ingestion_saisie.yml` (Accounting Sage Export Wrapper)
  * **Domain Tools:** `email_tools.go`, `pcm_bank_reconciliation_tools.go`, `bookkeeping_tools.go`
  * **Vector Store:** `vector_hydrator.go` (Semantic similarity search on vendor names, ICE, and IDs)
  * **Output Format:** Universal JSON Payload ➔ Standardized Sage 100 PNM/CSV import file.

---

### Agent 2: Document Retrieval Agent (*L'Agent Relance & Collecte*)

* **Human Peer:** *Le Chargé de Clientèle / Secrétaire*
* **Daily Pain:** Chasing SME business owners (*les patrons*) repeatedly via phone calls and emails to collect missing invoices, bank receipts, and signed documents before monthly TVA deadlines.
* **Core Function:** Scans bank statement line items against accounting records, flags missing supporting documents, initiates multi-channel outreach (WhatsApp/Email via Postmark) in polite Business French or Darija, enters an ASE `HOLD` state waiting for client attachments, and routes client document replies through the **Standalone Data Clerk Sub-DAG** for automatic linking.

```
[ledger_unmatched_scanner]
           │
           ▼
[missing_voucher_detector] ─── (Flags Debit/Credit lines missing Facture/Reçu)
           │
           ▼
[outreach_message_composer] ── (Generates contextual Darija/French message)
           │
           ▼
[channel_dispatcher] ───────── (TAP Twilio WhatsApp Worker / Postmark SMTP Worker)
           │
           ▼
[response_listener_gate] ──── (ASE Node Enters HOLD_CLIENT_RESPONSE state)
           │ (On Client Reply via Postmark: Image/PDF Ingest)
           ▼
[sub_dag: standalone_data_clerk] (Parses reply document & extracts metadata)
           │
           ▼
[attachment_indexer] ───────── (Attaches file to ledger transaction & notifies Saisie Agent)
```

* **DAG Configuration & Reused Code:**
  * **DAG File:** `dags/document_relance.yml`
  * **Domain Tool:** `email_tools.go` & `domain_tool.go` (Async `HOLD` state resumption)
  * **TAP Workers:** `workers.twilio_whatsapp`, `workers.postmark_email`
  * **State Persister:** `accounting_store.go` (Tracks pending document requests per tenant).

---

### Agent 3: Payroll & CNSS Specialist (*L'Agent Paie & Social*)

* **Human Peer:** *Le Gestionnaire de Paie*
* **Daily Pain:** Manual calculations of monthly pay slips (*bulletins de paie*), tracking employee join/leave updates, and preparing mandatory CNSS (*Damancom*) batch upload files.
* **Core Function:** Ingests monthly client attendance/salary adjustment sheets (received via Postmark email or portal), computes statutory deductions (IR, CNSS employee 4.48% / employer 21.09%, AMO) per current Moroccan labor law, generates PDF pay slips, and formats batch upload files required for the *Damancom* portal.

```
[timesheet_ingestor] (Invokes standalone_data_clerk sub-DAG on attendance files)
           │
           ▼
[moroccan_tax_labor_engine] ── (Calculates Gross, IR, CNSS 4.48%/21.09%, AMO, Net MAD)
           │
           ▼
[bulletin_pdf_generator] ───── (Renders official PDF pay slips per employee)
           │
           ▼
[damancom_batch_serializer] ─ (Generates CNSS Damancom portal compliant XML/CSV)
```

* **DAG Configuration & Reused Code:**
  * **DAG File:** `dags/payroll_cnss.yml`
  * **Domain Tool:** `bookkeeping_tools.go` (Extended for payroll math validation)
  * **Guardrails:** `math.go` (Strict floating-point calculations to prevent rounding drift)
  * **Output Format:** Damancom XML batch file & PDF Pay Slips bundle.

---

### Agent 4: Tax & Audit Verification Agent (*L'Agent Contrôle Fiscal & TVA*)

* **Human Peer:** *L'Auditeur / Collaborateur Comptable*
* **Daily Pain:** Manually cross-checking invoices before tax filings to ensure compliance (checking ICE validity, TVA rates, cash payment limits, and duplicate entries) to avoid costly DGI (*Direction Générale des Impôts*) audit penalties.
* **Core Function:** Validates vendor ICE (*Identifiant Commun de l'Entreprise*) numbers against government records, verifies invoice TVA rates (20%, 14%, 10%, 7%, 0%) against transaction types, enforces deductibility rules, and flags high-risk transactions (e.g. cash payments > 5,000 MAD).

```
[invoice_compliance_ingestor]
           │
           ▼
[ice_registry_verifier] ────── (Validates vendor ICE against DGI lookup database)
           │
           ▼
[tva_rate_auditor] ─────────── (Cross-checks invoice TVA % vs PCM Category rules)
           │
           ▼
[audit_risk_detector] ──────── (Detects duplicate invoice IDs & cash threshold > 5,000 MAD)
           │
           ▼
[compliance_report_exporter] ─ (Generates Audit Summary Report & DGI risk score)
```

* **DAG Configuration & Reused Code:**
  * **DAG File:** `dags/tax_audit_tva.yml`
  * **Domain Tool:** `pcm_bank_reconciliation_tools.go`
  * **Classifier:** `bookkeeping_classifier.go` (Rule-based compliance evaluation)
  * **Output Format:** Executive TVA Compliance & Risk Audit Report.

---

### Agent 5: Financial Advisory & Reporting Agent (*L'Agent Synthèse & Conseil*)

* **Human Peer:** *Le Chef de Mission / Manager Comptable*
* **Daily Pain:** Writing monthly financial digests and executive summaries for SME client owners who find raw accounting balance sheets (*Grands Livres*) unintelligible.
* **Core Function:** Reads monthly ledger outputs from ToroDB, computes key performance indicators (cash flow trends, top cost drivers, working capital *BFR*, upcoming TVA/IS tax liabilities), and drafts a clean 1-page executive summary in French (or Darija) alongside draft email replies to client accounting inquiries dispatched via Postmark.

```
[ledger_data_aggregator] ───── (Fetches trial balance & transaction summaries from ToroDB)
           │
           ▼
[financial_kpi_analyzer] ───── (Computes Cash Flow Trends, BFR, and Tax Liabilities)
           │
           ▼
[executive_summary_drafter] ── (Generates 1-page French/Darija executive summary)
           │
           ▼
[client_qna_responder] ─────── (Drafts responses for client accounting questions)
```

* **DAG Configuration & Reused Code:**
  * **DAG File:** `dags/financial_synthese.yml`
  * **Domain Tool:** `email_tools.go` & `accounting_store.go`
  * **Vector Store:** `vector_store.go` (Retrieves historical client context & preferred reporting tone)
  * **Output Format:** Structured 1-Page PDF Executive Digest & Draft Email Reponses.

---

## 6. Technical Implementation & Component Matrix

| Agent / Engine | Architecture Level | Primary Function | Ingress / Input | Output | Reused ASE / TAP Component |
| --- | --- | --- | --- | --- | --- |
| **Standalone Data Clerk Engine** | **Sub-DAG** (`sub_dags/standalone_data_clerk.yml`) | Universal OCR Parsing & Field Normalization | Unstructured Scans, PDFs, Images (Postmark / API) | Structured JSON with Field Confidence Scores | `vector_hydrator.go`, TAP OCR Worker, Math Guardrails |
| **1. Saisie** | Parent Accounting DAG | Vendor Matching & Sage Import Pre-Keying | Postmark Inbound Webhook ➔ Data Clerk Sub-DAG | Sage/Ciel CSV/XLSX Import File + Postmark Confirmation Email | `pcm_bank_reconciliation_tools.go`, `email_tools.go` |
| **2. Relance** | Parent Accounting DAG | Chasing missing invoices & documents | Bank statement line items without vouchers | Complete Linked Document Folders | `email_tools.go`, Postmark SMTP, TAP Twilio WhatsApp Worker, ASE `HOLD_` state |
| **3. Paie** | Parent Payroll DAG | Labor math, CNSS & Pay Slip generation | Monthly attendance & salary change sheets | Damancom XML & PDF Pay Slips | ASE Batch Engine, `math.go`, PDF Worker |
| **4. Contrôle** | Parent Compliance DAG | ICE validation, TVA & risk audit | Ingested invoices & ledger entries | Audit Risk Report & Compliance Summary | `bookkeeping_classifier.go`, DGI ICE Lookup Service |
| **5. Synthèse** | Parent Advisory DAG | Financial digests & advisory drafts | Closed monthly ledger from ToroDB | 1-Page Executive Summary & Postmark Email Drafts | ToroDB Queries (`internal/database`), `vector_store.go` |

---

## 7. Verification Plan & Quality Assurance

### 7.1 Postmark Ingest & Sub-DAG Integration Testing
* **Webhook Payload Test:** Simulate Postmark JSON webhook payload containing base64 encoded invoice PDFs to test end-to-end processing:
  ```bash
  go test ./internal/erp/ase -v -run TestPostmarkIngress
  ```
* **Sub-DAG Composition Tests:** Verify embedding `sub_dags/standalone_data_clerk.yml` into parent DAGs:
  ```bash
  go test ./internal/erp/ase -v -run TestDAGCompliance
  ```

### 7.2 Client Demo Checklist
* **Email Ingestion:** Forward invoice PDF to Postmark test inbox ➔ observe NATS event dispatch ➔ inspect generated Sage 100 CSV ➔ verify receipt confirmation email.
* **ICE Registry Verification:** Confirm vendor ICE lookup flags non-existent or invalid company registration numbers.
