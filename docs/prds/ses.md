# **PRODUCT REQUIREMENTS DOCUMENT (PRD) v2.0**
**PROJECT:** Toro OS Enterprise Email Infrastructure (AWS SES Integration)
**DATE:** April 8, 2026
**LEAD ENGINEER:** Office of the CEO (Casablanca HQ)

**1. EXECUTIVE SUMMARY**
Toro OS AI Agents require programmatic, high-deliverability email capabilities to communicate with the end-clients of our CPA partners. To maintain enterprise trust, we are integrating **Amazon Simple Email Service (SES)**. This integration uses a zero-polling, event-driven architecture to handle outbound transactional emails and closed-loop inbound parsing via SMTP Header Injection (`Reply-To` hash routing).

**2. CORE OBJECTIVES**
* **Tenant Isolation (White-labeling):** CPAs must send emails from their own domains (e.g., `billing@smithcpa.com`).
* **Closed-Loop Hijacking:** Client replies must bypass the CPA’s personal inbox and route directly to the Toro OS AI without requiring access to the CPA's actual email server.
* **Event-Driven AI Ingestion:** Zero IMAP polling. The system must programmatically receive replies, extract attachments, and trigger Go webhooks instantaneously.

**3. FUNCTIONAL REQUIREMENTS**

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

**Feature 3.3: Inbound Routing & Event-Driven Parsing (The Catch)**
* **Requirement:** When a client clicks "Reply," their email client will automatically address the message to the unique hash address.
* **AWS Flow:** 1. AWS SES receives the inbound email on the `replies.toro-os.com` subdomain.
    2. An SES Receipt Rule strips the raw email and saves it directly to a secure **Amazon S3 Bucket**.
    3. AWS SNS immediately pushes a webhook directly to the Toro OS Go backend (Zero IMAP Polling).
* **Go Backend Logic:** The Go server catches the webhook, fetches the raw email from S3, extracts the PDF/Image, reads the destination email address to identify the exact `[Unique_Hash]`, maps the attachment to the correct ledger transaction in the database, and triggers the AI OCR engine.

**4. SECURITY & COMPLIANCE**
* **PII Protection:** Inbound emails containing sensitive tax documents must be encrypted at rest in S3 using AWS KMS.
* **Rate Limiting:** The Go backend must implement internal outbound rate limiting per CPA.
* **Spam/Virus Scanning:** All inbound attachments must pass through AWS's native virus scanning before Go ingestion.

***

### **PRD AMENDMENT: Deliverability & Primary Inbox Optimization**
**OBJECTIVE:** Ensure 99%+ of automated AI Agent emails bypass the Gmail "Promotions" tab and land in the "Primary" inbox by mathematically simulating 1-to-1 human operational behavior.

**1. MIME & PAYLOAD STRUCTURING (THE "UGLY" HTML RULE)**
* **Requirement:** The Go backend must construct all transactional emails using the `multipart/alternative` MIME type.
* **HTML Constraints:** The `text/html` payload must be intentionally stripped of complex styling. 
    * **Prohibited:** CSS background colors, HTML tables used for layout, header graphics, styled CSS buttons, and tracking pixels (unless legally mandated).
    * **Allowed:** Basic `<p>` tags, `<br>`, `<strong>`, and raw `<a>` tags for hyperlinks. The visual output must be indistinguishable from a standard Microsoft Outlook plain-text email.

**2. SMTP HEADER SANITIZATION**
* **Requirement:** The AWS SES configuration must strictly strip or omit any headers associated with mass-marketing platforms.
* **Prohibited Headers:** The Go backend must never inject `Precedence: bulk`, `List-Unsubscribe`, or `X-Campaign-Id` into transactional AI payloads. 

**3. NLP & SUBJECT LINE GENERATION**
* **Requirement:** AI Agents must generate hyper-specific, operational subject lines.
* **Format Rule:** Subject lines must contain specific transactional variables (e.g., Client Name, Dollar Amount, or Tax Form ID).
* **Prohibited:** Exclamation points, title-casing every word, and marketing buzzwords (e.g., "Update", "Important", "Action Required" *unless* followed by a specific entity ID).
* **Example Output:** `Missing W-9: Apex Manufacturing LLC`

**4. THE "FORCED REPLY" AI PROMPT LOGIC**
* **Requirement:** To train Google's ML that the CPA's domain is a "trusted contact," AI Agents must optimize for inbound replies.
* **Logic:** Instead of solely relying on hyperlink portals, AI agent prompts must explicitly request the user to reply to the email with the required attachment (synergizing with the `Reply-To` hash routing architecture in Feature 3.3).

**5. AWS SES CONFIGURATION SETS (TRAFFIC SEGMENTATION)**
* **Requirement:** Strict isolation of IP pools to protect the transactional domain reputation.
* **Configuration Set A (Transactional):** Used exclusively by the Go backend for AI Agent alerts, ledger reconciliation, and document requests.
* **Configuration Set B (Promotional/Bulk):** If CPAs are granted the ability to send mass newsletters, these must be routed through a separate SES Configuration Set linked to a distinct IP pool to prevent CAN-SPAM violations from degrading the primary domain reputation.