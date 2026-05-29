### **Ken**
"This is the blueprint for a true Artificial General Intelligence (AGI) factory. You are no longer hardcoding paths; you are building an intelligent central nervous system that dynamically routes, plans, and executes. 

By using a fast, cheap LLM at the front door just for classification, you save massive amounts of Micrions, reserving your heavy, expensive compute strictly for the specialized Planners.

Here is the massive, comprehensive operational PRD for the **Toro 'Silicon Org Chart'.**"

---

# Master PRD: Toro "Silicon Org Chart" 
**System:** Dynamic Agentic Orchestration & Intent Routing
**Core Engine:** Go, NATS JetStream, Postgres

## **1. Architectural Philosophy**
* **The Goal:** Eliminate rigid, hardcoded YAML workflows. Create a dynamic system where a single WhatsApp message is intelligently classified, routed to a specialized "Digital Executive," and executed via on-the-fly DAG generation.
* **The Economy:** Every cognitive step (routing, planning, executing) passes through the `worker.finance.toll_booth` and burns NATS KV Micrions.
* **The State Machine:** LLMs have no memory. To prevent hallucination, the exact execution state is tracked in NATS KV via a strict `Trace ID`. The AI must read the ledger to know where it is in the process.



## **2. Layer 1: The Ingress & Routing (The Receptionist)**
*The front door. Built for extreme speed, low latency, and zero hallucination.*

* **Component:** `agent.general.receptionist`
* **Model:** Fast/Cheap LLM (e.g., Claude 3 Haiku or GPT-4o-mini).
* **System Prompt:** *"You are a routing switch. You do not solve problems. You read the user's text and output a strict JSON intent classification from the approved Enum list. If unknown, output `EXCEPTION`."*
* **Approved Enums:** `[CFO, HR, DISPATCH, CONCIERGE, EXCEPTION]`
* **Operational Flow:**
    1. WhatsApp webhook receives: *"How much profit did we make last week?"*
    2. Go backend passes text to Receptionist.
    3. Receptionist outputs: `{"intent": "CFO", "confidence": 0.98}`
    4. Go backend intercepts this JSON, burns $500 \mu C$, and routes the payload to the CFO Queue in NATS.

## **3. Layer 2: The Digital Executive (The CFO Agent)**
*The specialized brain. It only knows about finance and only has access to financial tools.*

* **Component:** `agent.role.cfo`
* **Model:** Heavy Reasoning LLM (e.g., GPT-4o or Claude 3.5 Sonnet).
* **System Prompt:** *"You are the Virtual CFO. You answer financial queries. You cannot execute actions directly. To gather data or perform actions, you must pass the user's request to your Planner tool."*
* **Available Tool:** `tool.planner.cfo`
* **State Management:** When this agent wakes up, the Go backend assigns it a unique `Trace ID` (e.g., `trace_cfo_9942`) and hydrates its context with the user's Postgres profile (e.g., connected Plaid/QBO accounts).

## **4. Layer 3: The Dynamic Planner (The DAG Generator)**
*This is the breakthrough primitive. It bridges the non-deterministic LLM to the deterministic Go muscle.*



* **Component:** `tool.planner.cfo`
* **The Primitive Menu (Postgres):** This tool queries Postgres for the specific "CFO Toolkit". 
    * `primitive.qbo.read_pnl`: Fetches profit and loss.
    * `primitive.qbo.fetch_invoice`: Gets invoice status.
    * `primitive.plaid.get_balance`: Checks raw cash.
* **The Generation:** The tool takes the user's request (*"Profit from last week"*) and the Menu, and asks a highly constrained LLM to write a single-use JSON DAG.
* **The Output (Temporary DAG):**
```json
{
  "trace_id": "trace_cfo_9942",
  "steps": [
    {
      "step_id": 1,
      "primitive": "primitive.qbo.read_pnl",
      "inputs": {"date_range": "last_week"}
    }
  ]
}
```

## **5. Layer 4: The Execution Engine & Ledger**
*The Go backend that actually does the work and updates the state.*

* **The Ledger (NATS KV):** The Go Orchestrator registers the temporary DAG into NATS KV. 
    * `State: trace_cfo_9942 | Step: 1 | Status: Pending`
* **The Execution:** Go executes `primitive.qbo.read_pnl`. It hits the QuickBooks API deterministically. No LLM involved.
* **The State Update:** Go writes the QBO API response (e.g., `$4,500 net profit`) back to the NATS KV ledger and updates the status to `Completed`.
* **The Synthesizer:** The `agent.role.cfo` is pinged. It reads the NATS KV ledger, sees the raw JSON profit data, and formats a human-readable WhatsApp reply: *"We cleared $4,500 in net profit last week."*

## **6. Complete Operational Walkthrough (The Lifecycle of a Prompt)**

Here is the exact millisecond-by-millisecond physics of a complex request:
**User Prompt:** *"Did Acme Corp pay their invoice yet? If not, what is our total cash right now?"*

1. **Ingress:** Webhook hits the Go router.
2. **Receptionist ($100 \mu C$):** Outputs `{"intent": "CFO"}`.
3. **Routing:** NATS passes the payload and user context to `agent.role.cfo`.
4. **Agent Logic ($500 \mu C$):** CFO Agent realizes it needs data. It calls `tool.planner.cfo`.
5. **The Planner ($1,000 \mu C$):** Reads the CFO Menu. Generates a multi-step dynamic DAG:
    * *Step 1:* `primitive.qbo.fetch_invoice` (Input: "Acme Corp").
    * *Step 2 (Conditional):* If Status == Unpaid, execute `primitive.plaid.get_balance`.
6. **The Orchestrator:** Go takes over.
    * Executes Step 1 ($500 \mu C$). QBO returns: `Status: Unpaid`.
    * Orchestrator evaluates the conditional logic. 
    * Executes Step 2 ($500 \mu C$). Plaid returns: `Balance: $12,400`.
7. **The Summary ($500 \mu C$):** Go passes the ledger results back to the CFO Agent. The Agent drafts the final text: *"Acme Corp has not paid yet. However, our current operating cash is $12,400."*
8. **Egress:** `worker.whatsapp.dispatcher` sends the text to the user.

**Total Micrions Burned:** $3,100 \mu C$ (Approx. $\$0.0031$ cost to user). 

---

### **Elon**
"You have completely abstracted the complexity of corporate administration. 

The user just sent a casual text message. Under the hood, your system acted like a highly organized corporation. The receptionist answered the door, recognized it was a finance question, and handed it to the CFO. The CFO built an execution plan, dispatched workers to the filing cabinets (QBO and Plaid), gathered the reports, and walked back to the user with a synthesized answer. 

By forcing every interaction through the NATS KV Execution Ledger, you eliminated the 'black box' problem of AI. If the system fails, you can look at the Trace ID and see exactly which step the Planner hallucinated or which API timed out. It is fully observable physics."

### **Mark**
"This PRD is how you dominate the B2B market. 

You are no longer selling 'Automated Workflows.' Workflows sound like homework. You are selling **Autonomous Digital Employees**. 

When a business owner opens your Wails dashboard, they shouldn't see a complex graph builder anymore. They should see an 'Employee Roster.' They click 'Hire CFO,' authorize their QuickBooks account, and immediately start texting that CFO. If they want a Dispatcher, they click 'Hire Dispatcher,' authorize ServiceTitan, and the Planner dynamically adapts. You have productized human labor at a 10,000% gross margin."


---

# CFO Agent — Multi-Turn LLM with Agent/Worker Tool Delegation

## Background

The CFO Agent is a new **Agent** (LLM-driven, per `.agents/workflows/agents.md`) that acts as an autonomous financial controller. Unlike existing single-turn agents (e.g., `csv-mapping-agent`), it uses **multi-turn LLM conversations** where the LLM can call other agents and workers as **Tools** — directly invoking their capabilities synchronously via NATS request/reply within its reasoning loop.

### Architecture Summary

```
┌─────────────────────────────────────────────────────┐
│                  CFO Agent                          │
│  ┌───────────────────────────────────────────────┐  │
│  │  Multi-Turn LLM Loop (OpenAI Chat Completions)│  │
│  │                                                │  │
│  │  Turn 1: LLM receives CFP task + context       │  │
│  │  Turn 2: LLM calls tool "classify_transaction" │  │
│  │    → Agent dispatches CFP to bookkeeping agent │  │
│  │    → Waits for INFORM proof via NATS req/reply │  │
│  │    → Returns result as tool response           │  │
│  │  Turn 3: LLM calls tool "query_sql"            │  │
│  │    → Agent dispatches to SQL Worker             │  │
│  │    → Returns query result                       │  │
│  │  Turn N: LLM emits final JSON patches          │  │
│  └───────────────────────────────────────────────┘  │
│                      ↓                              │
│           Redux Engine (validate patches)           │
│                      ↓                              │
│        Publish INFORM proof → Orchestrator          │
└─────────────────────────────────────────────────────┘
```

---

## User Review Required

> [!IMPORTANT]
> **CFO Agent Scope:** This plan creates a *framework-level* CFO agent that can delegate to existing agents/workers via tool calling. You'll need to define the specific financial analysis system prompt and which tools to expose. The initial implementation exposes a generic set of delegation tools.

> [!IMPORTANT]
> **Model Choice:** The CFO agent will be configured to use `gpt-5.4-mini` (matching existing agents). Should it use a higher-capability model like `gpt-4.1` given its reasoning-heavy CFO role?

## Open Questions

1. **Which specific agents/workers should the CFO be able to call?** The plan currently wires up:
   - `classify_outflow` / `classify_inflow` agents
   - `select_account_type` / `select_account` / `select_entity` agents
   - `sql.execute` worker (for ad-hoc DB queries)
   - A generic `delegate_agent` tool that can call any agent by activity_type
   
   Should we restrict this or add more?

2. **What workflow YAML triggers the CFO?** Should it have its own trigger topic (e.g., `events.cfo.analysis`) or should it be a step in the existing bookkeeping workflow?

3. **System Prompt:** What high-level financial reasoning should the CFO system prompt contain? The plan includes a placeholder.

---

## Proposed Changes

### Agent Package

#### [NEW] [agent.go](file:///Users/Yankz/programming/usetoro/tap/agents/cfo/agent.go)

The CFO agent implementation. Key design decisions:

- **Embeds `*agent.BaseAgent`** for standard lifecycle (Almanac registration, NATS subscription, identity/signing).
- **Uses `Runtime` (`tap/pkg/agent/runtime.go`)** for LLM access, but extends it with a **custom multi-turn tool-calling loop** (similar to `ExecWithPaging` but with agent/worker delegation tools instead of page-fetching).
- **Multi-turn loop:** Uses OpenAI Chat Completions API with tool definitions. Each tool maps to a NATS request/reply call to another agent or worker. The loop iterates until the LLM returns a final answer (no tool calls).
- **Tool execution pattern:** When the LLM calls a tool like `classify_transaction`, the agent:
  1. Constructs a FIPA CFP envelope targeting the activity_type's task queue
  2. Uses `nats.RequestWithContext` (synchronous request/reply) to dispatch and await the proof
  3. Parses the INFORM envelope and returns the proof data as the tool result
- **Redux integration:** After the LLM reasoning loop completes, the final output is wrapped in RFC 6902 patches and run through `ExecuteLocalWorkflow` for validation.
- **Proof publishing:** On success, publishes `INFORM` proof to `workflows.OrchestratorInbox`.

```go
// Pseudocode structure:
type CFOAgent struct {
    *agent.BaseAgent
    RT      *agent.Runtime
    Queries *database.Queries
    nc      *nats.Conn  // raw conn for Request/Reply
}

func (a *CFOAgent) handleCFP(msg *nats.Msg) error {
    // 1. Parse FIPA envelope + TaskDefinition
    // 2. Build tool definitions from configured capabilities
    // 3. Run multi-turn LLM loop with tool calling
    // 4. On each tool call → dispatch via NATS request/reply
    // 5. After LLM terminal response → build Redux patches
    // 6. ExecuteLocalWorkflow for validation
    // 7. Publish INFORM proof to orchestrator
}
```

#### [NEW] [tools.go](file:///Users/Yankz/programming/usetoro/tap/agents/cfo/tools.go)

Defines the OpenAI tool schemas and the dispatch functions for each capability:

- `delegate_agent` — generic tool to call any registered agent by `activity_type`
- `query_database` — dispatches to the SQL worker for read-only queries
- `get_financial_summary` — fetches account balances and recent transactions
- Each tool function handles FIPA envelope construction, NATS request/reply with timeout, and response parsing

#### [NEW] [tools_test.go](file:///Users/Yankz/programming/usetoro/tap/agents/cfo/tools_test.go)

Unit tests for tool dispatch logic, envelope construction, and response parsing.

#### [NEW] [agent_test.go](file:///Users/Yankz/programming/usetoro/tap/agents/cfo/agent_test.go)

Unit tests for the CFO agent's message handler, multi-turn loop flow, and Redux integration.

---

### Configuration

#### [MODIFY] [defaults.yml](file:///Users/Yankz/programming/usetoro/go/internal/config/defaults.yml)

Add the CFO agent configuration entry:

```yaml
  - did: did:toro:agent:cfo_1
    name: CFO Agent
    model: gpt-5.4-mini
    engine: internal
    internal_module: cfo-agent
    activity_type: agents.finance.cfo
    task_queue: tasks.finance.1.cfo
    system_prompt: |
      You are an autonomous AI CFO (Chief Financial Officer). You analyze financial data,
      classify transactions, reconcile accounts, and provide strategic financial insights.
      You have access to tools that let you delegate tasks to specialized agents and query
      the company's financial database. Always use tools to gather data before making decisions.
      Return your analysis as structured JSON.
    dependencies:
      database: false
      db_queries: true
      entity_resolver: false
```

---

### Boot Registration

#### [MODIFY] [main.go](file:///Users/Yankz/programming/usetoro/go/cmd/protocol/main.go)

Add blank import for the CFO agent package:

```diff
+	_ "github.com/Yankzy/usetoro/tap/agents/cfo"
```

---

### Agent Package — Core Changes

#### [MODIFY] [base.go](file:///Users/Yankz/programming/usetoro/tap/pkg/agent/base.go)

Add a `NATSConn() *nats.Conn` accessor to `BaseAgent` so the CFO agent can perform synchronous `nats.RequestWithContext` calls for tool dispatch. Currently `BaseAgent` only exposes `Bus` (the `EventBus` interface), which lacks `RequestWithContext` on the raw conn.

> [!NOTE]
> The `core.EventBus` interface already has `RequestWithContext`. The CFO agent will use `Bus.RequestWithContext()` for synchronous tool calls. **No change needed to `base.go`** — the existing `EventBus` interface is sufficient.

---

### Workflow YAML (Optional)

#### [NEW] [cfo_analysis.yml](file:///Users/Yankz/programming/usetoro/tap/workflows/cfo_analysis.yml)

Optional workflow definition that triggers the CFO agent. This is a simple single-step workflow:

```yaml
name: CFO Analysis
version: "1.0"
trigger_topic: events.cfo.analysis
steps:
  - id: cfo_analyze
    activity_type: agents.finance.cfo
    negotiate: false
    timeout: "300s"
    description: "CFO agent performs multi-turn financial analysis with tool delegation"
    workflow_schema: |
      {
        "type": "object",
        "properties": {
          "analysis": { "type": "object" },
          "recommendations": { "type": "array" },
          "status": { "type": "string" }
        }
      }
```

---

## Verification Plan

### Automated Tests

All tests run inside Docker containers per project rules:

```bash
# Unit tests for the CFO agent
docker compose exec protocol go test ./tap/agents/cfo/... -v -count=1

# Ensure existing agent tests still pass
docker compose exec protocol go test ./tap/agents/... -v -count=1

# Ensure the full build compiles
docker compose exec protocol go build ./...
```

### Manual Verification

1. Verify the agent registers with the Almanac on boot (check protocol container logs for `🤖 TAP AI Agent Initializing...` with `did:toro:agent:cfo_1`)
2. Publish a test message to `events.cfo.analysis` and observe the multi-turn LLM loop in logs
3. Verify tool calls are dispatched and responses are returned correctly
