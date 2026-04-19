# PRD: SusannaAI "Zero-Trust" Transaction & Reputation Ecosystem

## 1. Executive Summary
**Vision:** To eliminate financial risk for blue-collar contractors by enforcing a "Funded Escrow First" rule, backed by Consumer Financing (BNPL) and a transparent, network-wide Homeowner Trust Score. 
**Mechanisms:** Toro OS will orchestrate API calls between Stripe (Escrow), Wisetack (BNPL), and the AI Agent (SMS/WhatsApp) to manage state transitions based on milestone sign-offs.

---

## 2. Core Primitives & Technical Requirements

### Primitive 1: The Escrow & Milestone Engine
**Objective:** No work begins until the job is 100% funded. Funds are released programmatically via SMS approval.
**Integration:** `Stripe Connect` (Custom Accounts) with `manual_payouts`.

* **State Machine:**
    1.  `Vault_Created`: AI generates a Stripe Payment Link for the total project cost.
    2.  `Vault_Funded`: Stripe Webhook confirms the homeowner paid. Funds sit in the SusannaAI Master Stripe Account.
    3.  `Milestone_Pending`: Contractor texts "Phase 1 done" with a photo.
    4.  `Milestone_Approved`: Homeowner replies "APPROVE" via SMS.
    5.  `Funds_Released`: Toro OS triggers a Stripe `Transfer` from the Master Account to the Contractor's Connected Account.
* **Go Architecture Data Model:**
    ```go
    type EscrowVault struct {
        VaultID        string
        ContractorUUID string
        HomeownerPhone string
        TotalAmount    int64
        Status         string // "pending_funds", "funded", "disputed", "cleared"
        Milestones     []Milestone
    }
    ```

### Primitive 2: The Buy Now, Pay Later (BNPL) Engine
**Objective:** Remove homeowner friction for high-ticket jobs ($5k - $50k) by instantly funding the Escrow Vault through a consumer lender.
**Integration:** `Wisetack API` (specifically built for home services) or `Affirm API`.

* **The Workflow:**
    1.  If the Escrow Vault is over $1,000, the AI texts the homeowner: *"To secure your start date, please fund the $10,000 vault here: [Vault Link]. Or, pay $250/month by applying for instant financing here: [Wisetack Link]."*
    2.  Homeowner clicks Wisetack, gets approved (soft credit pull).
    3.  **The Magic Routing:** Wisetack's API deposits the full $10,000 directly into the SusannaAI Stripe Escrow balance.
    4.  Toro OS updates the state to `Vault_Funded`. Contractor is notified to start work.

### Primitive 3: The Susanna Trust Network (The Bouncer)
**Objective:** A transparent, network-wide rating system tied to the homeowner's phone number that blocks bad actors from using any contractor on the Susanna platform.

* **The Trust Logic:**
    * **New Homeowner (Default State):** Score is `NULL` or `Pending`. AI explains the rules upfront.
    * **Positive Action:** Releasing escrow on time (+0.5 points).
    * **Negative Action:** Forcing arbitration, delaying approval, or verbal abuse to the AI (-2.0 points).
* **The AI Gatekeeper Node (YAML Blueprint):**
    Before the AI even looks at the contractor's calendar, it queries your Go backend for the homeowner's phone number.
    ```yaml
    nodes:
      - id: check_homeowner_trust_score
        type: logic.database_query
        action: check_trust_score
        inputs:
          phone_number: "{{incoming_sms.phone}}"
        on_score_below_3:
          transition_to: reject_homeowner
        on_score_above_3:
          transition_to: standard_booking_flow

      - id: reject_homeowner
        type: communication.sms
        inputs:
          message: "Your request is declined. Your Susanna Trust Score is currently {{score}}, which is below our 3.0 network minimum. Our contractors do not accept under-performing profiles."
    ```

### Primitive 4: Automated Dispute & Arbitration Protocol
**Objective:** Never let SusannaAI staff act as customer service. Push all disputes to an automated funnel.
**Integration:** `FairClaims API` (Digital Arbitration).

* **The Workflow:**
    1.  Homeowner texts "NO" to a milestone approval. State changes to `Vault_Disputed`.
    2.  AI enters **De-escalation Mode**: Requests 3 photos from the homeowner and demands a fix or a cash discount from the contractor.
    3.  If no resolution in 48 hours, the AI triggers the **Nuclear Option**:
    4.  Toro OS hits the `FairClaims API`, passing the Escrow Vault amount, text logs, and photos.
    5.  A 3rd-party digital arbitrator makes a binding ruling. Toro OS receives a webhook from FairClaims and automatically routes the Stripe funds to the winner. 
    6.  The Homeowner's Trust Score is instantly penalized if they lose.

---

## 3. The Implementation Phasing (How you code this)

To execute this without drowning in technical debt, you build it in three stages:

### Phase 1: The Manual Vault (Next 30 Days)
* **Build:** Stripe Payment Links and the SMS Approval mechanism. 
* **Skip:** BNPL and Arbitration APIs. 
* **Logic:** The homeowner pays via credit card/ACH. The AI holds the funds in Stripe. When the homeowner texts "APPROVE", your Go orchestrator pushes the funds to the contractor.

### Phase 2: The Trust Network (Days 30 - 60)
* **Build:** The `Homeowner_Trust_Score` database table. 
* **Logic:** Add the "Gatekeeper" node to your YAML flow. Send automated post-job rating requests to the contractor: *"Rate this customer 1-5."* Expose the score to the homeowner.

### Phase 3: The APIs of Scale (Days 60 - 90)
* **Build:** Wisetack (BNPL) and FairClaims (Arbitration) API connections.
* **Logic:** Once you have real volume, these APIs take the friction and legal liability entirely off your plate, allowing you to scale to thousands of contractors.

---

### The Resulting Moat
If you build this PRD into Toro OS, **SusannaAI becomes a monopoly.**
No other software company will be able to poach your contractors, because leaving SusannaAI would mean leaving the Escrow Vault, losing the BNPL financing, and losing the shield of the Trust Network. 

You are building the ultimate financial operating system for the physical world.