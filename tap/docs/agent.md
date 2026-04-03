# TAP Agent Primitive

The TAP (Toro Agent Protocol) SDK provides a standardized framework for building, deploying, and orchestrating autonomous AI agents. This document breaks down the core concepts, lifecycle, and usage patterns of an Agent within the ecosystem.

## 1. What is a TAP Agent?
At its core, a TAP Agent is an autonomous microservice equipped with a decentralized identifier (DID), cryptographic keypairs, and the ability to parse/generate natural language via an LLM. 

Agents communicate asynchronously over NATS JetStream, passing signed, structured messages called `core.Envelope`s. These envelopes use **FIPA-compliant performatives** (e.g., `CFP` for Call For Proposal, `PROPOSE`, `ACCEPT`, `INFORM`) to negotiate contracts and exchange proofs of work.

## 2. The Agent Interface (Lifecycle)
All agents within the TAP runner ecosystem must conform to the strictly defined `agent.Runnable` interface:

```go
type Runnable interface {
    Start() error
    Stop() error
}
```

This interface ensures that the deployment runner (the **Supervisor**) can safely manage the heartbeat, health checks, and graceful shutdowns of hundreds of concurrent agents regardless of their internal complexities.

## 3. Communication Patterns
Agents receive work through two primary NATS subject architectures:

1. **Domain Queues (Pub/Sub):**
   Agents subscribe to public functional domains (e.g., `tasks.accounting.cleanup.>`). Multiple identical agents can join a single NATS QueueGroup. When a system broadcasts a `CFP` (Call for Proposal) to the domain, the NATS broker load-balances the task to a single available agent, scaling horizontally.
2. **Direct Inboxes:**
   Every agent provisions a personal listening topic: `core.BuildAgentInbox(did)`. If an agent needs to reply directly to another agent (e.g., sending an `ACCEPT_PROPOSAL`), it publishes directly to the recipient's inbox.

## 4. The Almanac Registry
To participate in the ecosystem, an agent **must** register itself immediately upon `Start()`. The **Almanac** serves as the dynamic DNS and capabilities registry for the hive.

An agent publishes its DID, routing inbox, operational capabilities (e.g., `accounting.cleanup`), and a TTL to the `almanac.register` stream. This allows the Hive and other agents to discover collaborators dynamically without hardcoded routing.

## 5. Instantiation and Execution

The TAP framework supports two distinct modalities of agents operating under the same Supervisor:

### A. Declarative Pipeline Agents (`agent.Runtime`)
For simpler workflows, agents can be instantiated dynamically from a `defaults.yaml` configuration using the generic `agent.Runtime`. 

```yaml
agents:
  - did: "did:toro:agent:chaser_1"
    name: "Invoice Chaser"
    engine: "pipeline"
    model: "gpt-5.4"
    system_prompt: "You are an invoice collection assistant..."
```

The runtime automatically subscribes to the parsed topics. Whenever a message arrives, the runtime builds the context and triggers an LLM completion loop via the standard `openai.NewClient().Responses.New(...)` API wrapper built into `rt.Exec()`.

### B. Compiled Internal Modules (Tutorial)
For highly complex operations requiring deterministic logic, semantic vector lookups, or strict database transactions (like the Enrichment Agent), developers build custom compiled Go structs.

Follow these four exact steps to build and deploy a native compiled agent:

#### Step 1: Implement `agent.Runnable`
Create a struct in `tap/agents/{your_agent_name}/agent.go`. It must store the base properties (Logger, EventBus, Config) alongside any of your custom Fignode dependencies (e.g., Fignode Postgres DB, Pinecone vector stores). It must implement `Start()` and `Stop()`.

```go
package myagent

import (
	"context"
	"log/slog"
	
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/Yankzy/usetoro/internal/database"
)

type MyAgent struct {
	logger *slog.Logger
	bus    agent.EventBus
	cfg    agent.AgentConfig
	db     *database.Queries
}

// Ensure the constructor returns the agent.Runnable interface
func NewAgent(l *slog.Logger, b agent.EventBus, c agent.AgentConfig, db *database.Queries) agent.Runnable {
	return &MyAgent{logger: l, bus: b, cfg: c, db: db}
}

func (a *MyAgent) Stop() error { return nil }
```

#### Step 2: Register in the Almanac & Subscribe in `Start()`
Inside your `Start()` method, you *must* announce your presence to the Hive via the Almanac registry. Then, bind your functional queues.

```go
func (a *MyAgent) Start() error {
	inbox := core.BuildAgentInbox(a.cfg.DID)
	
	// 1. Announce to Almanac
	regBytes, _ := json.Marshal(map[string]any{
		"did": a.cfg.DID,
		"endpoints": []string{inbox},
		"capabilities": []map[string]any{{"type": "accounting.custom_task"}},
	})
	a.bus.Publish("almanac.register", regBytes)

	// 2. Subscribe to your public work queue
	a.bus.QueueSubscribe("tasks.accounting.custom_task.>", "myagent-group", func(msg *nats.Msg) {
		a.logger.Info("Received task!")
	})
	
	return nil
}
```

#### Step 3: Register the Factory Closure in the Runner
In `go/cmd/agents-runner/main.go`, the Supervisor controls everything. You must securely inject your custom Fignode dependencies into the generic Supervisor factory signature:

```go
// Create your custom dependency
pool, _ := pgxpool.New(ctx, cfg.DatabaseURL)
db := database.New(pool)

// Bind your package string ("my-custom-agent") to the factory registry
supervisor.RegisterInternalAgent("my-custom-agent", func(l *slog.Logger, b agent.EventBus, c agent.AgentConfig, m agent.MemoryStore) agent.Runnable {
    // Inject the DB alongside the standard agent properties!
    return myagent.NewAgent(l, b, c, db)
})
```

#### Step 4: Configure `defaults.yaml`
Finally, define the agent in your `defaults.yaml` under `agents:`. The Supervisor will automatically lookup the `internal_module` string and spawn your struct dynamically.

```yaml
agents:
  - did: "did:toro:agent:custom_1"
    name: "My Custom Worker"
    engine: "internal"           # Tells Supervisor this is compiled Go code
    internal_module: "my-custom-agent" # Looks up the factory we registered in Step 3
```

## 6. Summary of an Agent's Workflow
1. **Boot**: The Supervisor invokes `agent.Start()`.
2. **Register**: The agent generates its DID/Keys and broadcasts its presence to the Almanac.
3. **Listen**: The agent binds its queues to NATS (Domain Topics and Direct Inbox).
4. **Negotiate**: It receives a `CFP`, uses tools/LLM to evaluate complexity, and sends a `PROPOSE`.
5. **Execute Context**: Upon `ACCEPT`, it utilizes its built-in `rt.Exec()` wrapper to process raw textual inputs against its logic.
6. **Submit**: It cryptographically signs its resulting data into a `core.Proof` and `INFORM`s the requester or Hive.
7. **Shutdown**: The Supervisor issues `agent.Stop()` for memory-safe teardowns draining NATS subscriptions.
