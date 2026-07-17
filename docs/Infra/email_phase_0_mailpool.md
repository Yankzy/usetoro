# Phase 0: Automated Infrastructure Provisioning (Mailpool API Integration)

## Objective
Establish a fully programmatic layer to interact with the Mailpool API. This layer automates the procurement, provisioning, and configuration of the physical email delivery infrastructure (domains, DNS, and Google/Microsoft inboxes).

## Core Requirements

1. **Mailpool HTTP Client Service (`go/internal/services/mailpool_client.go`)**
   - Must implement a robust Go-based HTTP client to interact with Mailpool's API endpoints.
   - Requires API Key authentication.
   - **Supported Endpoints:**
     - `POST /mailboxes` for creating Google/Microsoft workspaces.
     - Domain endpoints to automate domain registration and alignment (SPF/DKIM/DMARC).

2. **Webhook Listener (`go/internal/workers/mailpool_webhook_worker.go`)**
   - Expose an endpoint/worker to receive live HTTP Webhooks from Mailpool.
   - **Key Webhook Events:**
     - `mailboxes.created` / `mailboxes.updated`: Catch the `PlatformCredentialsData` payload.

3. **Secure Credential Handoff**
   - When a mailbox is successfully created, extract the OAuth Tokens or App Passwords from the `PlatformCredentialsData`.
   - **Security Requirement:** Encrypt these credentials at rest using AES-GCM. The AES encryption key will be sourced directly from the `.env` file (e.g., `MAILPOOL_AES_KEY`).
   - Store the encrypted credentials in the `email_accounts` relational database table (to be built in Phase 1).

## Success Criteria
- Sending a `POST` request to the internal Toro API correctly triggers a Mailpool mailbox purchase.
- The Mailpool webhook successfully hits the Toro backend.
- Credentials are AES-encrypted and stored securely in the database without manual human intervention.
