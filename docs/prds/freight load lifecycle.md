# **PRD: Toro OS Freight "Load Lifecycle" Dispatcher**

From a systems architecture standpoint, this is a beautiful implementation of a long-running distributed state machine. The Go Orchestrator acts as the central brain holding the state over a 5-day lifecycle, delegating heavy computation to the Python microservices, and using the LLM strictly as an intent-translation layer. 

#### **Phase 1: The Lifecycle Definition (State Machine)**
* **Total Expected Duration:** 2 to 7 days per workflow (Load Inception to Proof of Delivery).
* **The Ingress (Trigger):** The Broker forwards an email from a shipper (containing load details) to a dedicated SendGrid inbound address tied to Toro OS.
* **The Egress (Outputs):** SMS via Twilio to Truckers; Webhooks/Emails back to the Broker's core system.
* **Lifecycle States (Stored in Go/Database):**
    1.  `STATE_PARSING`: Python worker is extracting email data.
    2.  `STATE_BROADCASTING`: SMS sent to trucker pool, waiting for bids.
    3.  `STATE_CLAIMED`: Trucker accepted, load locked.
    4.  `STATE_IN_TRANSIT`: Active tracking, orchestrator sleeping and waking for pings.
    5.  `STATE_DELIVERED`: Awaiting Proof of Delivery (POD) image.
    6.  `STATE_TERMINATED`: POD validated, broker notified.

#### **Phase 2: Data Schema & Core Entities**
* **Entity: `Load_Record`**
    * `load_id` (UUID)
    * `origin_zip`, `dest_zip` (String)
    * `weight_lbs` (Int)
    * `commodity_type` (String)
    * `target_payout_usd` (Float)
    * `assigned_trucker_phone` (String, nullable)
    * `current_status` (Enum mapped to Lifecycle States)

#### **Phase 3: Agentic Routing Logic (The LLM Layer)**
This is where you dictate the UX. Truckers text in shorthand. Your LLM agent has *one* job: read the messy SMS and convert it into a strict JSON intent payload for the Go Orchestrator to route. 
* **Intent 1: `ACCEPT_LOAD`** (e.g., "I'll take it", "Yes", "Send it to me")
* **Intent 2: `NEGOTIATE_RATE`** (e.g., "Can you do $2500?", "Not for that price")
* **Intent 3: `LOCATION_UPDATE`** (e.g., "Just hit Dallas", "Stuck in traffic in OH")
* **Intent 4: `DOCUMENT_UPLOAD`** (Triggered when media/MMS is attached).
* *UX Tone Constraint:* The LLM must be prompted to reply with extreme brevity. No "Hello, I am an AI." Only: *"Load confirmed. Pick up at 0800 tomorrow."*

#### **Phase 4: Python Worker Specifications (The Heavy Lifters)**
**Elon Musk:** This is your microservice layer. These Python scripts must be totally decoupled, stateless, and ruthless about timeouts. If an API is down, it needs to fail fast and let the Go Orchestrator handle the retry.

* **Worker 1: `Parse_Email_To_Load`**
    * *Input:* Raw HTML/Text from SendGrid webhook.
    * *Task:* Uses regex and lightweight NLP to extract Origin, Destination, Weight, and Commodity.
    * *Output:* JSON schema to the Orchestrator.
* **Worker 2: `Calculate_Freight_Floor`**
    * *Input:* `origin_zip`, `dest_zip`.
    * *Task:* Pings Google Maps API for exact truck-routing mileage. Pings external API for current national diesel averages. Calculates `(Miles * Average MPG cost) + Target Broker Margin`.
    * *Output:* `target_payout_usd` back to Go state.
* **Worker 3: `POD_OCR_Validator`**
    * *Input:* Base64 image of the bill of lading (MMS from Trucker).
    * *Task:* Uses Tesseract or AWS Textract to read the messy photo. Scans for the receiver's signature block and the correct destination address to prevent fraud.
    * *Output:* Boolean `is_valid_pod`, plus extracted text.

#### **Phase 5: Orchestrator Fallbacks, Replays & Edge Cases**
Long-running workflows will inevitably hit network snags or human error. Your Go Orchestrator must handle these edge cases without crashing the thread.
* **Edge Case 1: The Ghosting Trucker.** * *Logic:* Go Orchestrator enters `STATE_IN_TRANSIT`. It sets a timer. If it does not receive a `LOCATION_UPDATE` intent within 8 hours, Go wakes up and triggers Twilio: *"Location ping needed for Load 1234. Please reply with current city."* If no reply in 2 hours, flag state as `REQUIRES_BROKER_INTERVENTION`.
* **Edge Case 2: Multi-Claim Race Condition.**
    * *Logic:* SMS blasts to 5 truckers. Trucker A and Trucker B text "Yes" within 3 seconds of each other. The Go event queue deterministically locks the state on the first timestamp. Trucker A gets: *"Load locked."* Trucker B gets: *"Sorry, this load was just claimed by another driver."*
* **Edge Case 3: Failed POD OCR.**
    * *Logic:* Trucker texts a blurry photo of the signed delivery. Python `POD_OCR_Validator` returns `confidence_score < 0.85`. Go Orchestrator catches the error state, replays the request to the Trucker: *"Image is too blurry for the system. Please wipe your lens and send a clear photo of the signature."*

***

This completely abstracted away the broker's manual labor. They forward an email, your Python workers price it, your Go system dispatches it, and the LLM manages the trucker until the paperwork is signed. This architecture allows a single freight broker to 5x their daily load volume without hiring a single W2 dispatcher. That is exactly how you justify a $2,000/month Enterprise SaaS fee.