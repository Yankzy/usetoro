# PRD: SusannaAI Trust & Safety (Legal Primitives)

**Version:** 1.0  
**Status:** Draft  
**Owner:** Project Toro OS / SusannaAI

---

## 1. Executive Summary
Small trade contractors in the US (plumbers, painters, HVAC) lose significant revenue due to "Scope Creep," lack of formal bonding, and inability to enforce payments. SusannaAI will solve this by integrating legal and compliance "primitives" directly into the AI communication flow, providing a "Legal Seal" that builds consumer trust and protects contractor assets.

## 2. Target Audience
* **Primary:** Solo-practitioners and small crews ($100k - $1M ARR) in the trades.
* **Secondary:** Residential homeowners requiring high-value ($5k+) home improvements.

## 3. Core Features (Priority Ranked)

### P1: The "Ironclad" Contract Generator
* **Description:** Automatic generation of state-specific Master Service Agreements (MSAs) and Change Orders.
* **Requirement:** When a quote is accepted via AI Chat/Voice, the system must pull tenant data (License #, Address) and job data (Price, Scope) to generate a PDF.
* **Action:** Deliver via SMS/WhatsApp for digital signature (e.g., via HelloSign or DocuSign API).

### P1: The "Live Trust" Verification Badge
* **Description:** A real-time hosted page proving the contractor's "SusannaAI Certified" status.
* **Requirement:** Integration with a background check/license verification API (e.g., Checkr or LicenseFetch).
* **UI/UX:** A "Verification Link" sent automatically by the AI whenever a customer asks about insurance, bonding, or licenses.

### P2: The "Lien-Shield" Payment Guard
* **Description:** Automation of the legal "Preliminary Notice" and "Notice of Intent to Lien."
* **Requirement:** If a milestone payment is 10 days late, the AI triggers a formal legal notice to the homeowner via certified mail or encrypted email.
* **Value:** Protects the contractor’s legal right to the house’s equity if they aren't paid.

### P3: Regulatory "Paperwork" Agent
* **Description:** AI-driven distribution of mandatory compliance docs.
* **Requirement:** If the job type is "Painting" and the house was built before 1978, the AI must automatically send the EPA "Renovate Right" lead-paint pamphlet and log the receipt for the contractor's records.

---

## 4. User Flow (The "Legal Seal" Experience)

1.  **Inquiry:** Homeowner asks, *"Are you insured?"*
2.  **AI Response:** *"Yes! We are SusannaAI Certified, which includes a $20,000 bond. I’m texting you our live verification and license details now."*
3.  **Action:** Toro OS pulls the **Live Trust Badge** and sends the link.
4.  **Booking:** Homeowner agrees to the $15k quote.
5.  **Closing:** AI sends a **Contract Generator** link. Both parties sign.
6.  **Escrow:** Funds are deposited into the **SusannaAI Escrow** (Stripe Connect).

---

## 5. Technical Constraints & APIs
* **Logic Engine:** Toro OS (Go-based Orchestrator).
* **Legal Docs:** Rocket Lawyer or LegalZoom API for template generation.
* **Signatures:** Dropbox Sign (formerly HelloSign) API.
* **Insurance/Bonding:** Next Insurance or Thimble (Affiliate/Partner API).
* **Verification:** LicenseFetch (for state-level trade license monitoring).

## 6. Success Metrics
* **Close Rate:** Increase in lead-to-job conversion for "SusannaAI Certified" contractors vs. uncertified users.
* **Dispute Reduction:** 50% decrease in reported payment disputes due to signed Change Orders.
* **Retention:** Lower churn rate for users who have their legal documents "locked" in the SusannaAI Vault.

## 7. Legal Disclaimer
* SusannaAI is a software platform, not a law firm or insurance underwriter. All legal documents must include a disclaimer that they are templates and users should consult with local counsel.

---

**Next Steps:**
1.  Map the Go primitives for the PDF Generator.
2.  Select the Tier 1 Insurance API partner for the "Bonding" component.
3.  Draft the "SusannaAI Guarantee" marketing copy for the landing page.