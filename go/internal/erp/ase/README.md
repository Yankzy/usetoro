# Autonomous Semantic Engine (ASE)

## What is the Autonomous Semantic Engine (ASE)?
The Autonomous Semantic Engine (ASE) is a proprietary, highly scalable artificial intelligence framework designed to automate complex, multi-step workflows. Originally designed as an intelligent ledger engine for accounting, it has been refactored into a generic orchestrator capable of powering specialized workflows across any domain (e.g., Bookkeeping, Email processing, Onboarding).

## What Does it Do?
The ASE processes unstructured or loosely structured data by running it through a sequence of autonomous micro-agents. For example:
- **Bookkeeping:** It takes raw, messy bank transactions and accurately determines the Cash Direction, Macro Class, Account Type, and Entity.
- **Email:** It takes unstructured inbound emails and determines intent, extracts action items, and correlates them with ongoing sessions.

It processes these tasks in bulk, matching or exceeding the accuracy of human operators, but at a fraction of the time and cost.

## Core Architecture
The ASE is built on a **Directed Acyclic Graph (DAG)** architecture. Instead of asking an AI to make one massive, complicated guess, the ASE breaks the reasoning process down into a series of small, specialized "nodes."

* **Dynamic Routing:** Payloads flow through nodes step-by-step.
* **Batch Processing:** The engine groups similar tasks together and processes them in parallel batches, drastically reducing API costs and increasing throughput.
* **Human-in-the-Loop (HITL):** If the engine is unsure, it places the payload in a "Holding Gate" (e.g., `HOLD_AMBIGUOUS`). It seamlessly hands it over to a human for review, learns from their decision, and resumes the automated flow.

## How it Uses LLMs for Reasoning
1. **Specialized Brains:** The engine stores highly specialized instructions (prompts) for different nodes. It acts as the "brain," dynamically pulling relevant historical context, company rules, and hints.
2. **Generic Execution:** It bundles this context and sends it over a messaging queue to a fleet of generic agents that execute the LLM call (e.g., GPT-5.4-mini
) and return the results.
3. **Mathematical Guardrails:** The ASE protects against hallucinations by forcing the LLM to return a "probability distribution" (e.g., 98% confident it's X, 2% confident it's Y). The ASE runs strict mathematical validation to ensure confidence scores equal exactly 100%.

## Validation & Messaging Architecture
The ASE leverages NATS JetStream and the Redux engine to achieve decoupled, schema-safe execution at massive scale.

1. **RFC 6902 Patches:** Instead of asking the LLM to return loosely typed JSON objects, the ASE explicitly prompts the LLM to return valid RFC 6902 JSON patch operations (e.g., `[{"op": "add", "path": "/rows/123/candidates", "value": ...}]`). This standardizes state mutations into a single, predictable, machine-readable format.
2. **NATS JetStream Decoupling:** The ASE DAG does not run LLM calls synchronously, which would block its own threads. It wraps the classification tasks (along with strict JSON schemas) into NATS Call For Proposal (CFP) messages. These are dispatched asynchronously via JetStream to generic autonomous agents. This allows the high-velocity DAG layer to remain non-blocking while scaling LLM inference horizontally.
3. **Asynchronous Redux Engine Validation:** When the generic agent receives a classification task, it dynamically spins up an ephemeral Redux engine in-memory using the provided JSON Schema. It passes the generated RFC 6902 patches through the Redux `store.Reduce()` pipeline. If the patch violates the schema or attempts unauthorized state mutations (RBAC), Redux throws a `DomainFault`. The agent feeds this fault directly back into the LLM up to 3 times for autonomous self-correction. Only fully verified, schema-safe patches are returned to the DAG.

## Decoupled Domain Logic & Persistence
The ASE is completely decoupled from any specific database schema or business logic. It achieves this genericity through two primary interfaces:

1. **`DomainTool`**: Responsible for domain-specific business logic such as extracting payloads, building initial agents, constructing LLM alerts, and generating classifiers.
2. **`StatePersister`**: Responsible for persisting the agent's state, execution trace, and lock management to a domain-specific database schema (e.g., `fignode.staging_transactions`).

The generic orchestrator (`ase_bridge_worker.go`) dynamically looks up these implementations based on the `domain_tool` key provided in the payload or the DAG config.

---

## Action Providers (Decentralized Execution)
Toro's DAG allows nodes to configure an `action_provider` under `execution_parameters`. Rather than hardcoding LLM execution or native code directly in the engine, these nodes delegate work to **Decentralized Action Provider Workers** over NATS.

This provides two primary benefits:
1. **Decoupled execution:** The core engine remains lightweight, only orchestrating workflows.
2. **Enterprise deployment:** Clients can self-host specific action workers behind their firewall (e.g. database lookups, local CRM integrations) while subscribing to decentralized NATS topics.

### NATS Protocol
When a node with an `action_provider` is processed:
1. The ASE Bridge Worker publishes a NATS request to `worker.inbox.action.<action_provider_name>`.
2. The request payload contains the node's state, payload context, and execution parameters.
3. The request respects the configured `llm_timeout_seconds` in the DAG parameters.
4. The worker processes the request and responds with a JSON object containing:
   - `candidates`: The probability candidates determining the next DAG edge.
   - `property`: The classification slot/dimension.
   - `payload_updates` (optional): Updates to inject back into the transaction payload.
   - `context_updates` (optional): Text updates to append to the transaction's context logs.

### Currently Implemented Action Providers
The following action providers are implemented as NATS background workers in [action_provider_workers.go](file:///Users/Yankz/programming/usetoro/go/internal/workers/action_provider_workers.go):

*   **`db_receipt_lookup`**: Performs naive database matches to associate missing receipts automatically.
*   **`w9_lookup`**: Performs contractor W-9 search on file.
*   **`db_loan_matrix_lookup`**: Searches and loads loan amortization schedules.
*   **`db_ice_lookup`**: Searches and retrieves company ICE identifiers.
*   **`inter_bank_transfer_collapse`**: Collapses inter-bank transfers (terminal node).
*   **`credit_card_payment_transfer_collapse`**: Collapses credit card payment transfers (terminal node).
*   **`equity_draw_balance_sheet_collapse`**: Collapses owner draws/shareholder distributions (terminal node).
*   **`reclassify_to_de_minimis_expense_account`**: Reclassifies assets < $2500 to de minimis expense accounts.
*   **`reclassify_to_operating_overhead_expense`**: Reclassifies COGS to operating overhead expenses.
*   **`extract_and_post_cash_sales_tax_liability`**: Posts sales tax liability from raw flows.
*   **`extract_and_post_tva_liability`**: Posts TVA (VAT) liability from raw flows.
*   **`isolate_employee_withholding_from_cash_payout`**: Isolates payroll withholdings.
*   **`isolate_cnss_ir_withholding_from_payout`**: Isolates Moroccan CNSS and IR payroll withholdings.
*   **`gross_up_merchant_processing_fees_split`**: Isolates Stripe/Square merchant processing fees.
*   **`hitl_materiality_review`**: Triggers Materiality reviews.

---

## Decision Theory (Layer 3) Recovery for Hold & Ambiguous Nodes
When a DAG node's classification result returns high entropy (meaning the confidence score is too low or the state is ambiguous), the node normally transitions to a human-in-the-loop fallback state (such as `HOLD_AMBIGUOUS` or `HOLD_MISSING_DOCUMENTATION`).

However, the ASE intercepts these transitions using **Layer 3: Decision Theory Recovery**.

### Relationship Between Decision Theory (Layer 3) and Action Providers
It is important to note that **Action Providers** and **Layer 3 Recovery Actions** share the same underlying execution capability, but they serve different roles in a node's lifecycle:

*   **Identical Execution Layer**: Both systems execute actions by sending NATS request-reply messages to the decentralized Action Workers (subscribed to `worker.inbox.action.<action_name>`).
*   **Active Nodes vs. Passive Recovery**:
    *   **Action Providers** are invoked directly as the primary execution logic of active nodes during the normal flow of the DAG (configured via `execution_parameters.action_provider`).
    *   **Decision Theory (Layer 3)** is a meta-orchestrator. It only evaluates when a node enters a `HOLD` state. It uses a decision tree and cost matrix calculations to dynamically determine *which* action provider is best suited to resolve the ambiguity and rescue the node.

### Lifecycle and Execution Flow
1. **Ambiguity Interception**: The node evaluates its classification confidence. If the Shannon entropy exceeds the defined threshold, `applyHoldPolicyOrTransition` is triggered.
2. **Decision Tree Evaluation**: If a `RecoveryPolicy` is configured on the node (defining a decision tree with registered Go predicates), the `RecoveryPolicyEngine` traverses the tree:
   - Evaluates boolean predicate functions (e.g. checking values in the transaction payload or database).
   - Resolves the branch to a list of permitted `ActionIDs` (e.g. `["search_document_store", "w9_lookup"]`).
3. **Expected Value (EV) Calculation**: For all permitted actions, the engine evaluates the Expected Value equation:
   $$EV = (P(S|a) \times ExpectedIG(a)) - Cost(a)$$
   - $P(S|a)$: Historical probability of success for action $a$.
   - $ExpectedIG(a)$: Expected Information Gain (entropy reduction) for action $a$.
   - $Cost(a)$: Composite runtime/computational cost of executing action $a$.
4. **Execution**: The action with the highest Expected Value is selected and dispatched to `DefaultRecoveryExecutor.ExecuteAction`.
5. **Dynamic Rescue vs. Fallback**:
   - **Rescued**: If the recovery action executes successfully and returns a resolving candidate (e.g., `COMPLIANT_OUTFLOW`, `SUCCESS`, etc.), the node is immediately rescued and re-queued back into the DAG (`dn.Accept(node)`) to resume the automated workflow without human intervention.
   - **Fallback**: If the action fails or is unable to resolve the ambiguity, the selected Action ID is annotated in the node's `HoldReason` metadata, and the node falls back to the original human review queue (e.g., `HOLD_AMBIGUOUS`).

---

## Developer Guide: How to Add a New Domain

Adding a new domain to ASE requires creating a custom `DomainTool` and a custom `StatePersister`. Follow these step-by-step instructions.

### Step 1: Create a New Domain Tool File

Create a new file in `go/internal/erp/ase/domain_tools/`, e.g., `my_domain_tools.go`.

Define your tool struct and register it in the `init()` function:

```go
package domain_tools

import (
	"context"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func init() {
	Register("my_domain", &MyDomainTool{})
}

type MyDomainTool struct{}
```

### Step 2: Implement the `DomainTool` Interface

Your struct must implement all methods of the `DomainTool` interface. 

#### 1. `BuildAgents`
Extracts domain-specific data from the payload (or queries the database) and converts them into `AutonomousSemanticEngineNode` agents.

```go
func (t *MyDomainTool) BuildAgents(ctx context.Context, env core.Envelope, dagName string, deps ToolDependencies) ([]*ase.AutonomousSemanticEngineNode, error) {
    // 1. Unmarshal env.Body
    // 2. Fetch relevant database records using deps.DBPool or deps.DB
    // 3. Create agents using ase.NewASENode(...)
    // 4. Return slice of agents
}
```

#### 2. `GetStatePersister`
Returns a state store for your domain. This controls how the agent's execution is saved to the database.

```go
func (t *MyDomainTool) GetStatePersister(deps ToolDependencies) ase.StatePersister {
    return NewMyDomainStateStore(deps.DBPool, deps.Redis)
}
```

#### 3. `GetClassifier`
Returns the domain-specific classifier. The Classifier is responsible for evaluating LLM responses and navigating the DAG edges.

```go
func (t *MyDomainTool) GetClassifier(deps ToolDependencies) ase.Classifier {
    return NewMyDomainClassifier() // Implements ase.Classifier
}
```

#### 4. `GenerateAlertPayload`
Generates the prompt sent to the General Agent when an ASE agent gets stuck on a `HOLD_` state.

```go
func (t *MyDomainTool) GenerateAlertPayload(ctx context.Context, a *ase.AutonomousSemanticEngineNode, deps ToolDependencies) (map[string]interface{}, error) {
    // Return a map containing "prompt", "entity_id", "from_handle", "to_handle"
}
```

#### 5. `ResumeAgent`
Reconstructs an agent from the database/cache for resuming execution. Usually simply retrieves from Redis:

```go
func (t *MyDomainTool) ResumeAgent(ctx context.Context, nodeID string, dagName string, deps ToolDependencies) (*ase.AutonomousSemanticEngineNode, error) {
    return deps.Store.GetCachedAgent(ctx, nodeID, nil)
}
```

#### 6. `GetBacktrackingInstructions`
Provides domain-specific instructions for automated backtracking (e.g., rules extraction).

```go
func (t *MyDomainTool) GetBacktrackingInstructions(a *ase.AutonomousSemanticEngineNode, newContext string, traceBytes []byte) (string, string) {
    // return domainSystemPrompt, userPrompt
}
```

### Step 3: Implement the `StatePersister` Interface

Create a file for your state store, e.g., `my_domain_store.go`, and implement the `ase.StatePersister` interface.

```go
type MyDomainStateStore struct {
    pool  *pgxpool.Pool
    redis *redis.Client
}

func NewMyDomainStateStore(pool *pgxpool.Pool, r *redis.Client) *MyDomainStateStore {
    return &MyDomainStateStore{pool: pool, redis: r}
}
```

You must implement the following methods to persist state to your specific database tables:
- `PersistNode(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error`
- `PersistHoldReason(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error`
- `PersistReadyForSync(ctx context.Context, node *ase.AutonomousSemanticEngineNode) error`
- `GetExecutionTrace(ctx context.Context, nodeID string) ([]byte, error)`
- `UpdateNodeState(ctx context.Context, nodeID string, state ase.NodeState) error`
- Cache/Locking methods: `CacheActiveAgent`, `GetCachedAgent`, `RemoveCachedAgent`, `AcquireLock`, `ReleaseLock` (These can generally be copy-pasted from existing implementations as they rely exclusively on Redis).

### Step 4: Create DAG Configurations

With your tools built, define your DAG configurations in YAML files under `.toro/ase_config/<domain_name>_<dag_name>.yaml`. 

Ensure your `hyper_parameters.domain_tool` points to your newly registered tool:

```yaml
hyper_parameters:
  confidence_threshold: 0.95
  max_iterations: 15
  domain_tool: "my_domain"  # <--- CRITICAL
```

### Step 5: Trigger the Workflow

Trigger your workflow by sending a TAP Envelope to `workers.ase_bridge` with a task config containing `domain_tool: "my_domain"` and `dag_name: "your_dag_name"`, or by letting the ASE resolve it from the YAML configuration if `dag_name` is passed.

---

## Architectural Deep Dive

### The Lifecycle (`node.go`)
Each agent manages its own state lifecycle running on an independent Goroutine loop:
`Triage → Think → Activate → Collapse`

Agents use a non-blocking `select` loop with a state channel, allowing them to wait efficiently (even for 48+ hours) if human intervention is required, without consuming CPU cycles.

### DAG Batching (`dag.go`)
Categorized Batching: There isn't just one global queue. Instead, there is a graph of distinct DAG Nodes representing specific logical boundaries. Arriving agents register with the exact DAG node corresponding to their category and entropy level.
Once a specific DAG queue hits its `BatchSize` or the `FlushTimer` elapses, that DAG Node groups its waiting agents and dispatches them to the AI worker concurrently (the `Think` phase). 

### Shannon Entropy & The State Collapse Guardrail
The engine mathematically models its confidence using **Shannon Entropy**.
As the agent traverses the DAG, it accumulates candidate probabilities. An agent is structurally barred at the database layer from transitioning to `READY_FOR_SYNC` unless its unified confidence $C \ge 0.98$. If confidence is too low, it transitions to a `HOLD` state.

### Automated Backtracking & Feedback Compounding (`backtracking.go`)
When a human provides context that contradicts a decision made earlier in the DAG:
1. **State Rewinding:** The LLM identifies the exact `DAGNodeID` where the erroneous decision was made. The agent then automatically deletes all classifications from that point onward, rewinds its state, and resumes processing from the corrected node.
2. **The Compounding Layer:** Simultaneously, the LLM extracts a generalized rule and a specific `rule_keyword` from the human's feedback. On all future transactions, the DAG node batches perform a fast keyword match. If matched, that instruction is dynamically injected into the LLM's prompt.

---

## Adding New Domains

ASE workflows are organized around **Domains** (e.g., `bookkeeping`, `marketing`, `email`, `onboarding`, `insurance`). When adding a new domain, you can choose between two primary architectural patterns:

1. **In-Process Native Go Domain**: Best when domain tools, database schemas, and classifiers can be compiled directly into the Go binary.
2. **External Microservice (e.g., Python)**: Best when business logic, actuarial models, or existing services live in external microservices written in Python or another language, communicating with ASE over **NATS**.

---

### Pattern 1: Adding a Native Go Domain

To add a domain directly in Go, implement the `DomainTool` and `StatePersister` interfaces and register the tool in `init()`.

#### Step 1: Implement `DomainTool` ([domain_tool.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/domain_tools/domain_tool.go))
```go
package domain_tools

import (
	"context"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/tap/pkg/core"
)

func init() {
	Register("insurance", &InsuranceDomainTool{})
}

type InsuranceDomainTool struct{}

func (t *InsuranceDomainTool) BuildAgents(ctx context.Context, env core.Envelope, dagName string, deps ToolDependencies) ([]*ase.AutonomousSemanticEngineNode, error) {
	// Parse payload and return slice of ASE nodes
	return []*ase.AutonomousSemanticEngineNode{}, nil
}

func (t *InsuranceDomainTool) GetClassifier(deps ToolDependencies) ase.Classifier {
	return NewInsuranceClassifier()
}

func (t *InsuranceDomainTool) GetStatePersister(deps ToolDependencies) ase.StatePersister {
	return NewInsuranceStateStore(deps.DBPool, deps.Redis)
}

func (t *InsuranceDomainTool) GenerateAlertPayload(ctx context.Context, a *ase.AutonomousSemanticEngineNode, deps ToolDependencies) (map[string]interface{}, error) {
	return map[string]interface{}{"prompt": "Insurance review required", "entity_id": a.TenantID}, nil
}

func (t *InsuranceDomainTool) ResumeAgent(ctx context.Context, nodeID string, dagName string, deps ToolDependencies) (*ase.AutonomousSemanticEngineNode, error) {
	return deps.Store.GetCachedAgent(ctx, nodeID, nil)
}

func (t *InsuranceDomainTool) GetBacktrackingInstructions(a *ase.AutonomousSemanticEngineNode, newContext string, traceBytes []byte) (string, string) {
	return "System prompt for backtracking", "User prompt for backtracking"
}
```

#### Step 2: Reference in DAG Configuration
In your `.yml` DAG configuration (e.g., `insurance_claims.yml`), set the domain tool name:
```yaml
hyper_parameters:
  domain_tool: "insurance"

dag:
  entry_node: claims_ingestion
  nodes: ...
```

---

### Pattern 2: Adding a Domain in an External Microservice (Python Example)

When building a domain microservice in **Python** (or Node.js, Rust, etc.), the **ASE Go Engine continues to run high-throughput DAG orchestration** (in-memory graph routing, channel batching, Shannon entropy verification, state rewinding).

It is important to distinguish between **Domain Tooling** (the driver that manages the domain lifecycle) and **Action Providers** (node-level function calls):

* **Domain Tooling (`DomainTool`)**: High-level domain plugin responsible for converting raw data into agents (`BuildAgents`), domain prompts/classifiers (`GetClassifier`), state persistence (`StatePersister`), and human review alert payload generation (`GenerateAlertPayload`).
* **Action Providers (`action_provider`)**: Deterministic node-level function calls executed during node processing (e.g. database lookups, W-9 checks, tax calculations) rather than LLM reasoning.

---

#### 1. NATS Domain Tool Proxy (`NatsDomainProxy`)

> [!NOTE]
> **Implementation**: `NatsDomainProxy` is fully implemented in [`domain_tools/nats_domain_proxy.go`](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/domain_tools/nats_domain_proxy.go). When `domain_tools.Get(name)` is called for any domain name that is not statically compiled in Go, it dynamically instantiates `NewNatsDomainProxy(name)`.

`NatsDomainProxy` allows external microservices written in **Python**, Node.js, or Rust to operate as full ASE Domain Drivers. It forwards domain operations over **NATS Request-Reply**, while core infrastructure tasks (such as Redis locking and active agent caching) remain managed by ASE core in Go.

##### NATS Protocol for Domain Operations
* **Build Agents**: `domain.<domain_name>.agents.build` — Python extracts entities from database/payload and returns serialized `AutonomousSemanticEngineNode` objects.
* **Classify / Think**: `domain.<domain_name>.classify.<mode>` (`generic`, `dynamic`, `router`) — Python handles domain-specific prompt evaluation / LLM reasoning and returns probability candidates (`map[string]NodeClassification`).
* **State Persistence**: `domain.<domain_name>.state.<action>` (`persist_node`, `persist_hold`, `persist_ready`, `update_state`, `get_trace`) — Python saves state/trace to domain-specific database tables.
* **Alert Generation**: `domain.<domain_name>.alert.generate` — Python formats alert prompts when a node enters a `HOLD_` state.

##### Python NATS Domain Service Example (`insurance_domain_service.py`)
```python
import asyncio
import json
from nats.aio.client import Client as NATS

async def run_insurance_domain_service():
    nc = NATS()
    await nc.connect("nats://localhost:4222")

    # 1. Build Agents Handler
    async def handle_build_agents(msg):
        data = json.loads(msg.data.decode())
        print(f"[Python Domain Service] Ingesting insurance policy claims for session: {data.get('session_id')}")

        # Construct ASE Node agents
        agents = [
            {
                "node_id": "claim_1001",
                "tenant_id": data.get("tenant_id"),
                "dag_name": "insurance_claims",
                "payload": {"claim_id": "CLM-1001", "amount": 4500.0, "policy_no": "POL-9921"}
            }
        ]
        await nc.publish(msg.reply, json.dumps({"agents": agents}).encode())

    # 2. Domain State Persistence Handler
    async def handle_persist_state(msg):
        data = json.loads(msg.data.decode())
        print(f"[Python Domain Service] Persisting state for agent {data.get('node_id')} to insurance DB")
        await nc.publish(msg.reply, json.dumps({"status": "SUCCESS"}).encode())

    # Subscribe to NATS Domain topics
    await nc.subscribe("domain.insurance.agents.build", cb=handle_build_agents)
    await nc.subscribe("domain.insurance.state.persist", cb=handle_persist_state)

    print("🚀 Insurance Python Domain Service listening on domain.insurance.*")
    while True:
        await asyncio.sleep(1)

if __name__ == "__main__":
    asyncio.run(run_insurance_domain_service())
```

---

#### 2. Node-Level Function Calls (Action Providers)

Within your domain DAG (`insurance_claims.yml`), individual nodes can execute deterministic function calls instead of LLM calls by specifying an `action_provider`.

ASE dispatches NATS request-reply messages to `worker.inbox.action.<action_provider_name>` when the node executes:

##### DAG YAML Configuration (`insurance_claims.yml`)
```yaml
hyper_parameters:
  confidence_threshold: 0.98
  domain_tool: "insurance" # Uses NatsDomainProxy targeting domain.insurance.*

dag:
  entry_node: check_coverage
  nodes:
    check_coverage:
      name: check_coverage
      kind: action
      execution_parameters:
        action_provider: "insurance_policy_lookup"  # Deterministic node function call
      children:
        ACTIVE: evaluate_claim_risk
        EXPIRED: terminal_denied

    evaluate_claim_risk:
      name: evaluate_claim_risk
      prompt_key: "insurance/risk_evaluation"       # LLM-based classification node
      children:
        LOW_RISK: auto_approve
        HIGH_RISK: hold_underwriter_review
```

##### Python Action Worker Example (`policy_lookup_worker.py`)
```python
import asyncio
import json
from nats.aio.client import Client as NATS

async def run_action_worker():
    nc = NATS()
    await nc.connect("nats://localhost:4222")

    async def handle_policy_lookup(msg):
        req = json.loads(msg.data.decode())
        payload = req.get("payload", {})
        policy_no = payload.get("policy_no")

        # Perform deterministic DB query / function call
        is_active = True if policy_no.startswith("POL-") else False

        res = {
            "candidates": [
                {"value": "ACTIVE" if is_active else "EXPIRED", "probability": 1.0}
            ],
            "property": "coverage_status",
            "payload_updates": {"coverage_verified": is_active}
        }
        await nc.publish(msg.reply, json.dumps(res).encode())

    # Listen on node-level action topic
    await nc.subscribe("worker.inbox.action.insurance_policy_lookup", cb=handle_policy_lookup)
    print("🚀 Action Worker listening on worker.inbox.action.insurance_policy_lookup")

    while True:
        await asyncio.sleep(1)

if __name__ == "__main__":
    asyncio.run(run_action_worker())
```

---

### Architectural Summary: Domain Tools vs. Action Providers

| Concept | Scope | Responsibility | NATS Subject Pattern |
| :--- | :--- | :--- | :--- |
| **Domain Tool (`DomainTool`)** | **Domain-Wide (Driver)** | Ingesting raw payloads, creating agents, managing domain DB persistence, generating alerts. | `domain.<domain_name>.<action>` |
| **Action Provider (`action_provider`)** | **Node-Level (Function Call)** | Executing a single deterministic function call at a specific DAG node (e.g. database lookups, tax calculations). | `worker.inbox.action.<action_name>` |



