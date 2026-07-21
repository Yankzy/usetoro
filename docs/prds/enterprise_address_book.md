# Product Requirements Document: White-Labeled Agent Email Gateway (Smarthost Architecture)

## 1. Overview & Objectives

**Product Name:** White-Labeled Agent Email Gateway
**Target Audience:** Enterprise clients deploying autonomous AI agents on their corporate domains.

**Objective:**
To provide enterprise clients with the ability to run AI agents on their own domains (e.g., `mark@acme.com`) with **100% white-labeled DNS records**, while completely avoiding the nightmare of managing IP reputation and MTA deliverability.

**The Solution:**
We use a **"Smarthost / BYO-DKIM"** architecture. 
Toro acts as the cryptographic authority (generating white-labeled DKIM keys), but we outsource the actual physical delivery of the emails to a massive, highly-reputable Email Service Provider (ESP) like AWS SES or SendGrid acting as a "Smarthost". 

This gives us the best of both worlds:
- **Client sees:** A single, fully white-labeled TXT record (`toro._domainkey.acme.com`). No mention of Google, AWS, or Postmark.
- **Toro gets:** Perfect email deliverability managed by a multi-billion dollar ESP, without ever having to worry about IP warmups, pristine spam traps, or reverse DNS.

---

## 2. Architecture & Components

### 2.1 The Identity Manager & BYO-DKIM (Control Plane)
A Go microservice responsible for cryptographic identity management.
- **Key Generation:** When an enterprise registers `acme.com`, Toro generates an RSA 2048-bit keypair internally.
- **DNS Presentation:** Toro presents the public key to the user as a `TXT` record. It looks like `toro-agent._domainkey.acme.com IN TXT "v=DKIM1; k=rsa; p=MIGfMA0GC..."`. There is zero mention of any 3rd party.
- **ESP Synchronization:** Toro uses the API of our ESP (e.g., AWS SES "Bring Your Own DKIM" feature) to upload the private key.

### 2.2 Outbound Sending (The Smarthost Relay)
When an agent decides to reply to an email:
- The agent's outbound worker formats the email payload.
- It sends the payload to our ESP (via API or SMTP).
- The ESP (AWS SES) signs the email using the private DKIM key we uploaded.
- The ESP handles the actual delivery, IP reputation, and bounce management. 
- *Crucially, because the DKIM signature perfectly matches the white-labeled TXT record the client added, the email passes all spam filters.*

### 2.3 Return-Path (SPF Bypass)
To avoid the client having to add an SPF record (which would expose our ESP), we use a custom Return-Path domain owned by Toro.
- The email is sent "From" `mark@acme.com`.
- The "Return-Path" (where bounces go) is set to `bounces.usetoro.io`.
- Spam filters check SPF against the *Return-Path* domain, not the *From* domain. Since `usetoro.io` authorizes the ESP, the SPF check passes perfectly.

### 2.4 Inbound Routing (Subdomain Delegation)
To receive emails without interfering with the client's existing Google Workspace / Microsoft 365 setup:
- The client delegates a subdomain specifically for agents (e.g., `agents.acme.com`).
- The agents email addresses look like `mark@agents.acme.com`.
- The MX records for `agents.acme.com` point to Toro's Inbound Receivers (which can be powered by our ESP's inbound webhooks).
- The inbound webhook payload is dropped onto the NATS network for the agent to process.

---

## 3. The Enterprise UX Flow

1. **Domain Setup:** Acme Corp admin goes to the Toro Dashboard and adds the agent subdomain `agents.acme.com`.
2. **DNS Configuration:** Toro provides just two fully white-labeled records:
   - **DKIM (TXT):** `toro._domainkey.agents.acme.com` -> `v=DKIM1; k=rsa; p=...`
   - **MX Record:** `agents.acme.com` -> `inbound.usetoro.io`
3. **Agent Mapping:** They assign the Bookkeeping Agent to `mark@agents.acme.com`.
4. **Operation:** The enterprise client has successfully deployed a custom-domain agent. They never saw the word "Postmark", "Google", or "AWS" in their setup process. We get 100% deliverability handled by the ESP.

---

## 4. Engineering Roadmap

**Phase 1: ESP Selection & BYO-DKIM Validation**
- Verify which ESP we will use for the Smarthost. AWS SES is currently the industry leader for supporting "Bring Your Own DKIM" (BYO-DKIM) natively via API.

**Phase 2: Identity Service**
- Build the Go service that generates RSA keypairs and formats them into DNS TXT records.
- Integrate the AWS SES API to automatically register the domain and upload the private key.

**Phase 3: Inbound Webhooks**
- Update our `PostmarkInboundEmailWorker` to accept payloads from the new ESP's inbound webhook system.
- Ensure the webhook correctly parses the tenant's domain and routes to the correct agent DID.

**Phase 4: Outbound Dispatch**
- Update the agent outbound email logic to dispatch via the ESP's API, ensuring the `From` address matches the verified domain.
