# Product Requirement Document (PRD)

## Unified Revenue Engine: Unified Operational & Self-Healing Pipeline

This document outlines how to operationalize and merge the **Automated Infrastructure Pool**, the **Signal-Driven AI Synthesis Engine**, and the **Autonomous Inbox Router** into a single closed-loop system using Go and NATS JetStream.

---

## 1. System Topology & Core Loop

Rather than treating infrastructure and operations as separate silos, the system runs as a continuous feedback loop. The operational engine treats the infrastructure built in the first PRD as a dynamic, disposable utility pool.

```
 [Signal Caught] ──> [Enrich & Verify] ──> [LLM Persona Craft] ──> [Dispatch via Active Pool]
                                                                           │
 [CRM / Slack]   <── [Positive Interest] <── [IMAP Sentiment Parser] <─────┘
       │                                           │
       └─<─── [Self-Healing Eviction] <─── [Health Monitor Flags Drop]

```

### NATS JetStream Configuration

* **Stream Name:** `REVENUE_PIPELINE`
* **Storage:** File-backed for durability.
* **Max Deliver / Retries:** 5 attempts with exponential backoff before sending to the Dead Letter Queue (`pipeline.dlq`).

---

## 2. Functional Specification by Component

### Component 1: Dynamic Infrastructure Consumption & Self-Healing

The system maintains a database pool of available, warmed mailboxes (`state: active, warming, or burned`). The campaign dispatcher dynamically draws from this pool.

#### Go Worker Mechanics (`infra.health.monitor`)

* **Trigger:** Every 4 hours via internal cron ticker.
* **Logic:** The worker queries Google Postmaster API, Microsoft SNDS, and checks internal bounce metrics for each active domain.
* **The Self-Healing Loop:**
* If an inbox's bounce rate exceeds **2%** or its reputation drop triggers an error, the worker instantly changes its database status to `burned`.
* It immediately publishes an event to `infra.domain.evict`.
* A downstream subscriber catches this event, pauses any active campaigns tied to that mailbox, executes the infrastructure routine to buy a new domain via Namecheap/Cloudflare, provisions a new Google/M365 inbox, and pushes it into the `warming` queue.
* The active campaign hot-swaps the old inbox out for a fresh one from the `active` pool seamlessly.



---

### Component 2: Signal Harvesting & LLM Synthesis

Instead of manual scheduling, data flows down the NATS pipeline as an event-driven stream.

#### Phase A: The Harvester (`pipeline.signal.harvested`)

* **Logic:** Go routines scrape job listings (Looking for specific technology keywords or open positions), financial news APIs (funding rounds), or tech-stack drops.
* **Payload Output:** Passes the verified business entity, the specific intent trigger, and target decision-maker metadata.

#### Phase B: The AI Synthesis (`pipeline.copy.synthesized`)

* **Logic:** A Go pull-consumer reads the harvested payload and opens concurrent goroutines to execute the LLM Prompt Chain.
* **Prompt Chain Constraints:**
* **Input:** Target LinkedIn bio, company website pitch, and the intent signal.
* **Output Configuration:** Structured JSON string containing purely text-based subject lines and short body copy (<100 words). It strictly bans tracking pixels, heavy HTML styling, or attachments to evade 2026 spam filters.



---

### Component 3: Autonomous Inbox Parsing & CRM Routing

This module acts as the ultimate automated closer, listening directly to incoming mail streams and extracting actionable pipeline revenue.

#### The IMAP Listener (`pipeline.inbox.received`)

* **Logic:** A highly concurrent Go daemon keeps persistent TLS connections open to all active mailboxes via IMAP IDLE. When a new email arrives, it streams the raw text instantly to NATS.
* **Sentiment Classification Loop:**
* The inbound message is processed through a fast, deterministic LLM classification prompt. It bucketed into four responses:



```
IF response == "not_interested" -> Trigger global opt-out database flag.
IF response == "out_of_office"  -> Extract return date -> Schedule delayed NATS message.
IF response == "wrong_person"   -> Extract referred contact -> Re-route to Harvester.
IF response == "positive_interest" -> Trigger CRM Escalation.

```

#### CRM Escalation Engine (`pipeline.crm.escalated`)

* **Logic:** When a positive lead is identified, the Go worker bypasses the client's hands entirely. It hits the HubSpot/Salesforce API to create a live deal opportunity, attaches the email thread context, and fires a rich webhook notification to the client's internal Slack channel with a calendar booking link.

---

## 3. End-to-End Unified Data Schema

This unified JSON structure tracks a prospect's entire journey across all three components as it travels through NATS JetStream.

```json
{
  "pipeline_id": "pipe_98234723984",
  "client_id": "client_enterprise_xyz",
  "infrastructure": {
    "current_assigned_mailbox": "sales@getcompany.com",
    "tracking_domain": "track.getcompany.com",
    "infrastructure_status": "healthy"
  },
  "prospect_data": {
    "email": "target_exec@enterprise.com",
    "first_name": "Sarah",
    "company_name": "Acme Corp",
    "linkedin_url": "https://linkedin.com/in/sarah-exec"
  },
  "intent_signal": {
    "type": "TECH_STACK_REMOVAL",
    "source": "BuiltWith_API",
    "details": "Dropped competitor_software_v1 on July 10, 2026"
  },
  "ai_generation": {
    "subject": "quick question re: your tech stack",
    "body_plain_text": "Sarah, saw Acme Corp recently moved away from competitor_software_v1. If you are handling that transition internally right now, we built an automation framework that cuts migration times in half. Worth a brief look?",
    "token_cost": 0.0042
  },
  "operational_state": {
    "current_step": "DISPATCHED",
    "send_timestamp": "2026-07-13T09:00:00Z",
    "reply_detected": false,
    "sentiment_classification": "PENDING"
  }
}

```

---

## 4. Operational Guardrails (Saga Compensation Actions)

To maintain absolute reliability at a premium price point, the Go backend must natively mitigate execution failures without crashing the pipeline:

* **Saga Step 1 (Enrichment Fails):** If Apollo or NeverBounce APIs timeout or return an ambiguous status, the message is dropped from the active queue, and the transaction is logged as "Inconclusive Data." No email is ever sent to an unverified mailbox.
* **Saga Step 2 (Synthesis Failures):** If the LLM output violates formatting rules (e.g., outputs markdown instead of plain text), the validation check flags it, skips the sending step, and triggers a schema fallback loop to regenerate the text.
* **Saga Step 3 (Spam Filter Traps):** If a specific mailbox triggers more than **2 consecutive hard bounces**, the `REVENUE_PIPELINE` engine fires a high-priority system signal to the infrastructure subsystem, instantly freezing that account and executing the hot-swap procedure. No human intervention required.