# PRD: Project Toro OS (The Sub-Ledger Architecture)

**System Architecture Physics:**
* **The Event Stream (Plaid):** Raw, unverified, real-time cash movement.
* **The Sub-Ledger (Toro OS - Go/NATS):** The operational database. It holds pending states, anomalies, and runs the predictive CAS algorithms.
* **The General Ledger (QBO API):** The permanent, regulatory ground truth. Toro OS pushes formatted, categorized entries here only *after* the sub-ledger clears them.

---

## Phase 1: Liquidity & Debt (Financial Survival)
*Objective: Prevent SMB death by combining real-time bank telemetry with ledger liabilities.*

### 1. The Insolvency Radar (Zero-Day Predictor)
* **What:** A dashboard predicting the exact date an SMB will run out of cash.
* **Why:** Plaid only knows what you have today; QBO knows who you owe tomorrow. Combining them gives you the future.
* **How:** The Go Sub-Ledger pulls the live Plaid bank balance. Simultaneously, it ingests the QBO Accounts Payable (AP) ledger. It subtracts upcoming QBO bills from the Plaid live balance, outputting a deterministic "Zero-Cash Date."
* **ICP:** Cash-strapped SMBs (e.g., local retail, restaurants, construction).

### 2. Payroll Safe-Harbor Monitor
* **What:** An automated cash-guard for upcoming payroll runs.
* **Why:** Missing payroll triggers massive legal liabilities and employee flight.
* **How:** The Sub-Ledger maps the QBO Payroll Liability account against the primary Plaid operating account. If the Plaid balance is less than the QBO payroll liability 72 hours before the run date, the Sub-Ledger fires an urgent SMS to the CEO.
* **ICP:** Service businesses with heavy W-2 headcounts.

### 3. The High-Interest Debt Sniper
* **What:** A tool that flags toxic APRs mapped to specific ledger expenses.
* **Why:** Uncovering hidden debt is the fastest way a CPA can prove ROI.
* **How:** The Sub-Ledger uses the **Plaid Liabilities API** to pull live APRs. It cross-references these with the QBO Interest Expense accounts. The CPA is alerted to pitch a refinancing package if the blended APR exceeds a set threshold.
* **ICP:** Asset-heavy SMBs (e.g., dental practices with equipment financing).

### 4. Credit Utilization Optimizer
* **What:** A payment timing engine to artificially pump D&B business credit scores.
* **Why:** Lower utilization equals better future loan terms.
* **How:** Plaid Liabilities tracks the credit limit and statement dates. The Sub-Ledger checks QBO cash reserves. If cash is sufficient, it alerts the SMB to execute a payment *before* the statement closes to ensure QBO reflects optimal liability reporting.
* **ICP:** High-growth SMBs preparing for Series A or large capital expenditures.

### 5. Loan Covenant Enforcer
* **What:** A real-time monitor for commercial bank loan requirements.
* **Why:** Banks will call loans if balance sheets violate covenants (e.g., Debt Service Coverage Ratio).
* **How:** The Sub-Ledger calculates the covenant ratios using QBO's finalized Equity and Asset figures, constantly recalculating the math against Plaid's live cash feeds. Alerts the CPA if the buffer drops to 5%.
* **ICP:** Mid-market SMBs carrying commercial lines of credit.

---

## Phase 2: Reconciliation & Security (The Sub-Ledger Moat)
*Objective: Intercept and process raw data before it pollutes the QuickBooks General Ledger.*

### 6. Deterministic Diff Engine (The Shadow Ledger)
* **What:** An automated receipt-chasing and reconciliation bot.
* **Why:** Keeps QBO perfectly clean without human data entry.
* **How:** Plaid fires a transaction webhook. The Go Sub-Ledger holds it in a "pending" state and checks the QBO CDC stream for a matching receipt/entry. If no match in 24 hours, the Sub-Ledger texts the client. Once the client replies with the receipt, the Sub-Ledger marries the data and pushes a perfect, finalized journal entry to the QBO GL.
* **ICP:** Field services and contractors with high daily material expenses.

### 7. Vendor Embezzlement Trap
* **What:** An anomaly detection gatekeeper for outbound ACH.
* **Why:** Stops internal fraud before it gets buried in the GL.
* **How:** Plaid reads an outgoing ACH. The Sub-Ledger intercepts the event and queries the **QBO Vendor Master List**. If the payee is not an approved QBO vendor, the Sub-Ledger flags the transaction in the Wails app and blocks it from sinking to QBO until the CPA manually approves.
* **ICP:** SMBs with decentralized purchasing managers.

### 8. Subscription Bleed Auto-Cancellor
* **What:** An automated SaaS audit tool.
* **Why:** Cleans up bloated OPEX (Operating Expenses) automatically.
* **How:** The Sub-Ledger identifies recurring Plaid charges. It checks QBO to see how these were categorized historically (e.g., "Software & Subscriptions"). It generates a dashboard for the CEO highlighting unused tools with an ROI calculation based on QBO historical spend.
* **ICP:** Tech-forward SMBs and digital agencies.

### 9. The Multi-Entity Aggregation Hub
* **What:** A "God Mode" dashboard overlaying multiple QBO instances.
* **Why:** Serial entrepreneurs hate logging into 10 different QBO accounts.
* **How:** The Toro OS Sub-Ledger pulls data via API from 10 distinct QBO GLs and 10 distinct Plaid bank connections, normalizing them into one unified, real-time Sub-Ledger view for the CPA's daily monitoring.
* **ICP:** Real estate investors and serial entrepreneurs with complex holding structures.

### 10. Expense Misclassification Auto-Corrector
* **What:** An NLP engine mapping messy bank text to the QBO Chart of Accounts.
* **Why:** Eliminates the manual drop-down menu sorting in QBO.
* **How:** Plaid delivers a messy string ("SQ* Bobs Hardware"). The Sub-Ledger's NLP parses it, queries the specific client's **QBO Chart of Accounts (CoA)**, maps it to "Cost of Goods Sold - Materials," and auto-creates the QBO entry. 
* **ICP:** High-volume transaction SMBs (e-commerce, local coffee shops).

---

## Phase 3: Compliance & Tax (Government Abstraction)
*Objective: Automate compliance by syncing live bank reality with tax ledger rules.*

### 11. The Estimated Tax Siphon
* **What:** An automated quarterly tax withholding engine.
* **Why:** Prevents end-of-year tax bankruptcy.
* **How:** The Sub-Ledger pulls live inbound revenue from Plaid. It references the QBO GL to calculate YTD profitability and deductible expenses. It accurately calculates the estimated 15-25% tax burden and writes an "Accrued Tax Liability" to the QBO GL while alerting the client to move the cash.
* **ICP:** Freelancers, high-margin solo LLCs, and service businesses.

### 12. 1099 Contractor Auditor
* **What:** An automated W-9 compliance enforcer acting as a pre-ledger filter.
* **Why:** Saves the CPA from manual vendor audits in January.
* **How:** Plaid registers an outgoing payment. The Sub-Ledger checks the QBO Vendor list to see if a W-9 flag is checked. If total Plaid payments to that vendor exceed $600 YTD and no W-9 is logged in QBO, the Sub-Ledger auto-emails the contractor the request form.
* **ICP:** Homebuilders and marketing agencies relying on 1099 labor.

### 13. Audit-Proof PDF Generator (The IRS Shield)
* **What:** A cryptographic proof-of-reconciliation engine.
* **Why:** IRS audits require proof that the bank statement matches the GL exactly.
* **How:** Uses the **Plaid Assets API** to pull immutable bank statements, and maps them 1:1 against the QBO General Ledger entries within the Sub-Ledger. Generates a PDF proving zero discrepancies over a 24-month period.
* **ICP:** Cash-heavy businesses or those with high audit risk.

### 14. Franchise Royalty Verifier
* **What:** An automated royalty calculator mapping ledger rules to live cash.
* **Why:** Prevents legal disputes with franchisors over gross revenue definitions.
* **How:** The Sub-Ledger uses the QBO CoA to define exactly which revenue streams are subject to franchise fees. It reads Plaid for the live cash receipts matching those CoA rules, calculates the royalty, and pushes a bill to the QBO AP ledger automatically.
* **ICP:** Local franchisees (e.g., McDonald's, F45 Fitness).

### 15. Instant Vendor KYC
* **What:** A wire-fraud prevention gatekeeper for the GL.
* **Why:** Hackers spoof invoices; QBO doesn't verify identities.
* **How:** When a new vendor is added to QBO, the Sub-Ledger intercepts. It uses the **Plaid Identity API** to ensure the legal name on the vendor's bank account matches the name just entered into QBO. If it fails, the Sub-Ledger quarantines the QBO vendor profile.
* **ICP:** Manufacturing and logistics SMBs with large supply-chain payments.

---

## Phase 4: Growth & Advisory (The Revenue Moat)
*Objective: Use the Sub-Ledger to generate predictive financial insights that CPAs can sell at a premium.*

### 16. Automated Capital Readiness (The Loan API)
* **What:** An instant, underwriter-ready financial package generator.
* **Why:** Underwriters need historical GAAP financials AND proof of current liquidity.
* **How:** The Sub-Ledger queries QBO for the historical Trailing-12-Month (TTM) P&L and Balance Sheet, merging it with **Plaid Assets** to verify current liquid cash. It bundles this into a standardized bank loan package in seconds.
* **ICP:** SMBs seeking equipment financing or commercial real estate loans.

### 17. Idle Cash Opportunity Cost Calculator
* **What:** A yield-loss simulation engine.
* **Why:** Agitates the client to upgrade to treasury management advisory.
* **How:** The Sub-Ledger calculates average daily Plaid balances. It checks QBO to see if any cash is mapped to interest-bearing accounts. It simulates the lost yield against current T-Bill rates and pushes a "Lost Revenue" metric to the CPA's advisory dashboard.
* **ICP:** Highly profitable, cash-rich professional services.

### 18. B2B Churn Predictor
* **What:** An early-warning system for client churn using AR data.
* **Why:** A late invoice in the GL is just math; a late invoice in reality is a lost client.
* **How:** The Sub-Ledger ingests the QBO Accounts Receivable (AR) aging report. It monitors Plaid for expected inbound wires. If a historically reliable client hits 5 days past due on the QBO AR without a matching Plaid event, the Sub-Ledger triggers a "Churn Warning" for the CEO.
* **ICP:** B2B service providers and SaaS companies on monthly retainers.

### 19. Customer LTV (Lifetime Value) Tracker
* **What:** A dynamic dashboard identifying an SMB's most lucrative clients.
* **Why:** QBO tracks total revenue, but struggles to cleanly track LTV over multi-year periods of messy payments.
* **How:** The Sub-Ledger ties Plaid inbound payment identities to QBO customer profiles. It maintains a running LTV leaderboard outside of QBO, allowing the SMB to identify and prioritize their absolute best clients.
* **ICP:** High-ticket B2B services or recurring B2C businesses.

### 20. Competitor Ad-Spend Spy
* **What:** An anonymized industry benchmarking tool.
* **Why:** SMBs want to know what the guy across the street is doing.
* **How:** Because Toro OS is the Sub-Ledger for hundreds of CPAs, it can query the QBO Chart of Accounts globally. It aggregates anonymized marketing spend (e.g., Google Ads) from QBO and compares it to gross revenue from Plaid, generating actionable industry benchmarks.
* **ICP:** Local businesses in highly competitive, ad-driven markets (HVAC, roofing, law firms).