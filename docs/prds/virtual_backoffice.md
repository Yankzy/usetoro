# **TECHNICAL PRD: Toro OS "$1,000 Virtual Back-Office"**

## **1. System Architecture & Setup (The Localhost Build)**
* Because this must be live by Monday, we are running a lightweight edge architecture.
* **Ingress/Egress:** A Twilio Phone Number with the SMS Webhook pointing to your local machine via `ngrok` (e.g., `https://1234-abcd.ngrok-free.app/webhook/twilio`).
* **Control Plane:** Toro OS Go/NATS Orchestrator.
* **Compute Plane:** Universal AI Agents (`Agent_Intent_Extractor`, `Agent_Universal_OCR`, `Agent_Schema_Enforcer`) connected to the Gemini API.
* **State Management:** In-memory map or a lightweight SQLite database (to track session states between SMS replies).

---

## **2. Core NATS Subject Topology**
**Elon Musk:** These are the exact event pipes your Go Orchestrator will listen to.
* `ingress.twilio.sms`: Fires when `ngrok` receives the Twilio HTTP POST.
* `task.intent.classify`: Requests the Intent Agent to route the text.
* `task.vision.analyze`: Requests the OCR Agent to read an MMS image.
* `task.schema.calculate`: Requests the Schema Agent to calculate a quote range.
* `egress.twilio.send`: Triggers the API call back to Twilio to text the user.

---

## **3. The 5 Composable Workflows (DAG Definitions)**

### **Workflow 1: The Emergency Missed Call Catcher**
* This is triggered when a homeowner calls and leaves no voicemail.
* **Trigger:** Go detects a missed call via Twilio Voice webhook, OR user sends a first-time SMS.
* **Initial Action:** Go drops `egress.twilio.send`: *"Hi, this is Mike's Roofing AI. Mike is on a ladder right now. Is this an emergency leak or do you need a quote?"*
* **State Transition:** Set user's session to `STATE_AWAITING_INTENT`.
* **User Replies:** *"My ceiling is leaking."*
* **Intent Extraction:** Go drops payload onto `task.intent.classify`. Agent returns `INTENT: EMERGENCY`.
* **Action:** Go drops `egress.twilio.send`: *"Flagged for priority. Please text a wide photo of the damage so Mike can prep the truck."*
* **State Transition:** Set user's session to `STATE_AWAITING_PHOTO`.
* **User Sends MMS:** Go downloads image URL. Drops onto `task.vision.analyze`. Agent returns `{"damage": "Water damage on drywall."}`
* **The Handoff:** Go drops `egress.twilio.send` directly to the **Contractor's Cell Phone**: 
  `🚨 URGENT: Leak at [Number]. Damage: Water damage on drywall. [Image Link]`

### **Workflow 2: The "Tire-Kicker" Qualifier**
**Ken Griffin:** This shares the exact same entry point as Workflow 1, but branches based on the Intent classification.
* **User Replies (from Initial Action):** *"I need a quote for a new roof."*
* **Intent Extraction:** Agent returns `INTENT: QUOTE_REQUEST`.
* **Action:** Go drops `egress.twilio.send`: *"Happy to help. Roughly how many square feet is your home?"*
* **State Transition:** Set user's session to `STATE_AWAITING_SQFT`.
* **User Replies:** *"Around 2,000."*
* **Schema Agent Calculation:** Go drops text to `task.schema.calculate` with a prompt mapping sq ft to a local baseline formula (e.g., $4.00 per sq ft).
* **Action:** Go drops `egress.twilio.send`: *"Thanks! A standard replacement for that size usually ranges from $7,500 to $9,000. Does that align with your budget to schedule a free inspection?"*
* **The Handoff:** If User replies *"Yes"*, text the Contractor: `🟢 NEW QUALIFIED LEAD: [Number] - 2000 sqft - Budget Approved.`

### **Workflow 3: The "On-My-Way" Gatekeeper**
* This is triggered by the *Contractor's* phone number, acting as an Admin command.
* **Trigger:** Contractor texts the Twilio number: *"OMW to 555-1234"* or *"OMW to John"*.
* **Intent Extraction:** Agent recognizes `INTENT: ADMIN_COMMAND_OMW` and extracts the target identifier (`555-1234`).
* **Action:** Go Orchestrator resolves the target's phone number from the session database.
* **Execution:** Go drops `egress.twilio.send` to the Homeowner: *"Hi! Mike's Roofing is heading your way now. Please ensure the driveway is clear and pets are inside."*

### **Workflow 4: The 5-Star Review Trap**
* This requires a time-delay mechanism (a sleep or a cron-job in Go).
* **Trigger:** Contractor texts Twilio: *"Done with 555-1234"*.
* **Intent Extraction:** Agent recognizes `INTENT: ADMIN_COMMAND_DONE`.
* **Action:** Go Orchestrator sets a scheduled task for `time.Now().Add(24 * time.Hour)`.
* **24 Hours Later:** Go drops `egress.twilio.send` to Homeowner: *"Hi! Mike's Roofing here. Were you 100% satisfied with our work?"*
* **User Replies:** *"Yes, looks great."*
* **Intent Extraction:** Agent returns `INTENT: POSITIVE_FEEDBACK`.
* **Execution:** Go drops `egress.twilio.send`: *"Amazing! We are a family business. Dropping a 5-star review here helps us feed our kids: [Google Maps Link]."*

### **Workflow 5: The Invoice Chaser**
* Simple text parsing to trigger a financial reminder.
* **Trigger:** Contractor texts Twilio: *"Invoice 555-1234 $600"*.
* **Intent Extraction:** Agent recognizes `INTENT: ADMIN_COMMAND_INVOICE`. Extracts `amount: 600` and `target: 555-1234`.
* **Action 1 (Immediate):** Go drops `egress.twilio.send` to Homeowner: *"Hi, here is the secure link to clear your $600 balance: [Stripe Payment Link]"*
* **Action 2 (Scheduled):** Go sets a task for `time.Now().Add(48 * time.Hour)`. 
* **48 Hours Later (If status is unpaid):** Go drops `egress.twilio.send` to Homeowner: *"Notice: Your invoice for $600 is currently past due. Please clear the balance today to close out your ticket."*

---

## **4. Data Contracts (JSON Schemas for Go)**

**Session State Tracking (SQLite / In-Memory Struct)**
```go
type Session struct {
    PhoneNumber string    `json:"phone_number"`
    State       string    `json:"state"` // e.g., "STATE_AWAITING_INTENT", "STATE_AWAITING_PHOTO"
    LastUpdated time.Time `json:"last_updated"`
    Role        string    `json:"role"`  // "CUSTOMER" or "ADMIN"
}
```

**Standard Intent Payload (Python/Go to Orchestrator)**
```json
{
  "phone_number": "+15550198372",
  "classified_intent": "QUOTE_REQUEST",
  "extracted_entities": {
    "sqft": null,
    "amount": null,
    "target_user": null
  },
  "confidence_score": 0.98
}
```

---

## **5. The Weekend Execution Sprint**

**Saturday (The Ingress & The Core Loop):**
1. Buy a Twilio number ($1.15).
2. Start `ngrok` and point the Twilio SMS webhook to your local Go port.
3. Write the HTTP handler in Go to accept the Twilio JSON payload and drop it onto NATS.
4. Build the `Agent_Intent_Extractor` prompt in Gemini. 
5. Test Workflow 1 & Workflow 2 end-to-end on your phone.

**Sunday (The Admin Commands & Edge Cases):**
1. Implement the `Role` check so the Go Orchestrator knows when *you* (the contractor) are texting it.
2. Build Workflows 3, 4, and 5 (OMW, Reviews, Invoices). 
3. Hardcode the Stripe link and Google Review link for the demo.
4. QA test every flow. Try to break it by sending weird texts. Ensure the Go Orchestrator gracefully handles unexpected inputs by repeating the prompt.

**Monday (The Revenue Event):**
1. Look up 5 local roofers or plumbers. 
2. Call them, pitch the bundle using their actual economic numbers ($7,500 replacements, $500 repairs).
3. Text their phone from your Twilio number to show them the real-time AI receptionist.
4. Collect the $1,000 setup fee via Stripe.

This is a rock-solid, highly pragmatic PRD. You are building a decoupled, event-driven architecture that is perfect for a fast weekend sprint but scalable enough to handle real volume later. 

---

# Technical stack and component breakdown required to make this function.

## **1. The Gateway (Ingress & Egress)**
* **Twilio API:** The absolute front door of the system. It provisions the phone numbers, catches incoming SMS/MMS, detects missed calls, and executes the final outgoing text payloads.
* **Ngrok Tunnel:** The edge-to-local bridge. It exposes your localhost securely so Twilio's webhooks have a live URL to POST payloads to during your weekend build.

## **2. The Control Plane (Orchestration & Routing)**
* **Go Orchestrator:** The brain of the operation. It acts as the traffic controller, subscribing to NATS topics, checking database states, formatting AI prompts, and executing hardcoded business logic.
* **NATS Message Broker:** The event-driven nervous system. It handles the pub/sub subject topology (e.g., `task.intent.classify`, `egress.twilio.send`), keeping your Go services and AI agents completely decoupled and lightning-fast.
* **Time-Delay Scheduler:** A vital sub-component running within Go (via cron jobs or goroutine sleeps) to manage async delayed triggers like the 24-hour review trap and the 48-hour invoice chaser.

## **3. The Compute Plane (AI Agent Microservices)**
* **Gemini API:** The core LLM engine doing the heavy lifting for reasoning and extraction.
* **Agent: Intent Extractor:** The decision engine. It ingests raw text and outputs strict, machine-readable JSON classifications (e.g., `INTENT: EMERGENCY`, `INTENT: ADMIN_COMMAND_DONE`).
* **Agent: Universal OCR:** The vision processor. It takes downloaded Twilio MMS image URLs and returns structured text describing the physical damage to hand off to the contractor.
* **Agent: Schema Enforcer:** The calculator. It maps user input (like square footage) against your predefined local pricing formulas to output safe, bounded quote ranges.

## **4. State & Data Management**
* **Session Database (SQLite):** The ground-truth storage for conversational context. It must track the user's phone number, their current conversational state (e.g., `STATE_AWAITING_SQFT`), a timestamp to handle timeouts, and an access control role (`CUSTOMER` vs `ADMIN`).

## **5. External Business Integrations**
* **Stripe:** Used to generate and host the secure payment links handed out by the Invoice Chaser component.
* **Google Maps / Business Profile:** The destination URL required for the review trap workflow.
* **The Admin Terminal:** The contractor's actual cell phone, recognized by the Session DB via its phone number, which acts as the command-line interface to trigger internal workflows.

---

### The critical "last mile" of the telecom architecture
The critical "last mile" of the telecom architecture. You have a Twilio number sitting on your server, and Mike has a physical iPhone in his pocket. How do they talk to each other when Mike misses a call? 

There are exactly two ways to wire this up, depending on how Mike runs his business. 

### **Method 1: Conditional Call Forwarding (The "Golden Path")**
This is what 90% of contractors want. Mike does *not* want to change his business phone number. He wants people to keep calling his normal AT&T or Verizon cell phone. 

**The Execution:**
You use a native telecom feature called **Conditional Call Forwarding (CCF)**. CCF tells the cell phone carrier: *"If Mike does not answer, is on another call, or has his phone turned off, do NOT send them to Mike's standard voicemail. Forward the call to this Twilio Number instead."*

1. **The Setup:** You tell Mike to open his phone dialer and type in a specific code based on his carrier (e.g., for Verizon, he dials `*71` followed by your Twilio Number: `*71-555-019-8372` and hits Call).
2. **The Miss:** A homeowner calls Mike's real number. Mike is on a roof. It rings 4 times, Mike doesn't answer. 
3. **The Intercept:** Verizon automatically forwards the live call to your Twilio number. 
4. **The Twilio Webhook (TwiML):** Your Twilio number is configured with a Voice Webhook that points to your Go server. When Twilio receives the forwarded call, your Go server immediately returns a tiny piece of XML (TwiML):
   ```xml
   <Response>
      <Say voice="Polly.Matthew">Hi, you've reached Mike's Roofing. We are on a job right now, but my digital assistant is going to text you immediately so we can get you taken care of.</Say>
      <Hangup />
   </Response>
   ```
5. **The Trigger:** As soon as Go sends that TwiML to hang up the voice call, it transitions the state machine to `STATE_TRIAGE` and fires the very first SMS to the Caller ID number: *"Hi, this is Mike's AI assistant..."*

### **Method 2: Twilio as the Primary Number (The "Tracking Number" Path)**
This is for contractors who run Facebook Ads or Google Local Service Ads and want a dedicated number for marketing. They put the Twilio number directly on their website. 

**The Execution:**
1. **The Setup:** Homeowner finds Mike on Google and calls the Twilio number directly.
2. **The Bridge:** Twilio receives the call. Your Go server tells Twilio to use the `<Dial>` verb to ring Mike's actual cell phone. 
3. **The Miss:** Your Go server monitors the `DialCallStatus`. If the status returns as `no-answer` or `busy`, Go knows Mike missed it.
4. **The Trigger:** Go immediately drops the call to voicemail, plays the greeting, and fires off the initial SMS intercept. 

---
### For Monday's demo, you are going to use **Method 2** to prove the concept. You will call your own Twilio number from your buddy's phone, let it ring out, and watch it instantly fire the text message back. 

But when you actually sell this to Mike for $1,000, you will use **Method 1**. You sit in his truck, take his phone, dial the `*71` (or AT&T's `*004*`) code, and wire his existing business number directly into your Go server's brain. 

### From a Go perspective, this means your webhook router just needs *two* endpoints exposed via `ngrok`:
1. `POST /api/twilio/voice` (Returns the TwiML greeting XML and triggers the first SMS).
2. `POST /api/twilio/sms` (Receives the homeowner's text replies and feeds them to the Intent Agent).

That is how you bridge the physical telecom grid with your NATS event bus.

---

## Breakdown of the Agents, Workers, and Tools required.

### **1. Agents (The Cognitive Layer)**
*These subscribe to JetStream subjects, ingest ambiguous inputs via the Gemini API, and return structured JSON payloads to trigger state mutations.*

* **`Agent_Intent_Extractor`:** * **Purpose:** The primary routing brain. Analyzes raw SMS text to determine what the user or admin is trying to do.
    * **Input Subject:** `task.intent.classify`
    * **Outputs:** Structured intents like `QUOTE_REQUEST`, `EMERGENCY_LEAK`, or `ADMIN_COMMAND_OMW`.
* **`OCR_Agent`:**
    * **Purpose:** Evaluates incoming MMS images. It analyzes structural damage to determine severity and context for the contractor.
    * **Input Subject:** `task.vision.analyze`
    * **Outputs:** Structured context (e.g., `{"damage_type": "missing_shingles", "severity": "high"}`).
* **`Agent_Schema_Enforcer` (The Estimator):**
    * **Purpose:** Parses conversational measurements ("around a couple thousand square feet") into hard numbers to generate the quote range. 
    * **Input Subject:** `task.schema.calculate`
    * **Outputs:** Standardized numeric values to be passed to a Tool for actual calculation.

---

### **2. Workers (The Deterministic Code)**
*These are your reliable, LLM-free Go routines. They listen to JetStream, execute strict business logic, interact with external APIs, and handle the Redux state mutations.*

* **`Worker_Twilio_Ingress`:**
    * **Purpose:** The entry pipeline. It receives the HTTP POST from the webhook, normalizes the Twilio payload (stripping out unnecessary metadata), standardizes the phone number, and publishes the clean event to JetStream (`ingress.twilio.sms`).
* **`Worker_Twilio_Egress`:**
    * **Purpose:** The exit pipeline. Listens to `egress.twilio.send`, formats the final outgoing text string, POSTs to the Twilio API, and handles delivery failures/retries.
* **`Worker_State_Mutator` (The Redux Store Manager):**
    * **Purpose:** The single source of truth for your SQLite session database. It listens for intent classification events and synchronously updates the user's `State` (e.g., transitioning from `STATE_AWAITING_INTENT` to `STATE_AWAITING_SQFT`).
* **`Worker_Delay_Scheduler`:**
    * **Purpose:** Manages time-based rules. It listens for events like `ADMIN_COMMAND_DONE`, stores a delayed job in a lightweight queue or memory, and fires a new event to JetStream exactly 24 or 48 hours later (for Reviews and Invoices).

* **`Tool_Calculate_Pricing(sqft int)`:** * **Purpose:** You never want an LLM doing arithmetic. `Agent_Schema_Enforcer` extracts the `sqft` integer, but calls this Go function to reliably multiply it by the local baseline (e.g., `$4.00/sqft`) to generate the strict `$7,500 - $9,000` quote array.
* **`Tool_Generate_Stripe_Link(amount int, phone string)`:**
    * **Purpose:** Called when processing the `ADMIN_COMMAND_INVOICE` intent. It generates or fetches the exact Stripe Payment Link required to clear the specific balance.
* **`Tool_Format_Phone_E164(raw_number string)`:**
    * **Purpose:** A simple regex/formatting function the Intent Agent can call to ensure numbers extracted from natural text ("OMW to 555-1234") map perfectly to the E.164 database standard ("+15550001234").
