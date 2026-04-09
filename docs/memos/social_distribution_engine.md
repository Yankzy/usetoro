# **INTERNAL STRATEGY MEMO: TORO OS**
**DATE:** April 8, 2026
**TO:** Toro OS Engineering
**SUBJECT:** Architecture of the "Fractional CMO" Social Distribution Engine

**1. EXECUTIVE SUMMARY**
Toro OS is expanding beyond core accounting into automated revenue generation for CPAs. We are building the **Fractional CMO Module**, an AI-driven social media engine that transforms our CPAs into local thought leaders while simultaneously using their networks as a zero-CAC (Customer Acquisition Cost) distribution channel for the Toro OS platform.

**2. THE SYMBIOTIC ALIGNMENT (THE 80/20 RULE)**
We cannot treat the CPA's LinkedIn feed as a free billboard, or they will churn. We must use the "Jab, Jab, Jab, Right Hook" framework to build their audience first.
* **The Jabs (80%):** Pure organic value. High-signal posts about the CPA's specific industry niche and tax philosophy. This strokes the CPA's ego, builds their local authority, and generates inbound leads for *their* firm.
* **The Trojan Horse (20%):** Once a week, the system generates a post that highlights a massive client win, explicitly attributing that win to the advanced algorithmic capabilities of the Toro OS platform. 

**3. INGESTION LAYER: THE "CPA DNA" PROMPT**
To prevent the LLM from generating generic, low-quality marketing copy, we must force the CPA to define their unique market positioning during the module's onboarding phase. This data becomes the permanent `System Prompt` for their specific AI agent.
The CPA must complete three mandatory fields:
1. **Industry Focus:** (e.g., E-commerce, Real Estate, SaaS startups)
2. **The Common Trap:** What is the most expensive mistake you constantly see new clients making?
3. **Firm Philosophy:** Are you aggressive on tax strategy, or highly conservative and audit-proof?

**4. ENGINEERING WORKFLOW (THE AUTOMATED QUEUE)**
The UX must be entirely frictionless. The CPA should not have to type a single word.
* **The Generation Cron:** Every Sunday at 2:00 AM, the Go backend spins up an LLM job. It digests the "CPA DNA" and generates exactly four social media posts (3 Organic, 1 Trojan Horse).
* **The UI / UX:** The CPA logs in on Monday morning and clicks the "Marketing" tab. They see a clean calendar view with the four pre-written posts. 
* **The Single-Click Action:** The CPA reads the posts and clicks one master button: `Approve Week`. 
* **The Dispatch:** The Go backend securely stores their OAuth tokens and automatically schedules and dispatches the posts to the LinkedIn and X (Twitter) APIs over the next 7 days.

**5. PRICING & DEPLOYMENT STRATEGY**
This is not included in the base Toro OS subscription. This is a premium **$500/month add-on module**. Because it directly generates new billable clients for the CPA, it shifts Toro OS from an operational expense to a primary revenue driver, driving platform churn to absolute zero.
