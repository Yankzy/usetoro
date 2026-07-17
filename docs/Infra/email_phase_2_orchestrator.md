# Phase 2: Core Orchestrator & Dispatcher

## Objective
Build the actual logic execution engine in Go. This involves a Sequencer worker (that manages the drip timing and templating) and a Dispatch worker (that manages connection pooling and the raw SMTP send).

## Core Requirements

1. **Campaign Orchestrator (`go/internal/workers/email_sequencer_worker.go`)**
   - **Trigger:** Listens to `ase.events.email.campaign.triggered`.
   - **Validation:** Performs a strict SQL check: `SELECT status FROM prospects WHERE id = $1`. If the status is not `active` (e.g., they replied and it is now `paused`), immediately drop the event.
   - **Rendering:** Pull the campaign template and execute Go's `text/template` against the prospect's JSON metadata. Use strict fault tolerance: if `{{first_name}}` is missing, inject a fallback like "there" instead of panicking or sending a broken string.
   - **Scheduling the Send:** Publish the rendered email payload to `ase.events.email.pipeline.dispatch`.
   - **Scheduling the Follow-up:** If there is a Step 2 in the campaign, schedule the next step by inserting a row into `toro_core.scheduled_jobs` with `queue_subject` = `ase.events.email.campaign.triggered` and `fire_at` = Now + delay (e.g., 3 days). The existing `ConversationSchedulerWorker` will automatically pick this up and publish it to NATS when the time comes.

2. **High-Performance Dispatcher (`go/internal/workers/email_dispatch_worker.go`)**
   - **Trigger:** Listens to `mail.dispatch.throttled`.
   - **Inbox Selection:** Fetch a valid, active row from `email_accounts`. Decrypt the AES token. If the account's `daily_send_count` > 25, loop to the next available account.
   - **Compliance Formatting:**
     - Must enforce RFC 8058 One-Click Unsubscribe headers.
     - Inject `List-Unsubscribe: <https://proxy...>` and `List-Unsubscribe-Post: List-Unsubscribe=One-Click`.
   - **SMTP Execution:**
     - Open a direct TCP connection to `smtp.gmail.com:587` or `smtp.office365.com:587`.
     - Upgrade to `STARTTLS`.
     - Authenticate via the decrypted credentials.
     - Dispatch the MIME payload.
   - **Cleanup:** Update the database counter and publish `mail.delivery.sent`.

## Success Criteria
- The sequencer correctly templates dynamic variables.
- An email is successfully delivered to a test inbox via direct SMTP.
- The One-Click unsubscribe header is verified as functional inside Gmail/Apple Mail.
