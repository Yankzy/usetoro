# INTERNAL STRATEGY MEMORANDUM

**TO:** Founding & Executive Team

**FROM:** Head of Product & Strategy

**DATE:** July 28, 2026

**SUBJECT:** Go-To-Market & Architecture Strategy: AI Front-Desk Utility & Agentic Marketplace for Blue-Collar Trades

---

## 1. Executive Summary

This memorandum outlines our core Go-To-Market (GTM) strategy for deploying an Agent-to-Agent (A2A) B2B platform tailored to blue-collar trade contractors.

Our strategy focuses on establishing high-density buyer acquisition by deploying a free, high-utility **Inbound AI Voice Receptionist** to solve contractors' immediate operational pain: missed customer calls. By securing ownership of the contractor's primary communication interface, we establish direct proximity to trade owners.

We then monetize this access through a gatekept A2A marketplace where B2B service providers (primarily specialized trade marketing agencies) pay micro-token fees to have their sales agents pitch contractor procurement agents directly.

---

## 2. Target Verticals & Account Profile

We are concentrating GTM efforts on high-volume residential and commercial trade operators:

* **HVAC (Heating, Ventilation, & Air Conditioning)**
* **Plumbing & Drain Services**
* **Roofing & Exterior Contracting**
* **Electrical Services**

### Strategic Rationale

1. **High Inbound Revenue Vulnerability:** Tradespeople operate on job sites, ladders, and in trucks. An unanswered phone call directly translates to thousands of dollars in lost revenue to a competitor.
2. **Severe Cold Outreach Fatigue:** Trade owners are relentlessly spammed by digital agencies, directories, and software vendors, making them eager for automated phone shielding.
3. **High Seller Acquisition Budget:** Marketing agencies specializing in home services spend massive sums on cold calling and direct mail to reach contractors, making them willing to pay for algorithmic access to vetted trade accounts.

---

## 3. Product Architecture

```
                       ┌──────────────────────────────────────────────┐
                       │           CONTRACTOR PHONE NUMBER            │
                       └──────────────────────┬───────────────────────┘
                                              │
                               ┌──────────────┴──────────────┐
                               │   INBOUND VOICE AI AGENT    │
                               └──────────────┬──────────────┘
                                              │
            ┌─────────────────────────────────┴─────────────────────────────────┐
            ▼                                                                   ▼
   [INBOUND CUSTOMER CALL]                                             [SALES / VENDOR CALL]
• Answers missed calls 24/7                                        • Intercepts cold pitches
• Schedules appointments directly on calendar                      • Diverts vendor to A2A portal link
• Captures job details & emergency dispatches                      • Seller agent pays tokens to pitch

```

### Module A: Single-Player Utility (Inbound Voice AI)

Offered free to contractors, the Inbound Voice AI integrates into their phone system and primary calendar software (e.g., ServiceTitan, Housecall Pro, Google Calendar).

* **24/7 Call Answering:** Answers incoming residential/commercial customer leads instantly when the contractor is unavailable.
* **Automated Scheduling:** Qualifies lead urgency, captures service address details, and schedules quote visits.
* **Emergency Dispatch Logging:** Identifies critical issues (e.g., burst pipes, main line backups) and alerts the owner via instant SMS notification.

### Module B: The A2A Vendor Gatekeeper

When vendors or marketing agencies dial the contractor's phone line, the Voice AI screens the call and enforces automated procurement protocol:

> *"Our company uses an AI Procurement Agent for vendor inquiries. Please visit **[contractor-name].ourplatform.com** to connect your agent with ours. If your offer meets our operational criteria, a meeting will be scheduled."*

---

## 4. Marketplace Economics & Monetization

### The Pay-Per-Pitch Token Engine

To eliminate low-quality spam and drive platform revenue, the marketplace operates on a **reverse-billing token structure**:

* **Seller-Funded Execution:** The initiating party (e.g., Marketing Agency Agent) **covers 100% of the token costs** for both its own agent and the contractor's buyer agent during the negotiation.
* **Algorithmic Barrier:** Token fees act as a micro-tax that filters out broad cold blasts, ensuring seller agents only initiate contact when match probability is high.

### Autonomous Negotiation Mechanics

1. **Parameters Set by Contractor:** The trade owner sets basic procurement filters (e.g., *"Only consider local SEO agencies that specialize in HVAC, offer guaranteed cost-per-lead under $45, and operate on month-to-month contracts"*).
2. **Evaluation:** The Agency Agent submits verified performance metrics and contract terms to the Contractor Agent.
3. **Resolution:** If the proposal satisfies all parameters, the Contractor Agent books a 15-minute human-to-human introduction call on the owner's calendar. If it fails, the interaction terminates automatically with zero human interruption.

---

## 5. Implementation Roadmap

### Phase 1: Inbound Voice Utility Deployment

* Finalize core Voice AI receptionist stack (Vapi / Retell endpoints).
* Build native calendar sync triggers for standard SMB dispatch software.
* Onboard initial cohort of 50 pilot trade contractors.

### Phase 2: A2A Gatekeeper Infrastructure

* Deploy dedicated contractor portal URLs for incoming vendor pitches.
* Implement token-metering billing infrastructure for seller accounts.
* Formulate standard A2A data exchange protocols for marketing contract evaluation.

### Phase 3: Marketplace Monetization

* Open marketplace onboarding to specialized trade marketing agencies.
* Enforce mandatory token-backed outreach across all contractor portal links.
* Scale contractor acquisition leveraging the spam-blocking value proposition.