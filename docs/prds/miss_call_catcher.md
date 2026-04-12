# **TECHNICAL PRD: Toro OS "Missed Call Catcher"**

## **1. Product Vision & Economic Model**
This is not a consumer app; this is a B2B revenue-salvage tool. 
* **The Target:** US-based owner-operator Home Service contractors (Roofing, HVAC, Plumbing).
* **The Problem:** Contractors miss calls while working. Missed calls equal lost revenue.
* **The Solution:** A Twilio-backed AI that intercepts missed calls via SMS, triages the urgency, collects photographic evidence, and texts a highly structured lead to the contractor.
* **The Price:** $1,000 setup fee + $200/month retainer.

---

## **2. System Architecture (The Localhost Strategy)**
Because you are building and testing this over the weekend on your local machine, the ingress architecture relies on a secure tunnel. 

* **The Ingress/Egress Layer:** Twilio Phone Number. Configured to forward voice calls to voicemail, but handle SMS via Webhook.
* **The Tunnel:** `ngrok`. You will run `ngrok http 8080` to expose your local Go server to the internet so Twilio can push SMS payloads to your laptop.
* **The Control Plane (Go):** Your Toro OS Orchestrator handles the webhook, manages the conversation state, and drops payloads onto the NATS bus.
* **The Compute (Python/Go Universal Agents):** `Agent_Intent_Extractor` and `Agent_Universal_OCR` process the text and MMS images via Gemini Plus.

---

## **3. The NATS Subject Topology**
Keep the event routing as simple and decoupled as possible.

* `ingress.twilio.sms`: Fired when `ngrok` receives the Twilio webhook.
* `task.intent.triage`: Go Orchestrator asks the Intent Agent to classify the user's text.
* `task.vision.analyze`: Go Orchestrator asks the Universal OCR Agent to analyze an incoming MMS image.
* `egress.twilio.send`: Fired when the Orchestrator wants to push a text to either the Homeowner or the Contractor.

---

## **4. State Machine Lifecycle (The Triage Workflow)**
The UX must be instantaneous and highly professional. Homeowners in an emergency are panicked; the AI must project calm competence.

### **State 0: The Intercept**
1. **Trigger:** Homeowner calls the Twilio number. They hang up or leave a voicemail.
2. **Action:** Go Orchestrator detects the missed call event. It transitions the session to `STATE_TRIAGE` and fires an SMS: 
   *"Hi, this is Mike's Roofing AI assistant. Mike is currently on a job. Is this an emergency leak, or are you looking for a quote?"*

### **State 1: The Classification**
1. **Trigger:** Homeowner texts back (e.g., *"My ceiling is leaking water everywhere."*).
2. **Intent Extraction:** Orchestrator publishes `task.intent.triage`. `Agent_Intent_Extractor` returns `{"intent": "EMERGENCY"}`.
3. **Action:** Orchestrator transitions to `STATE_EVIDENCE_GATHERING` and replies:
   *"I am flagging this for priority. To help Mike prep his truck, please reply with a wide photo of the leak/damage."*

### **State 2: Visual Processing**
1. **Trigger:** Homeowner sends an MMS (Image). Twilio hits the Go webhook with the `MediaUrl`.
2. **Vision Extraction:** Orchestrator downloads the image and publishes `task.vision.analyze`. 
3. **Python/Go Execution:** The Vision Agent queries Gemini Plus: *"Identify the home damage in this photo. Be concise."* It returns: `{"damage_detected": "Drywall water saturation, active ceiling drip."}`
4. **Action:** Orchestrator transitions to `STATE_LEAD_FORWARDING`. It replies to the Homeowner:
   *"Received. I have sent this directly to Mike's personal phone as an emergency. He will review it as soon as he is off the ladder."*

### **State 3: The Handoff (Monetization Moment)**
1. **Trigger:** Internal state shift.
2. **Action:** Go Orchestrator formats the final payload and sends a Twilio SMS directly to the Contractor's actual cell phone:
   `🚨 URGENT LEAD 🚨`
   `Phone: 555-019-8372`
   `Type: EMERGENCY LEAK`
   `AI Analysis: Drywall water saturation, active ceiling drip.`
   `Photo: [Twilio Media URL]`
   `Click to Call: tel:+15550198372`

---

## **5. API Contracts & Webhook Payloads**

**1. Twilio Ingress Webhook (Mapped via `ngrok` to Go)**
```json
{
  "SmsMessageSid": "SMxxxxxx",
  "From": "+15551234567",
  "To": "+15559876543",
  "Body": "My roof is leaking",
  "NumMedia": "0"
}
```

**2. Internal NATS Intent Request (`task.intent.triage`)**
```json
{
  "session_id": "sess_8832",
  "raw_text": "My roof is leaking",
  "valid_intents": ["EMERGENCY", "QUOTE_REQUEST", "SPAM"]
}
```

**3. Internal NATS Vision Request (`task.vision.analyze`)**
```json
{
  "session_id": "sess_8832",
  "media_url": "https://api.twilio.com/.../Media/ME123",
  "prompt": "Analyze the structural or water damage in this image."
}
```

---

## **6. Execution & Migration Plan**
You build this, you test it locally, and you sell it. Once the Stripe payment hits, you must migrate to protect the SLA (Service Level Agreement).

1. **Weekend (Localhost):** Run the Go backend and Python NATS client locally. Expose via `ngrok`. Do the sales pitches using your Twilio number as the demo.
2. **Monday Afternoon (The Migration):** * Provision a $10/month DigitalOcean Droplet or AWS EC2 `t3.micro`.
   * Install Docker, NATS JetStream, and Go.
   * Run your backend as a `systemd` service or Docker container.
   * Update the Twilio Webhook URL from your `ngrok` address to your new cloud server's static IP/Domain.
3. **Scale:** Once stable, you clone this exact DAG workflow for Plumbers, HVAC techs, and Electricians. You just change the "Mike's Roofing" variable in the config file.