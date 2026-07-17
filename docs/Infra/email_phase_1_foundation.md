# Phase 1: Database & NATS Foundation

## Objective
Establish the state layer required to manage proprietary outbound email sequences. This phase involves creating the new relational schemas in PostgreSQL and configuring the robust NATS JetStream setup for message routing.

## Core Requirements

1. **PostgreSQL Relational Schema (`sql/schema/014_email_engine.sql`)**
   - We need strict constraints and relations to prevent sending anomalies.
   - **`email_accounts` Table:**
     - Store the AES-encrypted OAuth tokens/App Passwords.
     - Include counters for `daily_send_count` (reset nightly).
     - Include reputation tracking (`status`: active, warming, cooling, burned).
   - **`campaigns` & `campaign_steps` Tables:**
     - Store the liquid templates for the sequence (e.g., Day 1, Day 3, Day 7).
   - **`prospects` Table:**
     - Store the lead metadata, current `step_id`, and `status` (active, paused, opted_out, bounced).
   - **`email_logs` Table:**
     - Complete audit log of every email sent, opened, clicked, or replied to, tied by `Nats-Msg-Id`.

2. **NATS Stream Setup**
   - The primary stream for orchestration is `WORKFLOWS`.
   - **Stream Configuration:**
     - **Subjects:** `ase.events.email.pipeline`, `ase.events.email.campaign.>`, `ase.telemetry.email.analytics` (handled automatically by `WORKFLOWS` stream wildcards).
     - **Durability:** File-based durability. *(Note: Durability is already wired globally in our `docker-compose` cluster, so we just need to ensure the stream definition requests file storage).*
     - **Deduplication:** Enforce exactly-once delivery semantics using a 2-minute deduplication window on `Nats-Msg-Id` (derived by hashing `client_id + prospect_id + step_id`).
     - **Acknowledgments:** `AckExplicit` to prevent premature message loss.

## Success Criteria
- The `014_email_engine.sql` migration executes successfully via goose/sqlc.
- The `WORKFLOWS` stream can be programmatically initialized.
- A test message published with the same `Nats-Msg-Id` twice within 2 minutes is automatically dropped by the NATS server.
