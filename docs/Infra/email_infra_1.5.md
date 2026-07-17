# Product Requirement Document (PRD)

## Enterprise Core: Native Sequencing, Sending, Monitoring, & Analytics Engine

This PRD provides the engineering specifications to build a fully proprietary, high-throughput email execution stack using a **Go** backend and **NATS JetStream** event streams. This completely eliminates reliance on third-party sequencers like Smartlead or Instantly, putting infrastructure, data, and compliance entirely under your platform's control.

---

## 1. System Topology & NATS Stream Architecture

To manage distributed state transitions without losing messages or hitting race conditions, we define a dedicated NATS stream named `SENDING_CORE`. This stream utilizes file-backed message durability and explicit acknowledgments (`AckExplicit`).

```
[campaign.schedule.triggered] ──> [mail.render.synthesized] ──> [mail.dispatch.throttled]
                                                                        │
[analytics.metrics.logged]    <── [email.inbound.reply.parsed]     <── [mail.delivery.sent]

```

### Stream Policies

* **Deduplication Window:** 2 minutes (`Nats-Msg-Id` enforced via hashing `client_id + prospect_id + step_id`).
* **Retry Strategy:** Max 3 retries for transient SMTP/network drops, featuring exponential backoff ($30\text{s} \rightarrow 5\text{m} \rightarrow 15\text{m}$). Dead-lettered items fall back to `sending.dlq`.

---

## 2. Component Specifications

### 1. Campaign & Sequence Orchestration (The State Machine)

This module acts as the core cron-and-state controller. It determines *who* gets emailed, *when*, and *what* step they are on.

* **Distributed Delay Execution:** Rather than running heavy SQL loops searching for `"send_at < NOW"`, use NATS JetStream’s native **Delayed Messages** feature (`Nats-Deferred-Publish`). When a step finishes, publish the next step to NATS with a calculated delay header (e.g., `3 days`).
* **Conditional Branching / Stop Conditions:**
* If a prospect replies, bounces, or unsubscribes, flip their state in the central database to `paused` or `opted_out`.
* When a delayed NATS message wakes up, the worker must execute a lightweight lookup check against the database: if `state != "active"`, discard the event instantly to kill the sequence.


* **Liquid Variable Injection:** Go's fast native text parsers (`text/template`) handle data injection safely. Custom validation tokens guard against missing tags (e.g., if `{{first_name}}` is blank, default elegantly to a generic alternative without breaking the layout).

---

### 2. High-Performance Sending & Compliance Engine (The Executor)

This worker manages connection pools and authenticates directly with your wholesale Google/Microsoft accounts, formatting messages to comply with strict email deliverability rules.

```go
// Enforced RFC 8058 One-Click Unsubscribe headers for 2026 Compliance
msg.SetHeader("List-Unsubscribe", "<https://track.yourdomain.com/u?t=opaque-token>, <mailto:unsub@yourdomain.com>")
msg.SetHeader("List-Unsubscribe-Post", "List-Unsubscribe=One-Click")

```

* **Dynamic Multi-Account Mailbox Rotation:** The sending queue automatically shards traffic. If a campaign requires 1,000 emails sent today, Go routines load-balance the payloads across an array of 40 active inboxes, strictly enforcing a maximum cap of **25 outbound sends per mailbox, per day**.
* **Strict Transport Layer Security (TLS):** The custom SMTP pool forces a minimum of `STARTTLS` transport. It natively validates forward-confirmed reverse DNS (FCrDNS) and matching PTR records across all nodes.
* **Click/Open Tracking Proxy Server:** Avoid standard, easily flagged tracking domains. Build a stateless Go HTTP tracker proxy server.
* **Opens:** Injects a single-pixel plain transparent GIF.
* **Clicks:** Encrypts redirect URLs into short hashes (`/c/xyz123`). When clicked, the proxy logs the metric to NATS and issues an instant HTTP `302 Redirect` to the target page.



---

### 3. Self-Healing Deliverability & Monitoring Loop (The Guardian)

This background daemon acts as your platform's built-in deliverability engineer, keeping your sender reputation safe on autopilot.

* **Google Postmaster Sync:** A Go worker periodically polls the Google Postmaster API and parses client DMARC aggregate XML reports (`rua`).
* **Automated Inbox Eviction Engine:**
* **The Rule:** If a domain's user-reported spam complaint rate spikes above **0.1%** (early warning) or hits the **0.3%** hard cliff, the system triggers an emergency protocol.
* **The Action:** The system publishes an eviction message to NATS, pauses all active campaigns utilizing that domain, flags the mailbox as `burned`, and calls your wholesale inbox provider's API to provision and replace it with a fresh node.



---

### 4. Real-Time Analytics & Attribution Engine (The Brain)

This analytics module moves away from vanity data and focuses entirely on revenue attribution metrics.

* **IMAP Idle Streaming:** Persistent Go routines keep live TCP sockets open to your mailbox pools via IMAP IDLE. When an email lands, the server captures the raw body text and pushes it down the NATS line.
* **LLM Sentiment Classification:** An inline LLM worker flags incoming text to filter out noise from actual revenue opportunities:

| Sentiment | System Action | CRM / UI Output |
| --- | --- | --- |
| **`positive_interest`** | Halt sequence instantly. Fire priority NATS alert. | Creates a hot Lead Opportunity in client CRM. Sends a Slack alert. |
| **`out_of_office`** | Parse return date via LLM. Reschedule sequence step to `return_date + 48h`. | Logs "Out of Office - Rescheduled" on dashboard timeline. |
| **`not_interested`** | Halt sequence permanently. Push email to global MD5 suppression blocklist. | Marks prospect as "Opted Out". |

---

## 3. Core Data Schemas

### Dispatch Payload (`pipeline.mail.dispatch`)

```json
{
  "task_id": "tsk_88319203",
  "client_id": "client_enterprise_99",
  "campaign_id": "camp_b2b_saas_outbound",
  "step_id": "step_02_followup",
  "sender_identity": {
    "mailbox_id": "box_google_772",
    "email": "alex@tryyourbrand.com",
    "display_name": "Alex Miller"
  },
  "recipient_identity": {
    "prospect_id": "pros_44102",
    "email": "target_cto@enterprise.com",
    "first_name": "Marcus"
  },
  "tracking": {
    "unsubscribe_token": "unsub_token_crypto_hash_string",
    "tracking_pixel_id": "px_991823"
  },
  "retry_count": 0
}

```

### Metrics Logging Payload (`pipeline.analytics.metric`)

```json
{
  "metric_id": "met_7718293",
  "client_id": "client_enterprise_99",
  "campaign_id": "camp_b2b_saas_outbound",
  "prospect_id": "pros_44102",
  "event_type": "REPLY_RECEIVED", 
  "timestamp": "2026-07-13T17:20:00Z",
  "metadata": {
    "mailbox_id": "box_google_772",
    "raw_reply_snippet": "Let's chat next Tuesday at 2 PM EST. Send an invite.",
    "ai_sentiment_classification": "positive_interest",
    "confidence_score": 0.98
  }
}

```

---

## 4. Why This Architecture Commands $10,000/Month

By writing this stack natively, your software can confidently back an ironclad **Enterprise SLA** that third-party agency tools can't touch:

1. **True Database-Level Multitenancy:** A single rogue client cannot poison another client's deliverability because the NATS dispatch layer completely isolates routing namespaces, tracking domains, and IMAP listeners per tenant.
2. **Infinite Elastic Scaling:** Because Go compiled binaries operate with fractional memory footprints compared to Node/Python platforms, your server costs scale logarithmically, leaving you with massive **95%+ profit margins** on your $10k retainers.
3. **Guaranteed Anti-Spam Compliance:** The system protects client brands from permanent 550 rejections by automatically enforcing the 2026 Google/Microsoft delivery caps, domain alignments, and RFC headers entirely through code.

## Mailpool integration 

Once Mailpool provisions a Google Workspace or Microsoft 365 inbox, **you do not call Mailpool’s API to send the emails.**

Mailpool’s job ends the moment it passes the authenticated mailbox credentials back to your Go application. The actual sending, scheduling, and sequencing are handled entirely by **your own Go infrastructure.**

The architecture works through a highly efficient division of labor:

---

## 1. The Handoff Flow

1. Your NATS infrastructure worker calls Mailpool's API to provision a new mailbox.
2. Mailpool sets up the Google/Microsoft tenant, configures the DNS, and returns a secure **OAuth Token** or **SMTP/IMAP credential block** to your system.
3. Your Go backend saves these tokens securely in your database. Mailpool is now out of the loop for this specific email's journey.

## 2. The Direct-to-Big-Tech Sending Line

When your NATS sequencing worker triggers a send event, your Go application code establishes a direct connection to the core email service provider (ESP) using the credentials stored in your database:

* **For Google Inboxes:** Your Go code opens a native secure SMTP/TLS connection directly to **`smtp.gmail.com:587`**.
* **For Microsoft Inboxes:** Your Go code connects directly to **`smtp.office365.com:587`**.

You are using Big Tech’s official mail-routing servers to dispatch the message. This approach ensures your email inherits the multi-billion-dollar IP reputation of Google or Microsoft right out of the gate.

---

## 3. The Real-Time Tracking Proxy

Because the email is fired directly through Google or Microsoft, any tracking pixels or redirect links inside the email body must point back to **your custom Go tracking server** to log engagement data.

```
 [Your Go/NATS Engine] ──(Direct SMTP Connection)──> [Google/MS Mail Servers] ──> [Recipient Inbox]
          ▲                                                                                 │
          └───────────────────(Recipient opens email / clicks link)─────────────────────────┘
                                   (Hits your Go Tracking Proxy)

```

By bypassing external APIs during the active sending loop, you keep your system's performance exceptionally high, lower your operational expenses to zero middleware costs, and maintain total ownership over the core delivery pipeline.