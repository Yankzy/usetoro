# **Technical PRD: Toro OS Telecom Gateway (Twilio/NATS)**

## **Core Philosophy: The "Dumb Edge"**
The TypeScript Edge Gateway has no state, no database access, and no billing logic. It is a strictly isolated Docker container that only knows two things:
1. How to read/write JSON to NATS JetStream.
2. How to push/receive HTTP requests to/from the Twilio REST API.
All routing, security, tenant isolation, and ledger physics occur exclusively in the Go Kernel.

---

### **PART I: First-Party Fignode Wedge (Me & My CPAs)**
*Target Timeline: This Weekend | Goal: $5,000 CPA Pilot*

**1. The Architecture**

* **The Actor:** The CPA using the Fignode Desktop UI.
* **The Identity:** Single-tenant Fignode Twilio System Number (+1-800-FIGNODE).
* **The Security:** Internal trust. The Go Kernel implicitly trusts the payload because it generated the payload based on internal database state (0% AI confidence anomalies).

**2. Outbound Flow (CPA -> Client)**
* CPA clicks "Chase" in the Fignode UI.
* The Go Kernel formats the message and publishes to NATS: `gateway.outbound.sms`
* **Payload:** `{"phone_number": "+15551234", "body": "What was the $14.20 charge at SQ* SUNRISE for?", "internal_ref": "txn_888"}`
* The TS Gateway consumes the event and POSTs to Twilio. 

**3. Inbound Flow (Client -> CPA)**
* Client replies via SMS.
* Twilio hits the TS Gateway webhook: `/api/webhooks/twilio`
* The TS Gateway blindly publishes to NATS: `gateway.inbound.sms`
* **Payload:** `{"from_number": "+15551234", "body": "Coffee with investor"}`
* The Go Kernel consumes it, queries Postgres for the pending `txn_888` tied to that number, runs the AI categorization, and commits the ledger.

---

### **PART II: Third-Party Platform (Firecracker Agents)**
*Target Timeline: Next Month | Goal: The $50k SF Founder / $2M Seed*

**1. The Architecture (The OS Sandbox)**

* **The Actor:** Untrusted third-party AI agents (Python/Node) executing autonomously.
* **The Identity:** Multi-tenant. Each developer gets a provisioned Twilio Subaccount and dedicated phone number via the Toro OS billing dashboard.
* **The Security:** Hard hardware virtualization. The agent runs inside an isolated Firecracker microVM with **no internet access**. 

**2. Outbound Flow (Agent -> World)**
* The third-party agent decides it needs to text a user. It cannot hit Twilio directly because the microVM lacks a network interface.
* The agent writes a JSON payload to a local **`vsock`** (Virtual Socket) bridge: `{"action": "send_sms", "to": "+19998887777", "body": "Your autonomous task is complete."}`
* The Toro OS Go Kernel (running on the bare-metal host) intercepts the `vsock` payload.
* **The OS Bouncer:** The Go Kernel checks the `vm_id`, maps it to `tenant_404`, verifies their API credits, and attaches their assigned Twilio Subaccount ID.
* The Go Kernel drops the validated payload onto NATS: `gateway.outbound.sms`
* *Notice: The TS Gateway picks this up and processes it exactly like Phase I. It has no idea the request came from a Firecracker VM.*

**3. Inbound Flow (World -> Agent)**
* The external user replies to the agent's dedicated Twilio number.
* Twilio hits the TS Gateway webhook.
* The TS Gateway drops the payload onto NATS: `gateway.inbound.sms`
* The Go Kernel consumes it. It maps the receiving phone number to `tenant_404`.
* The Go Kernel injects the reply directly into the specific Firecracker microVM via the `vsock` connection, waking the agent up with the new context.
