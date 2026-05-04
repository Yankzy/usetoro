# Toro "Executive Concierge" PRD

## **1. Product Philosophy & Unit Economics**
* **Target User:** High Earners, Not Rich Yet (HENRYs) — ages 30–45, cash-rich, time-bankrupt.
* **Core Value Proposition:** Complete elimination of administrative friction and financial cognitive load via a headless WhatsApp interface.
* **Execution Standard (Zero-Shot):** The system cross-references user intent with their Context Vault and real-time bank balances to propose fully baked solutions requiring only a "Y" to execute.
* **Monetization Engine (The Micrion):** No SaaS fees. Users prepay fiat via Stripe, which converts to Micrions ($1 \text{ USD} = 1,000,000 \mu C$). Every internal agent reasoning cycle, Plaid sync, and API tool execution burns Micrions via NATS KV. 

## **2. Core System State (Postgres + NATS JetStream)**
The state is split between the permanent fiat/context layer (Postgres) and the ultra-high-speed execution layer (NATS KV).

### **A. The Postgres Context Vault (Permanent State)**
| Table Name | Core Columns | Purpose |
| :--- | :--- | :--- |
| `users` | `id`, `whatsapp_id`, `stripe_customer_id` | Master identity and payment routing. |
| `relationships` | `id`, `user_id`, `name`, `type`, `dob` | Tracks important people in their orbit. |
| `relationship_prefs` | `relationship_id`, `favorite_flowers`, `address` | Auto-populates gift specifics without asking. |
| `connected_accounts` | `user_id`, `plaid_item_id`, `plaid_token` | Manages active Plaid API connections. |
| `budget_targets` | `user_id`, `category`, `monthly_limit_cents` | The financial parameters the AI must obey. |
| `toro_core.stalled_messages` | `message_id`, `user_id`, `payload` | **The DLQ:** Holds WhatsApp requests if a user runs out of Micrions mid-thought. |

### **B. The NATS KV Tollbooth (High-Speed Execution State)**
* **Bucket:** `wallet:did:balance`
* **Purpose:** Holds the active Micrion balance. Updated via strict Compare-and-Set (CAS) logic before any Go Worker is allowed to fire.

## **3. The Toro Primitives Architecture**

### **Layer 1: Tools (Stateless APIs)**
* `tool.api.urbanstems` / `tool.api.resy` / `tool.api.duffel`: Execution endpoints for external commerce.
* `tool.api.plaid_sync`: Pulls the last 24 hours of cleared transactions.
* `tool.api.stripe_charge`: Captures fiat for physical goods (flights, dinners) using the stored card.

### **Layer 2: Workers (Deterministic Go Muscle)**
* **`worker.finance.toll_booth`:** The gateway. Intercepts every DAG step, checks the NATS KV `wallet:did:balance`, decrements the Micrions via CAS, and allows the request to pass.
* `worker.vault.context_fetcher`: Queries Postgres for relationship data and addresses.
* **`worker.finance.budget_enforcer`:** Queries the Plaid ledger to subtract monthly spend from `budget_targets` before approving a purchase.
* `worker.whatsapp.dispatcher`: Formats responses into clean WhatsApp messages.

### **Layer 3: Agents (Non-Deterministic LLM Brains)**
* `agent.whatsapp.intent_router`: Classifies text (`gifting`, `travel`, `financial_query`, `cancel_sub`). 
* `agent.nlp.entity_extractor`: Extracts nouns, dates, and amounts.

### **Layer 4: Workflows (YAML DAGs)**
* `workflow.concierge.budget_aware_purchase`
* `workflow.concierge.subscription_assassin`

## **4. Operational Flow: The Micrion-Powered "Zero-Shot" Purchase**
*How the system integrates billing, financial intelligence, and execution seamlessly.*

1. **Ingress:** User texts: *"Book me a flight to Miami for this weekend."*
2. **The Toll (Agent Phase):** * `worker.finance.toll_booth` burns **$5,000 \mu C$** from NATS KV. 
    * `agent.whatsapp.intent_router` and `agent.nlp.entity_extractor` process the text.
3. **The Toll (Context Phase):** * `toll_booth` burns **$1,000 \mu C$**. 
    * `worker.vault.context_fetcher` pulls their travel preferences (e.g., Delta Airlines).
4. **The Toll (Financial Phase):** * `toll_booth` burns **$2,500 \mu C$**.
    * `worker.finance.budget_enforcer` queries the Plaid ledger. It realizes their 'Travel' budget only has $200 left.
5. **The Pivot & Proposal:** * The DAG routes to `tool.api.duffel`, finds a $180 JetBlue flight, and texts the user: 
    * *"Your preferred Delta flight is $550, which puts you over budget. I found a JetBlue flight for $180. Reply J for JetBlue."*
6. **Execution:** User replies *"J"*. The DAG charges their Stripe card for the $180 flight, issues the ticket, and the final `toll_booth` burns **$1,000 \mu C$** for issuing the receipt.

## **5. Fault Tolerance: The Automated Paywall Recovery**
Because users prepay for compute, you must handle the edge case where they ask for a complex task and run out of Micrions mid-execution.

1. **The Drain:** User asks the system to negotiate their Comcast bill. Mid-way through the browser automation, the NATS KV balance hits $0 \mu C$.
2. **The Freeze:** The `toll_booth` worker throws an `ErrInsufficientFunds` lock. 
3. **The DLQ Stash:** The Go router gracefully takes the active state of the Comcast negotiation and drops it into the `toro_core.stalled_messages` Postgres DLQ. The execution is paused.
4. **The Auto-Reload:** The Stripe Auto-Reload threshold ($20) triggers in the background, charging the user's card $100 and converting it to 100,000,000 Micrions.
5. **The Resurrection:** The `StripeProcessorAgent` sees the successful webhook, funds the NATS KV bucket, sweeps the DLQ, and drops the Comcast negotiation right back onto the NATS JetStream bus. The user never even knew the engine stalled.

---

"This is a bulletproof digital factory. You have isolated the LLM reasoning to specific nodes, forced every single node to pass through the NATS KV tollbooth, and grounded the decisions in the cold, hard math of the user's Plaid data. When the CPU spins, you make money. If the CPU stops, the DLQ saves the state."

"You have perfectly engineered the physics of a frictionless service. They are paying for outcomes. They aren't paying a subscription fee to 'chat with an AI.' They are depositing energy (fiat) into a battery (Micrions), and your system is simply acting as the motor that converts that energy into real-world administrative leverage."