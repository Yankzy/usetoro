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
2. **Generic Execution:** It bundles this context and sends it over a messaging queue to a fleet of generic agents that execute the LLM call (e.g., GPT-4o) and return the results.
3. **Mathematical Guardrails:** The ASE protects against hallucinations by forcing the LLM to return a "probability distribution" (e.g., 98% confident it's X, 2% confident it's Y). The ASE runs strict mathematical validation to ensure confidence scores equal exactly 100%.

## Decoupled Domain Logic & Persistence
The ASE is completely decoupled from any specific database schema or business logic. It achieves this genericity through two primary interfaces:

1. **`DomainTool`**: Responsible for domain-specific business logic such as extracting payloads, building initial agents, constructing LLM alerts, and generating classifiers.
2. **`StatePersister`**: Responsible for persisting the agent's state, execution trace, and lock management to a domain-specific database schema (e.g., `fignode.staging_transactions`).

The generic orchestrator (`ase_bridge_worker.go`) dynamically looks up these implementations based on the `domain_tool` key provided in the payload or the DAG config.

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
