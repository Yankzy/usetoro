Product Requirements Document (PRD)

Project: Toro Platform - The Final Evolution
Module: The Omni-Agent & Skill Ecosystem
Objective: Abstract complex multi-agent orchestration into a single, user-friendly "Digital Employee" equipped with expandable "Skills."

1. The Core Philosophy

CPAs do not want to manage a fleet of autonomous AI bots; they want one highly capable assistant.
The Omni-Agent ("Toro") is the single unified persona the CPA interacts with across Mobile (Fignode) and Desktop. The CPA "trains" Toro by equipping him with Skills. Under the hood, these Skills are entirely separate, specialized AI agents orchestrated over a NATS JetStream event bus.

Crucially, Toro operates within a true Agentic Economy. Agents are provisioned with digital wallets and pay for their own compute on a per-token basis.

2. The UX/UI: The Unified Chat Interface

The CPA interacts with Toro via a persistent chat interface available on every screen of the app.

REQ 2.1 - The Slash Command (/) Menu: Typing / opens a frictionless menu of the CPA's equipped Skills (e.g., /ocr, /ripple, /forecast). This trains the user on what the AI is capable of without guessing.

REQ 2.2 - Natural Language Triggering: If the CPA ignores slash commands and types, "Extract the totals from these 10 invoices and check for duplicates," the UI must visually indicate which Skills Toro is activating.

UI Animation: Small badges appear above the chat bubble: [⚙️ Triggering Skill: OCR] -> [⚙️ Triggering Skill: Anomaly Detection].

REQ 2.3 - The "Skill Store" & Agent Wallet: A dedicated tab in the app where CPAs can browse and "Install" new skills to their agent. Installing a skill is free. This tab also houses the Agent Wallet, where the CPA pre-funds their digital employee's budget to pay for skill execution.

3. Backend Architecture: The Gateway Router Pattern

The frontend never talks directly to specialized agents. It only talks to the Gateway.

REQ 3.1 - The Gateway Router (Intent Classification): * Every user prompt hits the Gateway LLM.

The Gateway is configured with a strict tools array defining all the Skills the specific CPA's tenant_id has installed.

The Gateway determines which Skill(s) are required to fulfill the prompt.

REQ 3.2 - NATS JetStream Dispatch: * Instead of executing the task, the Gateway emits an event to NATS: Publish("skill.ocr.execute", payload).

The Gateway places a correlation_id on the event and waits.

REQ 3.3 - Micro-Agent Execution & Metering: * Specialized micro-agents (running on optimized hardware/models) listen to their specific NATS topics.

The OCR Agent processes the PDFs, calculates the total tokens consumed, and replies to skill.ocr.reply.{correlation_id} with the data payload and a billing receipt.

REQ 3.4 - The Synthesis Response: * The Gateway Router catches the reply, debits the Agent Wallet, formats a human-readable chat response, and pushes it via WebSocket to the CPA's screen.

4. Phase 1: The Core Skill Roster (Claude Code Inspired Primitives)

Inspired by leading agentic hubs like Claude Code, Toro's core skills are built as Atomic Primitives. Instead of narrow, hardcoded features, we give the AI fundamental read/write/execute capabilities that it can chain together to solve dynamic accounting problems.

The Omni-Agent acts as the planner, calling these skills as tools and prompting the CPA for approval before committing database changes.

The "Read & Search" Primitives

/query (Semantic Ledger Search): The equivalent of grep for accounting. Allows the agent to query the entire database and document vault using natural language.

Usage: "Find all software subscriptions under $50 that we haven't used in 3 months."

/ocr (Data Extraction): The equivalent of cat or read_file for physical documents.

Usage: "Read this W-9 image and extract the EIN to the vendor profile."

The "Write & Mutate" Primitives

/mutate (Targeted DB Edit): The equivalent of an AST editor. Allows the agent to propose a direct edit to a vendor record, chart of accounts mapping, or transaction, with a dry-run diff presented to the CPA.

Usage: "Update Acme Corp's payment terms to Net-60 and apply it to their outstanding invoices."

/ripple (Bulk Execution Primitive): The equivalent of running a bash script. Finds a pattern, applies a restatement, and logs a cryptographic audit trail.

Usage: "Find all transactions matching this pattern and reclassify them to Owner's Draw." (Executes the Toro Ripple PRD).

The "Architect & Synthesize" Primitives

/simulate (Scenario Planning): The equivalent of the "Architect" role. Allows the agent to branch the database state in memory and run Monte Carlo simulations without affecting the production ledger.

Usage: "Run a liquidity stress test if we hire 2 engineers at $120k next month."

/armor (Defense Generation): A synthesis skill that combines transaction data with the Vector DB of tax law to generate legal documentation.

Usage: "Generate a tax court memo for this aggressive deduction." (Executes the Audit Armor PRD).

/ifta (Compliance Calculator): A specialized compliance synthesis tool.

Usage: "Calculate my fleet's quarterly fuel taxes based on ELD data."

5. The Agentic Economy (Monetization & Wallets)

We are completely abandoning the concept of flat SaaS subscriptions for advanced features. Skills are not paid for in advance; they are participants in a pure, token-based micro-economy.

The Agent Wallet: To use premium skills, the CPA must fund their digital employee. Every Omni-Agent has a Wallet (managed via Stripe auto-recharge). The CPA gives the agent a budget (e.g., $100/month), and the agent spends it autonomously to execute tasks.

Token-Based Execution: Skills are priced dynamically based on compute. When the Gateway Router dispatches an intent to a micro-agent, the system tracks the exact token usage of the underlying LLM processing that specific skill.

The 90% Infrastructure Margin: Toro acts as the infrastructure tollbooth. If the underlying foundational model (e.g., Google Vertex AI) charges $5.00 per 1M tokens, Toro applies a strict 90% margin. The agent's wallet is debited the marked-up cost upon successful execution.

The B2B Skill Marketplace: In Phase 2, enterprise CPA firms and independent developers can build proprietary Skills on the Toro platform. When another user's agent pays tokens to execute a third-party Skill, Toro splits the 90% markup margin with the creator. This transforms Toro from a software vendor into the foundational economy for Agent-to-Agent (A2A) commerce.