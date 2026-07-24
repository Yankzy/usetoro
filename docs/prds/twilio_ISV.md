# Product Requirements Document (PRD)

## Status
- [x] Draft
- [ ] Review
- [ ] Approved
- [ ] Executed

## 1. Product Overview & Core Objective

The platform is a multi-tenant AI-driven customer service SaaS designed for local home service businesses (e.g., plumbers, electricians). The AI voice agent intercepts inbound customer service calls forwarded from Google Maps or a dedicated business listing.

Upon successfully logging a service request or booking via voice, the platform must **automatically transmit a follow-up text message** to the customer. To do this at scale legally and prevent carrier blocking, the platform must act as an **Independent Software Vendor (ISV)**, programmatically provisioning isolated Twilio subaccounts and automating A2P 10DLC compliance workflows for every onboarding client without manual developer intervention.

---

## 2. System Architecture & High-Level Flow

The architecture isolates each tenant (client) structurally to satisfy carrier isolation rules while unifying billing and API control at the platform level.

### The Core Lifecycle:

1. **Merchant Onboarding:** The client inputs legal registration metadata inside the SaaS dashboard.
2. **Automated Provisioning:** Backend scripts automatically create an isolated Twilio subaccount and submit business entity records to Twilio TrustHub.
3. **Vetting & Webhooks:** The system tracks the registration status asynchronously via Twilio Webhooks.
4. **Activation:** Upon regulatory approval, the system provisions a localized phone number and anchors it to a pre-approved "Inbound-Service Voice Follow-up" A2P campaign.

---

## 3. Detailed Feature Requirements

### 3.1 UX/UI: Client Compliance Registration Module

The merchant onboarding wizard must feature a mandatory "Carrier Registration" interface. The system cannot assign active numbers or route traffic until this form is validated and submitted.

* **Legal Identity Input Fields:**
* Legal Business Name (Must match IRS SS-4 documentation or local registry exactly).
* Tax ID / EIN (9-digit validation for US entities).
* Business Structure Selector (LLC, S-Corp, C-Corp, Sole Proprietorship).


* **Physical Presence Fields:**
* Headquarters Street Address (Validation to block P.O. Boxes, which trigger automatic carrier rejection).
* City, State/Province, Postal Code, Country.


* **Vetting Evidence Fields:**
* Google Maps Business Profile URL or Live Corporate Website Link.
* *System Requirement:* The target link must contain a compliant data-sharing disclosure in its footer text.



### 3.2 Backend: Automated Twilio ISV Pipeline

Upon form submission, the platform backend must execute the following automated steps sequentially:

* **Step 1: Account Isolation**
* Trigger a POST call to the Twilio Accounts endpoint to generate a brand new `Subaccount_Sid`.
* Save the credentials securely mapped to the client’s Tenant ID in the database.


* **Step 2: TrustHub Profile Creation**
* Transmit the collected onboarding data to `/v2/Trusthub/CustomerProfiles` using the master Parent Account API keys.
* The payload assigns the profile directly to the newly generated client `Subaccount_Sid`.


* **Step 3: Automated Campaign Submission**
* Submit a standard, pre-approved transactional profile configuration using the Messaging Extensions API.
* **Standardized Submission Parameter Archetype:**
* *Use-Case Category:* `Low-Volume Mixed` (or `Customer Care`).
* *Consent Proof Profile:* "Implied Conversational Consent. The end-user discovers the client's public telephone number on Google Maps or their business website and explicitly calls in to request service. The automated AI voice assistant documents the call parameters and transmits a single confirmation text detailing booking times or request summaries."
* *Sample Payload Text 1:* `"Hi [Name], this is the AI assistant at [Business Name]. We've scheduled your service request for tomorrow at [Time]. Reply STOP to opt out."`





### 3.3 Event Driven Sync: Status Webhook Listener

To prevent blocking synchronous operations, the backend must listen to external lifecycle events from Twilio.

* **Webhook Destination Endpoint:** `/api/v1/webhooks/twilio-compliance`
* **Vetting Flow Logic:**
* **On `CustomerProfileStatus == APPROVED`:** Trigger the automated phone number purchasing routing for that subaccount. Programmatically associate that phone number to the approved Messaging Campaign ID.
* **On `CustomerProfileStatus == REJECTED`:** Extract the error payload reason, write it to the internal logging table, flip the client's dashboard state to "Action Required", and send an automated notification to the merchant detailing what structural input needs remediation (e.g., mismatched EIN).



---

## 4. Operational & Compliance Mandates

* **Parent Account Insulation:** The main parent Twilio account must **never** register its own A2P campaign or process outbound SMS queues directly. It must serve purely as a routing hub, token authenticator, and billing vehicle.
* **Client Privacy Policy Requirement:** The onboarding UI must notify the merchant that their website footer or landing page **must** display the explicit carrier-mandated legal text before submission:
> *"No mobile information will be shared with third parties or affiliates for marketing or promotional purposes. All the above categories exclude text messaging originator opt-in data and consent; this information will not be shared with any third parties."*


* **Mandatory Message Constraints:** Every outbound text message generated by the platform AI system must strictly include an implicit stop-phrase identifier (e.g., *"Reply STOP to unsubscribe"* or *"Reply STOP to opt out"*).

---

## 5. Non-Functional Requirements

* **Security & Isolation:** Under no circumstances may a phone number from one client subaccount process an execution thread or database call belong to another tenant subaccount. Access tokens must be verified at the subaccount scope per API call.
* **Retry Mechanics:** If a Twilio TrustHub API call encounters an infrastructure failure (HTTP 5xx), the backend must follow an exponential backoff loop strategy with a maximum ceiling of 5 execution attempts before alerting system administrators.
* **Observability Dashboard:** Provide a global admin interface for the platform team to track all open client registrations grouped by current state (`Pending Review`, `Approved`, `Rejected`) to spot wider-scale API blockages or vetting latency trends among telecom vendors.