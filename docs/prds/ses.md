# **PRODUCT REQUIREMENTS DOCUMENT (PRD) v3.0**
**PROJECT:** Toro OS Enterprise Email Infrastructure (Omni-Channel & AWS SES Integration)

### **1. EXECUTIVE SUMMARY**
Toro OS AI Agents require programmatic, high-deliverability email capabilities to communicate with end-clients and receive unstructured human data. To maintain enterprise trust and achieve infinite scalability, we are integrating **Amazon Simple Email Service (SES)** as our core machine-native primitive. This architecture handles two distinct email flows: **Closed-Loop White-Labeling** (sending on behalf of CPAs and hijacking replies) and **Direct Agent Ingress** (providing every agent with a native, dynamic inbox). Both flows utilize a zero-polling, event-driven S3-to-NATS pipeline.

### **2. CORE OBJECTIVES**
* **Tenant Isolation (White-labeling):** CPAs must send emails from their own domains (e.g., `billing@smithcpa.com`).
* **Closed-Loop Hijacking:** Client replies must bypass the CPA’s personal inbox and route directly to the Toro OS AI without requiring access to the CPA's actual email server.
* **Direct Agent Inboxes:** Every AI Agent in the Almanac receives a native, dynamically routed email address (e.g., `invoice-99@agents.toro.network`).
* **Event-Driven AI Ingestion:** Zero IMAP polling. The system must programmatically receive emails, strip attachments to S3, and trigger Go webhooks instantaneously via AWS SNS.

---

### **3. FUNCTIONAL REQUIREMENTS (THE EXECUTION DAG)**

**Feature 3.1: Domain Onboarding & Authentication**
* **Requirement:** The Toro OS UI must include a "Custom Domain Setup" tab for CPAs.
* **Logic:** The Go backend generates unique **SPF, DKIM, and DMARC** DNS records via the AWS SDK.
* **UX:** The UI displays these records for the CPA to paste into their DNS provider. 
* **Validation:** The Go backend polls AWS to verify DNS propagation and updates the CPA's SES status to "Verified."

**Feature 3.2: Outbound Sending & SMTP Header Injection (The Hook)**
* **Requirement:** The Go backend constructs MIME-formatted emails and dispatches them via the AWS SES API.
* **From Header:** Displayed as the CPA's verified domain (e.g., `From: billing@smithcpa.com`).
* **Reply-To Header (Critical):** All outbound emails MUST include a dynamically generated, RFC 5322-compliant `Reply-To` header.
    * *Format:* `Reply-To: txn_[Unique_Hash]@[Inbound_Subdomain].toro-os.com` (e.g., `txn_8839_abc123@replies.toro-os.com`).
* **Tracking:** Delivery, Bounces, and Complaints must be tracked via AWS SNS Configuration Sets.

**Feature 3.3: Inbound Routing & Event-Driven Parsing (Closed-Loop & Direct)**
* **Requirement:** The system must handle both reply-hash emails and direct agent emails via the same serverless AWS primitive.
* **AWS Flow:** 1. AWS SES receives the inbound email on either `replies.toro-os.com` (White-label) or `agents.toro.network` (Direct Ingress).
    2. An SES Receipt Rule strips the raw email and saves it directly to a secure **Amazon S3 Bucket**.
    3. AWS SNS immediately pushes a webhook directly to the Toro OS Go backend (Zero IMAP Polling).
* **Go Backend Logic (The Identity Worker):** * The Go server catches the webhook and fetches the raw JSON/attachments from S3.
    * **If `replies.toro-os.com`:** It reads the destination address to identify the exact `[Unique_Hash]`, maps the attachment to the correct CPA ledger transaction in Postgres, and triggers the NATS event.
    * **If `agents.toro.network`:** It parses the `To:` address (e.g., `accounting-agent@...`), authenticates the sender via the `user_identities` table, and publishes the payload to the specific Agent's NATS subject.

---

### **4. DELIVERABILITY & PRIMARY INBOX OPTIMIZATION**
**OBJECTIVE:** Ensure **99%+** of automated AI Agent emails bypass the Gmail "Promotions" tab and land in the "Primary" inbox by mathematically simulating 1-to-1 human operational behavior.

**Feature 4.1: MIME & Payload Structuring (The "Ugly" HTML Rule)**
* **Requirement:** The Go backend must construct all transactional emails using the `multipart/alternative` MIME type.
* **HTML Constraints:** The `text/html` payload must be intentionally stripped of complex styling. 
    * **Prohibited:** CSS background colors, HTML tables used for layout, header graphics, styled CSS buttons, and tracking pixels (unless legally mandated).
    * **Allowed:** Basic `<p>` tags, `<br>`, `<strong>`, and raw `<a>` tags for hyperlinks. The visual output must be indistinguishable from a standard Microsoft Outlook plain-text email.

**Feature 4.2: SMTP Header Sanitization**
* **Requirement:** The AWS SES configuration must strictly strip or omit any headers associated with mass-marketing platforms.
* **Prohibited Headers:** The Go backend must never inject `Precedence: bulk`, `List-Unsubscribe`, or `X-Campaign-Id` into transactional AI payloads. 

**Feature 4.3: NLP & Subject Line Generation**
* **Requirement:** AI Agents must generate hyper-specific, operational subject lines.
* **Format Rule:** Subject lines must contain specific transactional variables (e.g., Client Name, Dollar Amount, or Tax Form ID).
* **Prohibited:** Exclamation points, title-casing every word, and marketing buzzwords (e.g., "Update", "Important", "Action Required" *unless* followed by a specific entity ID).
* **Example Output:** `Missing W-9: Apex Manufacturing LLC`

**Feature 4.4: The "Forced Reply" AI Prompt Logic**
* **Requirement:** To train Google's ML that the CPA's domain is a "trusted contact," AI Agents must optimize for inbound replies.
* **Logic:** Instead of solely relying on hyperlink portals, AI agent prompts must explicitly request the user to reply to the email with the required attachment (synergizing with the `Reply-To` hash routing architecture in Feature 3.2).

**Feature 4.5: AWS SES Configuration Sets (Traffic Segmentation)**
* **Requirement:** Strict isolation of IP pools to protect the transactional domain reputation.
* **Configuration Set A (Transactional):** Used exclusively by the Go backend for AI Agent alerts, ledger reconciliation, and document requests.
* **Configuration Set B (Promotional/Bulk):** If CPAs are granted the ability to send mass newsletters, these must be routed through a separate SES Configuration Set linked to a distinct IP pool to prevent CAN-SPAM violations from degrading the primary domain reputation.

---

### **5. SECURITY, COMPLIANCE & THERMODYNAMIC CONSTRAINTS**
* **PII Protection & Data Gravity:** Inbound emails containing sensitive tax documents must be encrypted at rest in S3 using AWS KMS. Documents are never passed as raw Base64 strings through the NATS JetStream; only secure, ephemeral S3 URIs are passed to the Agents.
* **Spam & Malware Quarantine:** Because AWS SES is a raw pipe, an AWS Lambda function running ClamAV (or AWS's native SES virus scanning) must intercept the S3 `PutObject` event. Infected files are instantly deleted before the SNS webhook ever wakes up the Toro OS Go Worker.
* **Rate Limiting:** The Go backend must implement strict token-bucket rate limiting per CPA and per Agent to prevent runaway LLM loops from generating accidental email blasts and destroying domain reputation.