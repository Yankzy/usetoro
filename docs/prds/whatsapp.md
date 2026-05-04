# PRD: Meta WhatsApp Cloud API Primitives (Toro OS)

## 1. Objective
To build direct Ingress and Egress integration with the Meta WhatsApp Cloud API. This eliminates third-party (Twilio) messaging markup, improves unit economics by 20%+, and unlocks native interactive UI elements (buttons, lists) for the Agentic Network.

## 2. Architecture Overview
This integration requires the creation of two new isolated NATS workers (Primitives) in the Go backend.
1. **`Meta_WA_Ingress_Node`**: An HTTP server exposing a webhook endpoint to catch inbound messages from Meta, verify the signature, and publish to the NATS queue.
2. **`Meta_WA_Egress_Node`**: A worker listening to the NATS queue that takes standard Toro AI outputs and posts them directly to the Meta Graph API.



## 3. Scope & Requirements

### A. The Ingress Primitive (`Meta_WA_Ingress_Node`)
* **Endpoint Configuration:** Must expose a public `GET` and `POST` route (e.g., `/api/v1/webhooks/whatsapp`).
* **Verification (GET):** Must handle Meta's initial `hub.verify_token` challenge to authenticate the webhook URL.
* **Payload Parsing (POST):** Meta's JSON is deeply nested (`entry[0].changes[0].value.messages[0]`). The node must extract:
    * Sender Phone Number (`from`)
    * Message Body (`text.body`) or Interactive Button Reply (`interactive.button_reply.id`)
    * Message ID (for read receipts/logging)
    * Timestamp
* **Standardization:** It must map this data into the unified `ToroMessagePayload` struct and publish it to the `toro.messaging.ingress` NATS topic so the `Intent_Router_Node` can process it without knowing it came from WhatsApp.

### B. The Egress Primitive (`Meta_WA_Egress_Node`)
* **Trigger:** Listens to the `toro.messaging.egress` NATS topic for messages flagged with `channel: whatsapp`.
* **Authentication:** Must securely store and inject the Meta Graph API System User Token in the Authorization header.
* **Message Formatting:**
    * *Text Messages:* Standard string formatting.
    * *Interactive Messages:* Must support converting AI intents (e.g., "Ask plumber to accept job") into Meta's `interactive` button payload format.
* **Error Handling:** Must capture Meta API HTTP errors (e.g., User is outside the 24-hour window) and push an alert back to the `Toro_Metering_Ledger`.

### C. State Management (The 24-Hour Rule)
* WhatsApp enforces a strict 24-hour customer service window. If a user hasn't messaged the bot in 24 hours, the bot *cannot* send free-form text.
* **Requirement:** The Egress node must query the database to check the last interaction time. If `> 24 hours`, the Egress node must fall back to sending a pre-approved **Meta Message Template** instead of a free-form AI generation.

## 4. Phased Rollout Plan
* **Phase 1 (Today):** Use a Meta Developer Test App and a Meta-provided test phone number. Build the Ingress/Egress nodes and test with your personal cell phone.
* **Phase 2:** Implement interactive "Button" messages (e.g., "Approve Estimate", "Dispatch Me").
* **Phase 3:** Complete Facebook Business Manager verification for `susanaai.com` to migrate from the test number to a production WABA (WhatsApp Business Account) number.

## 5. Security & Metering
* **Security:** Every inbound webhook POST request must be validated using the `X-Hub-Signature-256` header to ensure it actually came from Meta and not a bad actor.
* **Metering:** The Egress node must report every outbound API call to the `Toro_Metering_Ledger` primitive so you can charge your clients the base cost + 20% margin.
