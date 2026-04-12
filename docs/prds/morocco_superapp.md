# **Master RG: The Toro OS "Casablanca Concierge"**

## **1. Executive Summary & Strategy**
**Mark Zuckerberg:** The product is a single WhatsApp Business number. Consumers save it as "Casablanca Concierge." There is no native iOS/Android app. The UX is entirely conversational, multilingual (Darija, French, Arabic), and powered by Toro OS's decoupled backend. 

**The Trojan Horse Strategy:** Launch the WhatsApp number with free, high-utility services (Finding Night Pharmacies, Legalizing Documents). Use word-of-mouth in family WhatsApp groups to acquire 50,000 users for 0 MAD in marketing spend. Once the audience is captive, activate the B2B lead-generation and micro-SaaS modules (Plumbers, Tutors, Tailors) to monetize the traffic.

---

## **2. System Architecture (The Physics Engine)**
**Ken Thompson:** This entire Super App runs on your heavily decoupled Actor Model. The system is designed so that when the app scales to 100,000 concurrent WhatsApp chats, the central engine does not crash.

* **The Control Plane (Go Monolith):** Runs the Orchestrator, manages the WhatsApp Twilio/Meta API webhooks, and holds state safely in the database.
* **The Event Bus (NATS JetStream):** The central nervous system. No function calls another function directly. Everything broadcasts events (e.g., `task.intent.extract` or `lead.plumber.dispatch`).
* **The Almanac (Service Registry):** Dynamic routing. When a Python worker boots up, it registers its capabilities to the Almanac. The Go Orchestrator dynamically routes tasks based on active nodes.
* **The Data Plane (Python Microservices):** Stateless Docker containers running on cloud infrastructure. They listen for heavy compute tasks (OCR, external API pings, parametric math), execute them, return the structured JSON to NATS, and scale to zero when the queue is empty.

---

## **3. The Universal Go Agents (The Standard Library)**
**Elon Musk:** These are the foundational AI primitives hardcoded into the Go platform. They do not know what the specific workflow is; they just execute their specific physics perfectly for any plugin that calls them.

* `Agent_Universal_OCR`: Takes any image (WhatsApp photo of a pipe, a prescription, a receipt), wraps it for Gemini Plus, and broadcasts the raw extracted text.
* `Agent_Intent_Extractor`: The Grand Central Dispatch. Reads the user's incoming WhatsApp text, detects the language, and outputs a strict classification (e.g., `INTENT: PHARMACY_SEARCH`) to trigger the correct DAG workflow.
* `Agent_Schema_Enforcer`: Forces chaotic LLM text into strict, deterministic JSON objects required by the Python workers.
* `Agent_Tone_Translator`: Sits at the leaf node of every workflow. Takes cold JSON data and rewrites it in the requested persona (e.g., empathetic for a patient, highly concise for a Honda driver) before sending the final WhatsApp message.

---

## **4. The 10 Plugins & Financial Mechanics**
**Ken Griffin:** This is the monetization matrix. We are mapping the exact workflow triggers to their respective billing engines. You do not charge Moroccan consumers; you charge the B2B endpoints for the leads and the software.

### **Tier 1: The Loss Leaders (Viral User Acquisition)**
*Monetization: FREE. Goal: Get the WhatsApp number saved in every phone in Casablanca.*

| Plugin | The Workflow (DAG) | The Technical Execution |
| :--- | :--- | :--- |
| **Pharmacie de Garde** | User texts location at 3 AM. | Python worker pings local night-pharmacy APIs/registries and returns a Google Maps pin via WhatsApp. |
| **Moqata'a Prep** | User asks how to legalize a specific document. | RAG retrieval from a database of Moroccan bureaucratic requirements based on the user's CIN location. |

### **Tier 2: The Pay-Per-Lead (PPL) Engine**
*Monetization: The "Prepaid Wallet." Businesses load 500 MAD into Toro OS via CMI/Wafacash. You deduct 15–50 MAD per dispatched lead.*

| Plugin | The Workflow (DAG) | The Technical Execution |
| :--- | :--- | :--- |
| **Auto Mechanic Triage** | User sends a video of a broken engine/dent. | Universal OCR/Vision parses the issue. NATS broadcasts the lead to verified Maarif garages. First garage to accept pays the lead fee. |
| **Guerab / Honda Dispatch** | User needs a couch moved from Anfa to Ain Sebaa. | Python worker calculates distance/price. Broadcasts to verified truck drivers. Driver pays a flat finder's fee per job. |
| **Soutien Scolaire (Tutor)** | Parent requests a Baccalaureate Math tutor. | Orchestrator matches local tutors via the Almanac. Toro OS takes a 100% commission on the *first* lesson, recurring is free. |

### **Tier 3: The Micro-SaaS Engine**
*Monetization: Flat Monthly Subscription (250 MAD – 500 MAD). You sell Toro OS directly to the business to manage their operations.*

| Plugin | The Workflow (DAG) | The Technical Execution |
| :--- | :--- | :--- |
| **Syndic Concierge** | Tenant texts a broken sink or heating issue. | Automatically dispatches the building's preferred maintenance crew and sends monthly WhatsApp "cotisation" (dues) reminders. |
| **Beldi Tailor Tracker** | Customer wants to know if their Caftan is ready. | Tailor updates the internal state. Orchestrator automatically intercepts customer texts and nudges the tailor for fittings. |
| **Traiteur Quoting** | User requests a price for an Aqiqah catering. | Parametric data collection (table count, menu). Python worker runs the math and outputs an instant quote, saving the caterer hours of sales calls. |

### **Tier 4: The Marketplace Engine**
*Monetization: Escrow / Take-Rate. Toro OS handles the actual money movement and skims a percentage (1% to 15%).*

| Plugin | The Workflow (DAG) | The Technical Execution |
| :--- | :--- | :--- |
| **Hanout Restock** | Hanout owner sends a voice note for wholesale goods. | Intent Extractor aggregates 50 hanout orders. Routes bulk order to distributor. Toro OS takes 1% GMV from the distributor. |
| **Femme de Ménage** | User requests a deep clean for Thursday. | Escrow logic. User pays 250 MAD upfront. Go Orchestrator holds state. Cleaner finishes. Payout of 200 MAD triggered, platform keeps 50 MAD. |

---

## **5. Phased Execution Plan**

**Marc Andreessen:** To execute this massive RG without drowning in technical debt, you must follow this strict build sequence:

1.  **Phase 1 (The Bedrock):** Finalize the `Agent_Universal_OCR`, `Agent_Intent_Extractor`, and `Agent_Tone_Translator` inside the Go Monolith. Ensure they can be queried by the Almanac.
2.  **Phase 2 (The Hook):** Build the Python Worker for **Pharmacie de Garde**. Launch the WhatsApp number. Post it in local Facebook groups (e.g., "Moms of Casablanca"). Watch the NATS event stream light up with free users.
3.  **Phase 3 (The Revenue):** While the free user base grows, build the **Prepaid Wallet** infrastructure in Go.
4.  **Phase 4 (The Marketplace):** Onboard 10 mechanics and 10 plumbers in a specific neighborhood (e.g., Maarif). Turn on the PPL modules. Watch the system automatically deduct Dirhams from their wallets as leads flow from your massive user base.