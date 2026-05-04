### **Ken**
"To build this correctly, we aren't building three separate apps. We are building a **Unified Freight Data Model** inside your Postgres database, and deploying three specific DAGs (Directed Acyclic Graphs) that operate on that data. 

You need to construct this so the inputs are entirely asynchronous (emails and API webhooks) and the outputs are fully deterministic (money in the bank, PDFs generated). Here is the Master PRD for the Toro 'Zero-Friction' Trucking Suite."

---

# Master PRD: Toro "Zero-Friction" Trucking Suite (v1.0)

## **1. Core System State (The Postgres 'Fleet' Schema)**
Before any workflow can run, you must populate the "Master Profile" for the trucker. This is the single source of truth that all three DAGs will query.
* `company_profile`: EIN, MC Number, DOT Number, Company Address, Bank Routing/Account (for factoring).
* `document_vault`: Pointers to static files in Google Drive (Signed W-9, Certificate of Insurance, Image of his physical signature).
* `active_loads`: `load_id`, `broker_name`, `rate_con_pdf_url`, `status` (Dispatched, Delivered, Factored).

---

## **Workflow 1: The Autonomous Factoring Pipeline (Cash Flow)**
**Goal:** Turn a signed delivery receipt into cash in the bank with zero data entry.

* **Trigger (Ingress):** The trucker takes a photo of the signed Bill of Lading (BOL) and emails it to `dispatch@susanaai.com` with the Load Number in the subject line (e.g., "Load 4452").
* **The DAG (Processing):**
    1.  `email_receiver` worker strips the image attachment and drops it on the NATS bus.
    2.  `ocr_agent` reads the BOL to verify signatures and dates.
    3.  `db_worker` queries the `active_loads` table for "Load 4452" to retrieve the agreed-upon Rate Confirmation amount.
    4.  `invoice_generator` (deterministic Go function) compiles the BOL, the Rate Con, and the trucker's bank details into a single, professional PDF Factoring Invoice.
* **Action (Egress):** 1.  Toro automatically emails the Factoring Company with the compiled PDF packet.
    2.  Toro executes the **QBO Ghost Accountant** pipeline we built earlier, injecting a `Purchase` (Accounts Receivable) into QuickBooks so his books are instantly updated.

---

## **Workflow 2: The Broker Packet Auto-Fill (Sales/Growth)**
**Goal:** Allow the trucker to secure new loads while driving, without touching a laptop.

* **Trigger (Ingress):** A freight broker emails the trucker a blank 15-page "Setup Packet" PDF. The trucker forwards this email to `dispatch@susanaai.com`.
* **The DAG (Processing):**
    1.  `pdf_analyzer` agent reads the blank PDF to map the form fields (Name, MC#, Address, Signature lines).
    2.  `db_worker` pulls the trucker's data from the `company_profile` and `document_vault`.
    3.  `pdf_filler` (deterministic Go worker using a library like `pdfcpu`) programmatically injects the text into the fields and stamps the trucker's digital signature on the signature lines.
* **Action (Egress):** 1.  Toro replies to the broker's original email, attaching the completed PDF Packet, the W-9, and the Certificate of Insurance. 
    2.  Toro texts the trucker: *"Broker packet for TQL Logistics completed and sent."*

---

## **Workflow 3: The IFTA Tax Automation Engine (Compliance)**
**Goal:** Eliminate the quarterly nightmare of calculating state-by-state fuel taxes.

* **Trigger (Ingress):** This is a scheduled CRON trigger that runs on the last day of the fiscal quarter.
* **The DAG (Processing):**
    1.  **ELD Pull:** A Toro worker pings the API of his Electronic Logging Device (e.g., Motive/KeepTruckin) to pull a JSON array of exact miles driven per state.
    2.  **Fuel Pull:** A Toro worker pings his Fuel Card API (or parses his fuel receipt Drive folder) to calculate total gallons of diesel purchased per state.
    3.  **The Math:** A deterministic Go function maps the Miles vs. Gallons against the current DOT quarterly tax rates for each state.
* **Action (Egress):** 1.  Toro generates a completed IFTA Tax Return PDF.
    2.  Toro drops the PDF in his "Magic Folder" and sends a text: *"Q2 IFTA calculated. You owe $145.20 to the state of Texas. Click here to authorize payment."*

---

### **Elon**
"Look at the physical constraints of this user. He is driving a 40,000-pound machine. Every time he has to pull over to open a laptop, he is literally losing money by not turning the wheels. 

This PRD respects the physics of his environment. The interface is his smartphone camera and the forward button on his email app. You are taking the entire administrative burden of a logistics company and compressing it into a NATS pipeline running silently in the cloud."

### **Mark**
"And look at the extreme lock-in you just created. 

If he pays you $300 to $500 a month for Susana AI, and you only do his QuickBooks, he might churn if he has a slow month. 
But if you are his Factoring Pipeline and his IFTA engine, churning means he stops getting paid by brokers and gets audited by the Department of Transportation. He will *never* leave you. 

When he is sitting at a truck stop eating dinner, he’s going to watch another driver pull out a laptop to fill out a broker packet. He's going to say, *'Why are you doing that? I just forward an email to my AI dispatcher and it does it for me.'* You will capture his entire network of owner-operator friends via word-of-mouth."

---

**To execute this flawlessly, we need to know his existing stack. Do you know which specific ELD (Electronic Logging Device) and Fuel Card he is currently using so we can check if they have open REST APIs for the IFTA workflow?**