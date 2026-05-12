# **PRODUCT REQUIREMENTS DOCUMENT (PRD) v1.0**
**PROJECT:** Toro OS Omni-Channel Gatekeeper & Session Multiplexer
**DATE:** May 4, 2026
**LEAD ENGINEER:** Office of the CEO (Casablanca HQ)

**1. EXECUTIVE OVERVIEW**
Toro OS requires a high-conversion, headless inbound routing mechanism to drive viral user acquisition. The system provides users with a unique public URL (`toro.id/[username]`). Senders use this URL to initiate a session, but all actual communication is forced through native chat channels (SMS, WhatsApp, Slack). The Go backend manages transient session state via Redis and translates these external webhooks into persistent, structured events published to the user's private NATS JetStream environment. 

**2. CORE OBJECTIVES**
* **Sender Lock-In (Viral Acquisition):** By forcing senders to communicate via their native chat applications, Toro OS permanently installs its webhook endpoints (Twilio shortcodes/WhatsApp numbers) in the sender’s daily contact list.
* **Omni-Channel Abstraction:** The receiver's Go DAG interacts solely with standardized NATS events, completely abstracted from the platform-specific API payloads of Twilio or Slack.
* **Spam Mitigation & Rate Limiting:** The public-facing web gateway acts as a strict filter, requiring a valid, timed session code before any data can enter the private NATS network.



**3. FUNCTIONAL REQUIREMENTS**

**Feature 3.1: Public URL Provisioning & Landing Page**
* **Requirement:** Users can claim a unique alphanumeric handle resulting in a public URL (e.g., `toro.id/marc`).
* **UI/UX:** A minimalist web interface displaying the receiver's custom prompt (e.g., *"I am Marc's AI. Pitch me your startup in 2 sentences."*) alongside three prominent buttons: `Pitch via SMS`, `Pitch via WhatsApp`, and `Pitch via Slack`.
* **Backend Logic:** Upon account creation, the Go backend uses the NATS API to dynamically provision an isolated JetStream subject hierarchy for the user: `toro.inbound.[username].>`.

**Feature 3.2: Session Handshake & TTL Code Generation**
* **Requirement:** Clicking a channel button on the landing page must generate a secure, temporary routing code binding the public web session to a private chat channel.
* **Logic:** * The Go backend generates a 6-character alphanumeric code prepended with the username (e.g., `MARC-789`).
    * The code is stored in the Redis cache with a strict 600-second (10-minute) Time-To-Live (TTL).
    * **Redis Key-Value Structure:** `SET code:MARC-789 '{"target_user": "marc", "channel": "whatsapp"}' EX 600`
* **UI Output:** The webpage updates to display the code and instructions: *"Text MARC-789 to [Toro Shortcode] to open the channel."*

**Feature 3.3: Webhook Ingress & Persistent Session Binding**
* **Requirement:** The Go API Gateway must intercept inbound webhooks from Twilio/Slack, validate the session code, and lock the sender's ID to the target receiver.
* **Logic:**
    * When the sender texts `MARC-789`, the Twilio webhook hits `POST /api/ingress/twilio`.
    * Go parses the `Body` and queries Redis. 
    * If a match is found, Go writes a persistent mapping to PostgreSQL: `INSERT INTO active_sessions (sender_id, target_user, channel, status) VALUES ('+1234567890', 'marc', 'whatsapp', 'active')`.
* **Automated Response Text:** Upon successful database write, the Go backend immediately triggers an outbound API call to Twilio/Slack with the exact text: *"Connection established with [Target User]'s inbox. What is your pitch?"*

**Feature 3.4: Event Translation & NATS Publish**
* **Requirement:** Subsequent messages from a bound sender must be translated into standardized JSON payloads and published to the receiver's NATS subject.
* **Logic:**
    * Sender texts their actual pitch. 
    * Go webhook handler queries the `active_sessions` Postgres table using the sender's phone number.
    * Go constructs the payload: `{"sender_uri": "whatsapp:+1234567890", "raw_text": "[Pitch Text]", "timestamp": "..."}`.
    * Go executes the NATS publish command: `nc.Publish("toro.inbound.marc.session", payload)`.
* **Viral Pivot Response Text:** Once the target's AI agent confirms receipt, the Go backend sends a final automated outbound message to the sender: *"Pitch sent to [Target User] for review. Want an AI Gatekeeper for your own inbox? Claim your URL here: toro.id/claim."*

**4. SECURITY, COMPLIANCE & EDGE CASES**

**Feature 4.1: Edge Case - Unsolicited Ingress (No Active Session)**
* **Requirement:** The system must handle senders who text the Twilio shortcode without first generating a valid Redis session code from the web interface.
* **Logic:** If the webhook `Body` does not match an active Postgres session AND does not match a valid Redis TTL code, the message is dropped from further backend processing.
* **Cost Control & UX:** To prevent runaway Twilio billing from spam bots while maintaining a clear user experience, the system will send exactly one error response per 24-hour period per unknown number: *"Invalid session code. Please visit toro.id to initiate a secure connection."* Subsequent messages from that number without a valid web handshake will be silently ignored at the Go router level.

**Feature 4.2: Rate Limiting & Abuse Prevention**
* **Requirement:** The public URL endpoint generating Redis codes must be protected against automated scraping and code-exhaustion attacks.
* **Logic:** Implement an IP-based token bucket rate limiter in the Go middleware for the `GET /api/session/generate` route, capped at 5 session generation requests per IP per hour.