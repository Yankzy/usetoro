# Phase 4: Feedback Loops (SMTP Inbound & Deliverability)

## Objective
Establish autonomous monitors that keep the delivery system healthy and responsive. This includes capturing prospect replies via SMTP Inbound Webhooks, utilizing LLMs to deduce intent, and acting on Mailpool deliverability webhooks to protect domain reputation.

## Core Requirements

1. **SMTP Inbound Listener (`go/internal/workers/smtp_inbound_worker.go`)**
   - **Purpose:** Monitor the active inboxes for prospect replies via SMTP routing.
   - **Logic:** Maintain a raw TCP server parsing SMTP protocols to accept forwarded emails from Mailpool or domains. When a new email arrives, extract the plain text body, match the sender email to an active `prospect_id`, and publish an `email.inbound.reply.parsed` NATS event.

2. **Sentiment Analysis Engine (`go/internal/workers/email_sentiment_worker.go`)**
   - **Trigger:** Consumes `email.inbound.reply.parsed`.
   - **Logic:** Pass the raw reply snippet to an LLM prompt.
   - **Outputs:**
     - `positive_interest`: Create a hot CRM opportunity, alert via Slack.
     - `out_of_office`: Extract return date, modify `prospects.status = paused`, and schedule a resume event for `return_date + 2 days`.
     - `not_interested`: Set `prospects.status = opted_out` to permanently kill the sequence.

3. **Deliverability Guardian (`go/internal/workers/deliverability_monitor_worker.go`)**
   - **Trigger:** Listen to Mailpool deliverability webhooks (e.g., `spam-checks.updated`, `inbox-placements.updated`, `ip-blacklist-checks.created`).
   - **Logic:** 
     - Mailpool continues to run background Warmups and Deliverability Checks for us.
     - If Mailpool's webhook indicates a Spam Check failure or an IP Blacklist for one of our domains, this worker must instantly flag the corresponding `email_accounts` row as `burned`.
     - Automatically pause all active campaigns utilizing that specific inbox to protect our overall reputation.

## Success Criteria
- Sending a reply to an outbound email triggers the SMTP inbound worker.
- The LLM successfully halts a sequence when given a "Please unsubscribe me" reply.
- Simulating a Mailpool spam-check failure webhook correctly marks the domain as burned in the database.
