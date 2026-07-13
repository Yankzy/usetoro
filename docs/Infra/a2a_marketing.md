# Product Requirement Document (PRD)

## 1. Executive Summary & Problem Statement

Traditional B2B lead generation is broken. Marketers spend thousands spamming businesses blindly, while business owners waste cognitive load filtering junk emails.

AgentGate solves this by building an AI-native marketplace where a company's private AI agent acts as a strategic gatekeeper. To solve the marketplace cold-start problem without triggering a recursion bug (selling marketing tools to marketing agencies), the platform launches as an operational inbound email utility for regular businesses before activating an anonymized, token-backed marketplace.

---

## 2. Core Architecture & System Flow

```
[Inbound Email/Lead] ──► [Phase 1: Auto-Responder Engine] ──► (Establishes Trust)
                                    │
                                    ▼
[Owner Voice/Text]   ──► [Phase 2: Git-Diff State Machine] ──► Updates business_state.json
                                    │
                                    ▼
[Marketplace Layer]  ──► [Section 2.5.4: Simulation Engine] ◄── Scraped & Synthetic Offers
                                    │
                                    ▼
[Phase 3 Deployment] ──► [Live Token-Gated Marketplace] ──► Real Agencies Pay Tokens

```

---

## 3. Detailed Functional Requirements

### Phase 1: The Single-Player Hook (Inbound Lead Auto-Responder)

The goal of this phase is to provide immediate operational value to non-marketing businesses (e.g., e-commerce, home services, SaaS) to get them into the ecosystem.

* **1.1 Email Client / API Onboarding**
* The system must connect securely to user inboxes via IMAP/SMTP OAuth (Gmail/Outlook) or provision an explicit inbound routing email alias (e.g., `leads@yourcompany.agentgate.ai`).


* **1.2 Automated Context Ingestion**
* Upon signup, the user provides their website URL.
* An internal scraping worker must crawl the site, extract core copy, product/service offerings, pricing structures, and FAQs, and save this data into a vector embedding store.


* **1.3 AI Lead Concierge**
* When an inbound inquiry or contact form email hits the inbox, a webhook must trigger an LLM prompt.
* The LLM drafts a hyper-personalized response using the company's vector context.
* **Behavior:** The system must save the response directly into the business's email "Drafts" folder and send an SMS/Slack notification to the owner: *"Draft response ready for customer [Name]."*



### Phase 2: The Core Agent & Git-Diff State Tracking

This module transitions the tool from a reactive email assistant into a proactive, state-aware strategic agent.

* **2.1 Low-Friction Briefing Interface**
* The system must interface directly with users where they already communicate (WhatsApp API, Slack Webhook, or SMS).
* The agent pings the owner on a recurring schedule (e.g., every Monday morning).


* **2.2 Voice-to-Text Ingestion Engine**
* The platform must accept raw audio file uploads (voice notes) via the messaging interface.
* Audio must be passed directly through a transcription API (e.g., Whisper) to extract raw text data.


* **2.3 The Business Git-Diff State Machine**
* The platform must maintain a centralized, structured file tracking the company's parameters: `business_state.json`.
* **Fields required:** `current_bottlenecks`, `revenue_targets`, `marketing_channels`, `active_vendor_spend`, `inventory_surplus`.
* When new raw text or voice data is received, an LLM evaluates the input against the previous JSON state.
* **The Delta Log:** The system computes the *diff* (the delta). If nothing changed, no action is taken. If parameters changed (e.g., spending too much on an agency, or stock inventory is too high), the state updates and creates a new version log in the database.



### Section 2.5.4: Simulating the Marketplace (Liquidity Engine)

This is the core programmatic middle ground designed to eliminate the cold-start problem. The system will mock the buy-side marketplace so early business users receive matching value immediately, before real agencies are onboarding.

* **2.5.4.1 Public Offer Scraping Worker**
* The system must run a scheduled cron job (daily/weekly) to programmatically scrape active B2B marketing agencies, public freelance platforms (e.g., Upwork public RSS/API, Fiverr), and agency directory sites.
* **Data Fields Extracted:** Agency name, core specialty (SEO, PPC, Email), baseline pricing, case study claims, and promotional offers.


* **2.5.4.2 Synthetic Marketer Profile Generation**
* An LLM background worker must parse the raw scraped data and transform it into standardized, agentic marketer personas stored in a dedicated `synthetic_agents` database table.


* **2.5.4.3 The Shadow Matcher Algorithm**
* When a real business owner updates their state via a `git-diff` change (e.g., *"We are paying $3,000/month for Facebook ads and the ROI is dropping"*), the system triggers a background semantic search.
* The algorithm matches the business's open bottleneck vector against the `synthetic_agents` capabilities database.


* **2.5.4.4 The Outbound Illusion Notification**
* If a highly compatible match is found (e.g., a scraped agency offering specialized Facebook ad audits for $1,000), the business's AI agent pings the owner:
> *"Boss, an agency specializing in your exact niche just submitted an optimized offer to cut your ad spend by 30%. Would you like me to open a secure channel to review their proposal?"*


* If the owner says yes, the platform flags this lead internally for manual or semi-automated outreach to the real scraped agency to finalize the connection.



### Phase 3 & 4: The Live Marketplace & Token Economy

Once the platform achieves liquidity (e.g., 100+ active businesses interacting with their agents), the marketplace layer flips from simulated to live.

* **3.1 Anonymization Layer**
* The marketplace interface must strictly mask all identifying business details.
* **Visible Data to Sellers:** Niche, rough scaling metrics, and active structural problems (e.g., *"Fashion e-commerce brand doing $40k MRR needs a retention marketing strategy to fix high cart abandonment"*).


* **3.2 Token Paywall Architecture**
* Real marketing agencies must register accounts and purchase a native utility asset: **Tokens**.
* To pitch an anonymized business need, an agency must spend a set amount of tokens (e.g., 10 tokens per pitch).


* **3.3 Agent Gatekeeping and Filtering Logic**
* When an agency submits a proposal, it does *not* go to the human owner. It goes directly to that company's private agent.
* The agent parses the proposal against the owner's explicit constraint rules stored in `business_state.json`.
* **Rejection/Acceptance:** If the pitch is generic or overpriced, the agent auto-rejects it (tokens are consumed). If it passes the evaluation matrix, it is queued for the owner's next briefing summary.



---

## 4. Data Security & Privacy Controls

* **Data Isolation:** Raw inbox data from Phase 1 and transcript logs from Phase 2 must be encrypted at rest and in transit. They can never be exposed to the public marketplace or used to train public LLM models.
* **Masking Algorithm:** Before a business state change is pushed to the marketplace discovery feed, an LLM filter must parse the text to explicitly strip out brand names, names of competitors, specific geolocation details, and precise revenue numbers (normalizing them into broad brackets instead).

---

## 5. Non-Functional Requirements & Performance Metrics

* **Latency:** Audio transcriptions and git-diff state updates must process within 45 seconds of submission.
* **Target MVP Scalability:** System architecture must support up to 500 concurrent scrapers and 10,000 synthetic agent profiles without degradation of semantic search matching performance.
* **Database Choice:** PostgreSQL with `pgvector` enabled to handle relational user data and semantic vector matching inside a single unified system.