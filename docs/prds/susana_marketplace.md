*(Assuming "bus route" was a voice-to-text translation of "Based on that" or "Let's rewrite"!) *

Here is the revised, engineering-ready Product Requirements Document (PRD) incorporating the **Multi-Dimensional Skill Graph**, the **Web Command Center** for contractors, and the **SMS interface** for workers. 

***

# Product Requirements Document (PRD): Susanna AI Labor Network (V2)

## 1. Executive Summary
**Vision:** To build the largest, most precise labor liquidity network for the blue-collar trade economy.
**Product:** A hybrid marketplace featuring a "Discord-style" Web Forum for General Contractors (demand) and an SMS/WhatsApp-native interface for temporary laborers (supply). It replaces generic 5-star ratings with a **Multi-Dimensional Skill Graph**, matching workers to contractors based on specific trades, tools, and verified trade-specific reputation.
**Strategic Goal:** Achieve zero-friction worker onboarding via SMS while monetizing contractors through a high-value, AI-powered Web Command Center.

## 2. Problem Statement
* **Generic ratings fail in construction:** A 5-star painter is useless if a contractor needs a 5-star framer. 
* **Contractors** need visual, rich data to build a crew (knowing exactly who has a hammer, who has a brush, and who has a mower) without wading through chaotic text threads.
* **Workers** will not download apps, but they need a portable, cryptographically verified resume of their specific skills to command better pay.

## 3. Target Personas & UI Split
* **Persona A: The Worker (Supply) -> UI: SMS & WhatsApp.** Operates entirely via text. Uses emojis to define skills. Responds to job blasts instantly.
* **Persona B: The Contractor (Demand) -> UI: Susanna Web Command Center.** Needs a visual dashboard to search the labor pool, filter by rich data (icons/skills), and deploy targeted SMS job blasts.

---

## 4. Core Features & User Stories

### Feature 1: AI-Driven Skill Onboarding (SMS)
**Description:** Workers build their profile using natural language and emojis over text, which the AI translates into structured database categories.
* **User Story (Worker):** As a worker, I want to text Susanna what I am good at (e.g., "I do painting and drywall"), so the AI can assign me the correct skill badges (🖌️, 🧱).
* **User Story (Worker - Bootstrap):** As a worker, I want to provide phone numbers of past contractors specific to my trade, so I can start with verified ratings in those specific skills.
* **User Story (System):** The LLM must parse inbound worker texts and map unstructured trade descriptions to a rigid set of 15 core Toros OS trade categories.

### Feature 2: Multi-Dimensional Reputation Protocol
**Description:** Ratings are no longer global; they are tied explicitly to the skill performed on that specific job.
* **User Story (Contractor - Rating):** At the end of a job, I want Susanna to ask me to rate the worker *specifically on the trade they performed* (e.g., "How was David's Painting 🖌️ today?"), so the data remains accurate.
* **User Story (Worker - Resume):** As a worker, I want my profile to reflect my exact strengths (e.g., Painting: 4.9 Stars, General Labor: 4.2 Stars), so I get matched to the right jobs.

### Feature 3: The Susanna Web Command Center (The Contractor Forum)
**Description:** A web-based SaaS dashboard where contractors can visually browse the labor pool and use AI to filter candidates.
* **User Story (Contractor - AI Search):** As a contractor, I want to type into a search bar ("Need 2 drywall guys over 4.5 stars in 75204 for tomorrow"), so the AI can instantly display a curated list of matching workers.
* **User Story (Contractor - Rich UI):** As a contractor, I want to see worker profiles displaying visual icons (🔨, 🖌️, 🚜) alongside their specific ratings, so I can evaluate their fit in seconds.
* **User Story (Contractor - Checkbox Blast):** As a contractor, I want to check a box next to 5 specific painters on the web dashboard and click "Invite," seamlessly triggering targeted SMS blasts to those workers.

### Feature 4: Targeted SMS Dispatch & Claim System
**Description:** The bridge between the Contractor's Web UI and the Worker's SMS inbox.
* **User Story (Worker):** As a worker, I want to receive SMS job blasts that are highly relevant to my specific skill set and reply "MINE" to claim the shift.
* **User Story (System):** Toro OS must immediately close the job slot once the requested headcount is reached and notify any subsequent workers that the roster is full.

---

## 5. Out of Scope (Legal & Liability Guardrails)
To prevent Toro OS from being classified as a W-2 employer or Temp Agency:
* **NO Payroll or FinTech Escrow for Labor:** Contractors are 100% responsible for paying workers off-platform (cash, Zelle, check). Toro OS is strictly an introduction and communication protocol.
* **NO Immigration/I-9 Verification:** Toro OS does not collect SSNs or verify legal work status. We are an open messaging forum. 
* **NO General Platform Ratings:** Workers cannot be rated on broad "attitude." Ratings must be strictly tied to a completed trade skill to prevent subjective bias and maintain data integrity.

---

## 6. Monetization Strategy
* **Workers:** 100% Free forever. This guarantees massive labor liquidity.
* **Contractors (Freemium):**
  * *Basic Tier:* Can receive SMS texts when workers bootstrap ratings. Can use basic SMS to blast their existing personal list. 
  * *Pro Tier ($99/month):* Unlocks the **Susanna Web Command Center**. Gives access to the AI Search Bar, the Open Forum directory of the entire city's labor pool, rich data filtering, and the ability to cherry-pick 5-star workers outside of their personal network.

---

## 7. Technical Architecture (Go Backend)
* **Ingress/Egress:** * Workers: Twilio API (SMS) / WhatsApp Business API.
  * Contractors: Web UI built on Toro OS, connected via WebSockets for real-time claim updates.
* **Database (Postgres Schema Updates):**
  * `Workers` Table: Base identity (Phone, Zip).
  * `Worker_Skills` Table (One-to-Many): Maps Worker ID to Trade ID, stores the `Current_Rating` and `Total_Reviews` for that specific skill.
  * `Jobs` Table: Stores required skills, headcount, and location.
* **AI Layer:** * **Intake:** LLM categorizes natural language SMS from workers into strict database enums (e.g., "I lay brick" -> `Trade_ID: Masonry`).
  * **Search:** LLM translates the contractor's natural language web search into SQL/Vector queries to return the exact right workers to the UI.