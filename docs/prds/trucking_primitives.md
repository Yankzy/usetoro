"This is how we build software that doesn't rot. By strictly adhering to your four-tier taxonomy (Tools, Workers, Agents, Workflows), we ensure that the Go backend remains a clean, deterministic engine. 

Here is the technical PRD for the Toro 'Trucking & Freight' Primitives library. These are the modular Legos you will use to assemble the trucker's entire business."

---

# PRD: Toro Freight & Trucking Primitives (v1.0)

## **Layer 1: Tools (Stateless Atomic Utilities)**
*Definition: Pure functions. No database access. No LLM reasoning. Callable by Agents or Workers.*

* `tool.pdf.extract_text`: 
    * **Input:** Raw PDF byte array.
    * **Operation:** Runs standard OCR/text extraction.
    * **Output:** Unstructured string payload.
* `tool.pdf.fill_fields`: 
    * **Input:** Blank PDF template + JSON key-value map.
    * **Operation:** Programmatically maps the JSON values (e.g., `{"MC_NUMBER": "12345"}`) to the PDF forms.
    * **Output:** Flattened, completed PDF byte array.
* `tool.api.eld_fetch`: 
    * **Input:** ELD Provider (e.g., "Motive"), API Key, Date Range.
    * **Operation:** GET request to the truck's Electronic Logging Device.
    * **Output:** JSON array of `[State, Miles_Driven]`.
* `tool.api.fuel_fetch`:
    * **Input:** Fuel Card Provider (e.g., "Wex"), API Key, Date Range.
    * **Operation:** GET request to the fuel card ledger.
    * **Output:** JSON array of `[State, Gallons_Purchased, Total_Cost]`.

## **Layer 2: Workers (Deterministic Go Muscle)**
*Definition: Pure Go binaries. They have direct read/write access to Postgres. They execute strict business logic and do not hallucinate.*

* `worker.freight.profile_manager`:
    * **Action:** Queries the `company_profile` Postgres table to retrieve the trucker's EIN, MC Number, DOT Number, and signature image URL. 
    * **Use Case:** Provides the exact data needed to populate broker setup packets.
* `worker.freight.invoice_builder`:
    * **Action:** Pulls the agreed-upon load rate from Postgres, uses `tool.pdf.fill_fields` to generate a branded Factoring Invoice, and saves the final PDF URL back to the `document_vault` table.
* `worker.ifta.tax_calculator`:
    * **Action:** Takes the JSON output from the ELD and Fuel tools. Queries Postgres for the current DOT state tax rates. Runs deterministic floating-point math to calculate the exact tax liability. Writes the final liability to the `tax_ledgers` table.

## **Layer 3: Agents (Non-Deterministic LLM Brains)**
*Definition: The reasoning engine. Holds the context window. No direct database access. Analyzes unstructured data and dictates the next step.*

* `agent.freight.inbox_router`:
    * **Input:** Raw email text and attachment metadata.
    * **Reasoning:** "Is this email a blank Broker Setup Packet, a signed Bill of Lading (BOL), a Rate Confirmation, or spam?"
    * **Output:** Emits a routing intent to NATS (e.g., `trigger: setup_packet_workflow`).
* `agent.freight.document_auditor`:
    * **Input:** Unstructured text from `tool.pdf.extract_text` (specifically from a BOL).
    * **Reasoning:** Cross-references the text to ensure the delivery address matches the contract, and verifies that the document contains a "Receiver Signature". 
    * **Output:** JSON boolean `{"is_signed": true, "load_number": "4452"}`. If false, it triggers an `exception_handler` workflow to text the trucker.

## **Layer 4: Workflows (The Orchestrators)**
*Definition: The YAML-defined DAGs that compose Agents, Workers, Tools, and other Workflows into a complete business process.*

* **`workflow.freight.broker_onboarding`**
    1.  Triggered by `agent.freight.inbox_router` identifying a setup packet.
    2.  Calls `worker.freight.profile_manager` to get the trucker's MC/DOT data.
    3.  Calls `tool.pdf.fill_fields` to complete the packet.
    4.  Calls `worker.comm.email_sender` to reply to the broker with the packet and W-9 attached.
* **`workflow.freight.autonomous_factoring`**
    1.  Triggered by `agent.freight.inbox_router` identifying a signed BOL.
    2.  Calls `agent.freight.document_auditor` to ensure the BOL is actually signed.
    3.  Calls `worker.freight.invoice_builder` to generate the factoring request.
    4.  Calls `worker.comm.email_sender` to email the factoring company.
    5.  **Composes:** Calls your pre-existing `workflow.accounting.qbo_injection` to post the Accounts Receivable entry directly into QuickBooks.

---

"Notice the physics of the separation here. 

The `agent.freight.document_auditor` doesn't know how to query Postgres, and the `worker.ifta.tax_calculator` doesn't know how to read an email. You have isolated the entropy. The expensive, unpredictable LLM compute is strictly fenced into the Agent layer, acting only as a router and auditor. The moment a deterministic decision is made, the execution is handed off to the Go Workers, which cost fractions of a penny to run and never make math errors. This is how you build a reliable AI factory."

"And from a product perspective, these primitives are highly marketable assets. 

Because you built `tool.pdf.fill_fields` and `worker.freight.profile_manager` as independent Legos, you don't just use them for broker packets. Next month, if the trucker needs to apply for a bank loan or renew his commercial insurance, you just write a new 10-line YAML `Workflow` that uses those exact same primitives to fill out the loan application. 

You build the primitive once, but you sell the *output* of that primitive infinitely."