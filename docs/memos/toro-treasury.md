# **OPERATIONAL MEMO: TORO TREASURY (B2B SaaS)**
**DATE:** April 9, 2026
**FROM:** Toro OS Product Strategy (US HQ / Casablanca Ops)
**TO:** Engineering, Compliance, & AI PM Pipeline
**SUBJECT:** System Architecture & Requirements for "Toro Treasury" (Hampton White-Label)

**1. PRODUCT DEFINITION & SCOPE**
* **Product Name:** Toro Treasury (White-label ready for "Hampton Wealth").
* **Target User (ICP):** High-Net-Worth CEOs and SMBs doing $5M–$50M in revenue.
* **Core Value Proposition:** An automated, non-discretionary cash management tool that monitors corporate bank accounts and sweeps idle cash into safe, 5% yield US Treasury Bills (e.g., SGOV) to combat inflation. 
* **Business Model:** Flat-fee SaaS subscription (e.g., $500/month). **Strictly NO Assets Under Management (AUM) fees or profit-sharing.**

**2. REGULATORY & COMPLIANCE FIREWALL (CRITICAL NFRs)**
Toro OS is a US-registered entity. To avoid the multi-million dollar burden of registering as an SEC Registered Investment Adviser (RIA) or an unlicensed bank, the software must strictly adhere to the following rules:
* **Non-Discretionary Execution:** The AI *cannot* make investment decisions. The user must manually define a rigid mathematical rule during onboarding (e.g., *"Maintain $150,000 in checking, sweep any excess into US T-Bills"*). The system acts strictly as an Order Execution Management System (OEMS).
* **Flow of Funds:** Toro OS must never hold client funds. Capital must flow directly from the user's corporate bank account to their own custodial account at the Broker-Dealer (Alpaca) via ACH. 
* **WORM Compliance:** All trade execution triggers routed through the event broker must be logged in a US-based, WORM-compliant (Write Once, Read Many) AWS/GCP storage bucket to satisfy SEC Rule 17a-4.

**3. SYSTEM ARCHITECTURE & MICROSERVICES**
The system uses a decoupled, event-driven architecture using Wails/React Native for the client layer, Go for the deterministic core, and Python for the API integrations.

* **The Brain (Go + NATS JetStream + Postgres):** Deployed in a US data center. Acts as the deterministic event router. It stores the user's sweeping rules, listens for balance updates, runs the math, and publishes execution commands to NATS.
* **Read Node (Python + Plaid API):** A stateless microservice that ingests real-time bank balance updates and publishes them to NATS (`plaid.balance.updated`).
* **Write Node (Python + Alpaca API):** A stateless microservice that listens to NATS for `trade.execute` events. It handles KYB (Know Your Business) document routing for onboarding, initiates ACH transfers, and executes the T-Bill trades.
* **Reporting Node (Python + Alpha Vantage API):** A background worker that pulls macro bond yield data to generate automated, white-labeled monthly PDF reports detailing the exact interest earned for the CEO.

**4. CORE USER WORKFLOWS (FOR PRD MAPPING)**
* **Workflow A: KYB Onboarding.** User signs up -> Prompts for EIN, Articles of Incorporation, and UBO IDs -> Python routes to Alpaca API -> Account approved.
* **Workflow B: Plaid Link & Rule Setting.** User links Chase account via Plaid -> User configures "Safe Buffer" amount (e.g., $100k) -> Rule saved to Postgres.
* **Workflow C: The Deterministic Sweep (Daily Loop).** 1. Plaid microservice reads balance ($250k). 
    2. Go engine calculates delta against the rule ($250k - $100k rule = $150k idle cash). 
    3. NATS routes execution payload to Alpaca. 
    4. Alpaca initiates $150k ACH pull and buys T-Bills.
* **Workflow D: The Withdrawal.** User clicks "Withdraw $50k" in Wails/React Native app -> NATS triggers Alpaca to sell $50k in T-Bills and push ACH back to Chase account for payroll.
