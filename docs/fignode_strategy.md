# Product Requirements Document (PRD)

Project: Fignode/Fignode Mobile App - Phase 2
Module: The CFO Command Center
Objective: Evolve the mobile app from a single-feature "gamified categorization tool" into a comprehensive "Autonomous Advisory Suite" for Senior CPAs.

Executive Summary

While the "$2 Gamified Swipe" handles historical data entry, CPAs need tools to provide forward-looking advisory services. Phase 2 introduces autonomous client communication (The Hound Agent), real-time financial monitoring (The Pulse), and a live portfolio view (CFO Dashboard) that includes cash, AR, and AP. This justifies the $2,500/mo enterprise price point by making the CPA look like a proactive, data-driven genius.

Epic 1: The "Hound" Agent (Swipe UP)

Objective: Eliminate the back-and-forth emails required to categorize unknown transactions.

REQ 1.1 - The Gesture: On the "Play" (Transaction Review) screen, introduce a third gesture: Swipe UP.

REQ 1.2 - The Action: Swiping UP triggers a backend mutation (dispatchHoundAgent). The transaction card is removed from the CPA's current deck and moved to a PENDING_CLIENT state.

REQ 1.3 - Autonomous SMS: The Toro backend autonomously sends an SMS to the business owner (the CPA's client): "Hi [Name], [CPA Name] is reviewing a [Amount] charge from [Vendor] on [Date]. Please reply with a quick description or a photo of the receipt."

REQ 1.4 - Resolution: When the client replies, the Toro LLM parses the response, auto-categorizes the transaction, attaches the receipt image to the ledger, and sends a push notification to the CPA confirming resolution.

Epic 2: The CFO Pocket Dashboard

Objective: Give the CPA a bird's-eye view of their entire client portfolio's financial health to provide on-the-fly advisory.

REQ 2.1 - Client Portfolio List: A new primary navigation tab (Clients). Displays a list of all businesses the CPA manages.

REQ 2.2 - Live Financial Telemetry: Tapping a client reveals a real-time dashboard calculating four strict metrics:

Current Cash Balance: Live bank feed aggregate.

Uncollected AR (Accounts Receivable): Total outstanding invoices owed to the client.

Unpaid AP (Accounts Payable): Total upcoming bills owed by the client.

True Cash Runway: Calculated dynamically: (Current Cash + AR - AP) / Monthly Burn Rate. Displayed in Months (e.g., "4.2 Months").

Epic 3: The "Pulse" (Real-Time Anomaly Alerts)

Objective: Weaponize push notifications to allow the CPA to catch fraud or errors before the client even notices.

REQ 3.1 - Anomaly Detection: The backend continuously monitors the live bank feeds for: Duplicate massive charges, unexpected international wires, or sudden drops below safety cash thresholds.

REQ 3.2 - Push Notifications: CPA receives an urgent push notification: "⚠️ Pulse Alert: [Client Name] has a suspected duplicate $12,000 payroll wire."

REQ 3.3 - Hero Action Button: Inside the alert in the app, provide a single click-to-action: "Text Client". This drafts a pre-written SMS for the CPA to send to the client immediately, proving the CPA is monitoring their account 24/7.
