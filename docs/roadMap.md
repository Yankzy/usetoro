Here is the **Week-by-Week Technical Roadmap** to get from "Zero" to "Level 1" (Selling Fignode to the first CPA).

---

### Phase 1: The Foundation (Weeks 1-4)

*Goal: A working "Digital Spine" that can ingest data and run simple logic.*

**Week 1: The "Go Gate" (Ingress)**

1. [x] **Repo Setup:** Initialize `github.com/toro/core` (Monorepo).
2. [x] **Postgres:** Setup `pgx/v5` connection pool. Create `tenants` and `events` tables. see postgrestSetup.md
3. [ ] **Go Server:** Write the `main.go` using `net/http`.
4. [ ] **Endpoint:** Implement `POST /webhooks/ingest/:provider`.
5. [ ] **Signature Verification:** Implement HMAC check for Stripe & Plaid.
6. [ ] **Deploy:** Push to AWS/Kubernetes (or just a VPS for now). **Test 100 req/s.**

**Week 2: The "Vault" (NATS)**

1. **NATS Setup:** Spin up NATS JetStream container.
2. **Stream Config:** Create `EVENTS` stream (File storage, 1 year retention).
3. **Go Publisher:** Modify the Gate to publish valid webhooks to `events.raw`.
4. **Test:** Send a fake Stripe webhook. Verify it persists to disk even if you kill the server.

**Week 3: The "Brain" (Go Agent Runtime)**

1. **OpenAI Client:** Integrate `sashabaranov/go-openai`.
2. **State Machine:** Write the `AgentLoop` struct.
* Input: User Message string.
* Action: Call LLM.
* Output: Tool Call or Text.


3. **Redis:** Setup Redis for "Conversation History" storage.

**Week 4: The "Hands" (Python Sidecar)**

1. **Proto Definition:** Write `agent.proto`. Define `ExecuteSkill`.
2. **Python Worker:** Write the gRPC server in Python.
3. **Go Client:** Write the gRPC client in Go.
4. **Bridge Test:** Have Go send "Hello" -> Python reverses it -> Go prints "olleH".

---

### Phase 2: The "Chaser" Agent (Weeks 5-8)

*Goal: The first sellable feature (Client Communication).*

**Week 5: The Communication Layer**

1. **Twilio/WhatsApp:** Sign up. Get API Keys.
2. **Webhook Handler:** Update Go Gate to accept Twilio webhooks.
3. **Routing:** Map `From: +1234` -> `Tenant: CPA_Bob`.

**Week 6: The "Receipt" Skill (Python)**

1. **Skill Logic:** In Python, write a function that takes an Image URL.
2. **OCR:** Use a library (or GPT-4 Vision API) to extract Total, Date, Vendor.
3. **Response:** Return JSON `{"amount": 50.00, "vendor": "Shell"}`.

**Week 7: The "Nagging" Logic (Go)**

1. **Cron Job:** Implement a simple ticker in Go.
2. **Logic:** "If transaction in DB is `uncategorized` AND `age > 3 days` -> Trigger WhatsApp Message."
3. **LLM Prompt:** "You are a polite accountant. Ask for the receipt for this transaction: {transaction_details}."

**Week 8: The Frontend (Fignode MVP)**

1. **React Setup:** `create-toro-app`. Install Tailwind.
2. **Dashboard:** A simple list of "Conversations."
3. **Real-time:** Connect Frontend to Go via WebSocket. Show messages appearing as they happen.

---

### Phase 3: The "Bookkeeper" Agent (Weeks 9-12)

*Goal: Closing the loop (Data -> QuickBooks).*

**Week 9: The Plaid Connector**

1. **Link Token:** Create an endpoint to generate Plaid Link tokens.
2. **Sync:** Write a Go worker that polls Plaid `transactions/sync` every hour.
3. **Storage:** Save transactions to Postgres `ledger_entries`.

**Week 10: The QuickBooks MCP Server**

1. **Auth:** Implement OAuth2 for QuickBooks Online.
2. **Skill:** Write Python MCP tool: `create_expense(amount, vendor, category)`.
3. **Integration:** Wire it into the Python gRPC worker.

**Week 11: The "Auto-Categorize" Loop**

1. **Logic:** When a new Plaid transaction arrives -> Send to Go Brain.
2. **Brain:** Asks LLM "What category is 'Shell Oil 554'?"
3. **Action:** If confidence high -> Call `create_expense` (Python).
4. **Action:** If confidence low -> Mark as "Needs Review" in DB.

**Week 12: The "Human Review" UI**

1. **UI:** Create a "Triage" screen in Fignode.
2. **Function:** Show the AI's guess. Allow CPA to click "Approve" or "Edit."
3. **Learning:** When CPA edits, save the correction to fine-tune the prompt next time.

---

### Phase 4: Launch (Week 13)

*Goal: Get paid.*

**Week 13: Onboarding "Customer Zero"**

1. **Deploy:** Move from `localhost` to AWS EKS (Production).
2. **Walk-in:** Go to a local CPA (or a US contact).
3. **Install:** Connect their QuickBooks. Invite their first 5 clients to WhatsApp.
4. **Verify:** Watch the "Chaser" agent collect a receipt live.

**STOP.** Do not build Payroll. Do not build Lending.
Once you have **one CPA** using the "Chaser" and "Bookkeeper," you have a business.

**Start coding Week 1 tomorrow.**