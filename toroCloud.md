# Product Requirement Document (PRD)

**Project Name:** Toro Cloud  
**Version:** 1.0  
**Status:** Draft  
**Type:** Backend-as-a-Service (BaaS) / AI Agent Infrastructure

## 1. Executive Summary

Toro Cloud is a specialized Backend-as-a-Service (BaaS) designed to democratize the creation of financial software. It replaces the traditional "banking infrastructure" stack with a serverless, agentic cloud platform.

Unlike traditional aggregators (Plaid/MX) that provide raw data access, Toro Cloud provides **Financial Labor**. We enable frontend developers to "hire" autonomous AI Agents—powered by Anthropic’s Model Context Protocol (MCP)—to perform complex financial tasks (bookkeeping, tax estimation, lending underwriting) without writing backend code.

**The Core Promise:** Developers build the Interface; Toro provides the Intelligence and Infrastructure.

## 2. Product Principles

- **Labor, Not Just Data:** We do not just pipe data from Point A to Point B. We process, reconcile, and act on that data using agents.
- **BYOK (Bring Your Own Keys):** Customers maintain direct legal contracts with data providers (Plaid/MX/QuickBooks). Toro acts as the infrastructure layer, storing credentials securely but never holding the data hostage.
- **The "Vault" is Sacred:** All raw data is persisted to NATS JetStream (File Store) before any processing occurs. Zero data loss is the baseline.
- **Frontend-First:** All complexity must be abstracted behind the `@toro/react` SDK and the HTTP API. A developer should never need to touch the backend to launch a bank.

## 3. System Architecture

The system follows a "Factory" pattern with four distinct components:

### 3.1 Component 1: The Gate (Ingress)
- **Role:** High-performance entry point for external webhooks (Plaid, Stripe, QuickBooks).
- **Language:** Go (Golang 1.22+).
- **Key Libraries:** `github.com/jackc/pgx/v5` (DB), `github.com/dgraph-io/ristretto` (Cache).
- **Responsibilities:**
    - **Signature Verification:** Validates incoming payloads using cached BYOK secrets.
    - **Buffering:** Immediately publishes raw payloads to NATS JetStream.
    - **Latency Target:** < 5ms response time to provider.
    - **Zero Logic:** No business logic lives here.

### 3.2 Component 2: The Vault (Persistence)
- **Role:** The immutable source of truth for all financial events.
- **Technology:** NATS JetStream.
- **Configuration:**
    - **Storage:** File-backed (Hardware persistence).
    - **Replication:** 3-Node RAFT Cluster (in production).
    - **Subjects:** `raw.ingest.{provider}.{tenant_id}`.

### 3.3 Component 3: The Hive (Agent Runtime)
- **Role:** The orchestration layer where AI Agents ("Workers") execute tasks.
- **Technology:** Python 3.11+.
- **Orchestration:** Temporal.io (for long-running workflows) + LangGraph (for agent reasoning loops).
- **AI Model:** Anthropic Claude 3.5 Sonnet.
- **Standard:** Model Context Protocol (MCP). All integrations are built as MCP Servers.

### 3.4 Component 4: The Cannon (Egress)
- **Role:** Reliable delivery of results to the customer’s frontend/backend.
- **Technology:** Svix (Self-Hosted).
- **Dependencies:** Isolated Redis instance (svix-redis).
- **Capabilities:** Automatic retries, exponential backoff, endpoint management.

## 4. The "Agent Hive" Specification

Toro does not run generic code. It runs specific "Skills" packaged as MCP Servers.

### 4.1 The Skill Registry (MCP Servers)

All skills reside in isolated environments and are injected with Tenant Credentials at runtime.

| Skill Name | MCP Server | Tools Exposed |
| :--- | :--- | :--- |
| **BankReader** | `mcp-plaid` | `get_transactions`, `get_balance`, `get_liabilities` |
| **LedgerWriter** | `mcp-quickbooks` | `search_invoice`, `create_journal_entry`, `match_transaction` |
| **DocScanner** | `mcp-ocr` | `extract_receipt_data` (Vision Model), `match_receipt_to_txn` |
| **Forecaster** | `mcp-analysis` | `predict_burn_rate`, `detect_subscription_churn` |
| **Communicator** | `mcp-twilio` | `send_whatsapp_reminder`, `send_email_invoice` |

### 4.2 The "Hiring" API

Developers instantiate workers via a REST endpoint.

**Endpoint:** `POST /api/v1/workforce/hire`

**Payload:**
```json
{
  "role": "BOOKKEEPER",
  "data_sources": ["plaid_conn_1", "quickbooks_conn_5"],
  "schedule": "REALTIME"
}
```

**Behavior:** Spins up a Temporal Workflow that monitors the BankReader and triggers the LedgerWriter autonomously.

## 5. Global Use Cases (By Geography)

The platform must support specific regional logic in the "Hive" layer.

### 5.1 Region: United States (US)
**Target Market:** Gig Economy, Trucking, Creator Economy.

**Key Agents:**
- **RigRun Agent (Trucking):**
    - *Trigger:* Fuel card swipe.
    - *Action:* Categorizes as "Fuel," checks against GPS location, attaches to specific Truck ID P&L.
- **CreatorCFO Agent:**
    - *Trigger:* Deposit from "Google AdSense" or "Patreon."
    - *Action:* Calculates estimated tax (15.3% SE Tax + Income Tax), moves funds to "Tax Savings" sub-account automatically.
- **Factoring Agent:**
    - *Trigger:* Ingests an unpaid invoice from QuickBooks.
    - *Action:* Scores the risk. Offers instant advance (lending) via webhook.

### 5.2 Region: European Union (EU)
**Target Market:** Cross-border SaaS, VAT Compliance.

**Key Agents:**
- **VAT-Auto Agent:**
    - *Trigger:* Transaction flagged as "Software Service."
    - *Action:* Detects counterparty country. Applies Reverse Charge Mechanism logic automatically. Syncs to Xero.
- **OpenBanking Aggregator:**
    - *Trigger:* User connects bank via PSD2 (GoCardless/Tink).
    - *Action:* Standardizes diverse EU bank formats into the Toro Unified Schema.

### 5.3 Region: Morocco (North Africa)
**Target Market:** Informal Economy Digitization, Property Management.

**Key Agents:**
- **Syndic-AI (HOA Manager):**
    - *Trigger:* Monthly billing cycle (1st of month).
    - *Action:* Sends WhatsApp payment link to residents. Monitors bank feed for incoming transfers. Auto-matches "Virement de Mr. Alami" to Apartment 4B.
- **Hanout-OS (Grocery Credit):**
    - *Trigger:* Voice note from shopkeeper ("Ahmed, bread and milk").
    - *Action:* Transcribes to ledger. Sends weekly WhatsApp summary to Ahmed with total debt.
- **Daman-Direct (Domestic Help):**
    - *Trigger:* Monthly payroll date.
    - *Action:* Calculates CNSS (Social Security) contribution. Declares wages on CNSS portal via browser automation (Agentic Web Browsing).

## 6. Frontend SDK Specification

**Package Name:** `@toro/react`

### 6.1 The "Chameleon" Component (`<ToroConnect />`)
- **Purpose:** Abstract away Plaid Link / MX Connect / Teller Connect.
- **Logic:**
    - Fetches orchestration config from Toro Backend.
    - Dynamically injects the required provider script (e.g., Plaid.js).
    - Handles the popup flow.
    - Returns a unified `toro_connection_id` to the developer.

### 6.2 The "Worker" Hook (`useToroWorker`)
- **Purpose:** Real-time visibility into what the AI Agents are doing.
- **Usage:**
```typescript
const { status, thought_process } = useToroWorker('bookkeeper-agent-1');
// Output: "I found a receipt for $50. Matching it to the Chevron transaction..."
```

## 7. Database Schema (Core Models)

### 7.1 ToroTransaction (The Canonical Model)
- `id`: UUID (Primary Key)
- `tenant_id`: UUID (Isolation)
- `amount_micros`: BigInt (Currency agnostic)
- `currency`: String (ISO 4217)
- `merchant_normalized`: String (AI Cleaned)
- `category_id`: String (Toro Taxonomy)
- `provider_refs`: JSONB (`{ "plaid": "...", "quickbooks": "..." }`)
- `reconciliation_status`: Enum (UNMATCHED, MATCHED, MANUAL\_REVIEW)

### 7.2 AgentLog (The Audit Trail)
- `id`: UUID
- `worker_id`: UUID
- `action_taken`: String ("Created Journal Entry")
- `reasoning`: Text ("Confidence score 98% based on amount and date match.")
- `timestamp`: DateTime

## 8. Development Phases

### Phase 1: The Infrastructure (Foundation)
- Setup NATS JetStream cluster.
- Build Go Ingress (The Gate) with Ristretto caching.
- Deploy Svix + Redis.

### Phase 2: The Agent Hive (Backend)
- Implement MCP Server for Plaid (Read).
- Implement MCP Server for QuickBooks (Write).
- Build "Bookkeeper" LangGraph workflow.

### Phase 3: The SDK (Frontend)
- Build `@toro/react` wrapper.
- Build Dashboard for "Hiring" agents.

### Phase 4: Regional Skills (Expansion)
- Train/Configure specific Agents for US Trucking and Morocco Syndic use cases.



Here are **5 White Collar Jobs** you can add to the Toro Hive **right now**, along with the technical implementation for each.

---

### 1. The "Expense Auditor" (Internal Audit)

**The Job:** Every company hates "Expense Reports." A human has to look at a receipt for $150 at a steakhouse and check the Employee Handbook to see if that is allowed.
**The Agent Skill:**

* **Input:** Transaction + Receipt Image + PDF of "Company Expense Policy."
* **Logic:**
* Checks date/time (Was it a weekend?).
* Checks itemized list (Was there alcohol? Is alcohol allowed?).
* Checks amount vs. limit (Is dinner capped at $50?).


* **Action:**
* *Pass:* Auto-approves in the system.
* *Fail:* Drafts a Slack message to the employee: *"Hey, this receipt includes alcohol which isn't covered. I've flagged this for manual review."*


* **Tech Feasibility:** **100%.** LLMs are perfect at reading policies and applying them to data.

### 2. The "Collections Agent" (Accounts Receivable)

**The Job:** Chasing unpaid invoices. It is awkward and time-consuming for humans to send "You haven't paid us" emails.
**The Agent Skill:**

* **Input:** QuickBooks "Unpaid Invoices" list + Email History.
* **Logic:**
* *Day 1 Past Due:* Sends polite "Just a reminder" email.
* *Day 10 Past Due:* Sends firmer email, attaching the invoice again.
* *Day 30 Past Due:* Drafts a legal demand letter or offers a payment plan.
* **Crucial:** It reads incoming replies. If the client says "The check is in the mail," the Agent pauses the harassment sequence.


* **Tech Feasibility:** **100%.** Standard Dunning automation logic + Sentiment Analysis on replies.

### 3. The "SaaS Procurement Officer" (Spend Management)

**The Job:** Companies bleed money on "Zombie Subscriptions" (forgotten software tools). A procurement officer reviews bank statements to find waste.
**The Agent Skill:**

* **Input:** Plaid Transaction History.
* **Logic:**
* Identifies recurring patterns (Monthly/Yearly).
* Detects "Duplicate Spend" (e.g., "Why do we have a charge for Dropbox AND Box.com?").
* Detects "Price Creep" (e.g., "Slack went from $500 to $550 this month. Did we add users?").


* **Action:** Fires a webhook: `alert.spend_waste_detected` with the potential savings amount.
* **Tech Feasibility:** **100%.** Pattern matching + Enrichment (knowing that "Dropbox" and "Box" are competitors).

### 4. The "Data Entry Clerk" (Invoice Extraction)

**The Job:** An invoice arrives in PDF format via email. Someone has to type "Invoice #1024", "Vendor: Acme", and "Amount: $500" into QuickBooks.
**The Agent Skill:**

* **Input:** Gmail (Watch for attachments) or Upload.
* **Logic:**
* OCRs the PDF.
* Maps the fields to the company's "Chart of Accounts" (e.g., Knows that "Acme Corp" = "Cost of Goods Sold").


* **Action:** Creates a "Bill" entity in QuickBooks via API, ready for one-click payment.
* **Tech Feasibility:** **100%.** This is a classic "Document Intelligence" use case.

### 5. The "Compliance Officer" (KYC/AML)

**The Job:** For your fintech customers, they legally must check if a new user is a terrorist or money launderer.
**The Agent Skill:**

* **Input:** User Name + Address + Transaction patterns.
* **Logic:**
* Checks the name against the **OFAC Sanctions List** (Government database).
* Monitors transaction velocity (e.g., "Why did this user receive $9,000 and immediately wire it to Cyprus?").


* **Action:** Freezes the account via Toro API and generates a "Suspicious Activity Report" (SAR) draft.
* **Tech Feasibility:** **100%.** It's mostly database lookups and rule engines, but marketing it as an "AI Compliance Officer" is huge value.

---

### The "Hire" Menu

When a developer logs into Toro Cloud, they shouldn't just see "API Keys." They should see a **Org Chart**.

**"Who do you want to hire today?"**

* 👮 **The Auditor:** $19/month (Enforce expense policies).
* 🦈 **The Collector:** $29/month (Get paid faster).
* ✂️ **The Cutter:** $49/month (Find SaaS waste).
* 📝 **The Clerk:** $0.10 per invoice (Auto-entry).

This is how you differentiate. You aren't selling software; you are selling **payroll reduction.**




You are not building "Accounting Software" (Horizontal, like QuickBooks).
You are building **"The Operating System for X"** (Vertical).

When you go to a Trucking Company and say: *"Here is an app that manages your drivers, your loads, AND replaces your bank,"* they will buy it immediately because you solved their specific headache.

Here is the breakdown of the Trucking use case, followed by **3 other massive verticals** where this strategy prints money.

---

### Use Case 0: The Trucking OS ("RigRun")

**The Target:** Mid-sized Fleet Owners (5–50 trucks).
**The Pain:** They wait 60 days to get paid by shippers. They pay a "Factoring Company" 3% to get cash now. They spend Sundays doing IFTA fuel taxes.

**The App You Build (Frontend):**

* **For the Owner:** A dashboard to see where trucks are and how much cash is in the bank.
* **For the Driver:** A mobile app to scan fuel receipts and see their next load.

**The Toro Backend (The Magic):**

1. **Bank Connection:** You issue a **Virtual Visa Card** for every driver (via Toro/Stripe/Marqeta).
* *Benefit:* Owner sets a $500/day limit on Fuel. No more fraud.


2. **The "Factoring Agent":**
* *Job:* When a driver uploads a "Proof of Delivery" (signed paper), the Agent reads it, verifies it against the Load Board, and **instantly advances 90% of the money** to the Owner's account.
* *Lunch Eaten:* You just killed the Factoring Company. You keep the 3% fee.


3. **The "IFTA Agent":**
* *Job:* It correlates the GPS data of the truck with the Fuel Card purchases to automatically file the state fuel tax reports.



---

### Use Case 1: The Construction OS ("BuildPay")

**The Target:** General Contractors (GCs) doing home renovations or commercial fit-outs.
**The Pain:** Paying subcontractors (plumbers, electricians) is a nightmare. If a GC pays a plumber before the work is verified, the plumber might ghost. If they pay too late, the plumber quits. Also, collecting "Lien Waivers" (legal docs) is manual hell.

**The App You Build:**

* **For the GC:** Project management dashboard.
* **For the Sub:** A "Get Paid" wallet app.

**The Toro Backend:**

1. **Bank Connection:** The Homeowner pays the GC into a Toro "Escrow" wallet.
2. **The "Site Inspector Agent":**
* *Job:* The plumber uploads a photo of the installed pipes. The AI analyzes the photo: *"Does this look like completed piping?"*
* *Action:* If yes, it **auto-releases** the payment from the Escrow wallet to the Plumber.


3. **The "Compliance Agent":**
* *Job:* Before releasing the funds, it forces the plumber to digitally sign a **Lien Waiver**. No waiver, no cash.
* *Lunch Eaten:* You replaced the bank loan officer and the construction lawyer.



---

### Use Case 2: The Property OS ("LandlordAI")

**The Target:** "DIY Landlords" who own 10–50 apartment units.
**The Pain:** They currently use Excel + Zelle. They hate chasing late rent ("The awkward text message"). They lose money on maintenance because they don't track repairs vs. improvements for taxes.

**The App You Build:**

* **For the Landlord:** A "Portfolio View" of cash flow.
* **For the Tenant:** A portal to pay rent and request repairs.

**The Toro Backend:**

1. **Bank Connection:** Tenants connect their bank (Plaid). Rent is pulled automatically via ACH.
2. **The "Bad Cop Agent":**
* *Job:* If rent isn't in by the 5th, the Agent sends the legal "Notice to Quit" or late fee notice via email/SMS. The Landlord doesn't have to be the bad guy.


3. **The "Maintenance Agent":**
* *Job:* Tenant sends a photo of a leak. Agent identifies "Plumbing." It blasts the request to 3 pre-vetted local plumbers via SMS. It reads their quote replies and presents the cheapest one to the Landlord to approve.
* *Lunch Eaten:* You replaced the Property Management Company (who takes 10% of rent).



---

### Use Case 3: The Staffing OS ("GigWallet")

**The Target:** Temporary Staffing Agencies (Nursing, Event Security, Catering).
**The Pain:** Retention. Nurses and security guards live paycheck to paycheck. They hate waiting 2 weeks for payroll. They often quit for agencies that pay faster.

**The App You Build:**

* **For the Agency:** Scheduling tool.
* **For the Worker:** A "Clock-in" app + Debit Card.

**The Toro Backend:**

1. **Bank Connection:** You issue payroll cards.
2. **The "Payroll Agent":**
* *Job:* Worker clocks out at 5:00 PM. Agent verifies the GPS location matches the job site. Agent calculates taxes. Agent **instantly pushes net wages** to the worker's card at 5:01 PM.


3. **The "Shift-Fill Agent":**
* *Job:* Someone calls in sick. The Agent looks at the roster, finds who is eligible (and not in overtime), and texts them: *"Take this shift for a $50 bonus?"*
* *Lunch Eaten:* You replaced the Payroll Bureau (ADP/Paychex) and the payday lender.



---

### Summary of the Strategy

You are building **"The Business in a Box."**

* **Trucking:** FleetOS + Factoring + Fuel Cards.
* **Construction:** Project Mgmt + Escrow + Lien Waivers.
* **Real Estate:** Rent Collection + Maintenance + Eviction AI.
* **Staffing:** Scheduling + Instant Payroll.

**Pick ONE.** (I recommend **Trucking** or **Construction**—the payments are huge and the existing software is terrible). Build the frontend, wire it to Toro Cloud, and go sell it to 5 companies.