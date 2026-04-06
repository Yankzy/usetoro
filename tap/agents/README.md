# Toro Agent Plugin Registry (`tap/agents`)

The `agents` package is the **plugin registry** for all compiled internal AI agents in the Toro ecosystem. It decouples business logic from the core framework — adding a new agent never requires touching the daemon, supervisor, or any other infrastructure file.

---

## How It Works

Agents register themselves at binary startup via Go's `init()` mechanism, identical to how `database/sql` drivers work. The `ProtocolDaemon` then reads `defaults.yaml`, matches each `internal_module` string against the registry, and boots the agent with the correct infrastructure injected.

```
init() in each agent package
        │
        ▼
agents.Register("my-agent", NewAgent)   ← global registry map
        │
        ▼
daemon.go iterates agents.GetRegistry()
        │
        ▼
supervisor.RegisterInternalAgent(name, factory)
        │
        ▼
supervisor.LoadAgents(configs from defaults.yaml)
        │   for each config where engine == "internal":
        │   - builds core.Environment (injects DB, EntityResolver if requested)
        │   - calls factory(env) → core.Runnable
        │   - calls .Start()
        ▼
Agent is live, subscribed to JetStream
```

---

## Registered Agents

| `internal_module` | Package | DB | EntityResolver | Listens On |
|---|---|---|---|---|
| `cleanup-agent` | `tap/agents/cleanup` | ✅ | ❌ | `tasks.accounting.cleanup.>` |
| `reconcile-expense-agent` | `tap/agents/reconcile_expense` | ✅ | ✅ | `proof.accounting.cleanup.enrichment` |
| `reconcile-revenue-agent` | `tap/agents/reconcile_revenue` | ✅ | ✅ | `proof.accounting.cleanup.enrichment` |
| `approval-agent` | `tap/agents/approval` | ❌ | ❌ | `accounting.approved` |
| `stripe-processor-agent` | `tap/agents/stripe_processor` | ✅ | ❌ | `stripe.checkout.session.completed` |

---

## Adding a New Agent

### 1. Create the package

```
tap/agents/my_agent/agent.go
```

### 2. Implement the constructor and register

```go
package my_agent

import (
    "github.com/Yankzy/usetoro/tap/agents"
    "github.com/Yankzy/usetoro/tap/pkg/agent"
    "github.com/Yankzy/usetoro/tap/pkg/core"
    "github.com/nats-io/nats.go"
)

func init() {
    agents.Register("my-agent", NewAgent)
}

func NewAgent(env core.Environment) core.Runnable {
    var a MyAgent

    handler := func(msg *nats.Msg) {
        // your logic
        msg.Ack()
    }

    a.BaseAgent = agent.NewBaseAgent(
        env.Logger, env.Bus, env.Config, env.Memory,
        "my.agent.type",         // agentType (used in Redux trace)
        "tasks.my.subject.>",    // NATS subject to subscribe to
        "my-agent-group",        // queue group
        "my-agent-durable",      // durable consumer name
        handler,
    )
    return &a
}
```

### 3. Add a blank import to `go/cmd/protocol/main.go`

```go
import (
    _ "github.com/Yankzy/usetoro/tap/agents/my_agent"
)
```

### 4. Add a config entry to `go/internal/config/defaults.yaml`

```yaml
agents:
  - did: "did:toro:agent:my_agent_1"
    name: "My Agent"
    model: "gpt-4o-mini"
    engine: "internal"
    internal_module: "my-agent"
    dependencies:
      database: true    # set false if not needed
      entity_resolver: false
```

### 5. Run `make vndr`

```bash
make vndr
```

That's it. The daemon will discover and start your agent on next boot.

---

## Dependency Injection

The `Supervisor` reads the `dependencies` block from YAML and conditionally populates `core.Environment` before calling your factory. **You never ask for infra directly — you declare what you need in YAML.**

| YAML key | Populated field | Type |
|---|---|---|
| `database: true` | `env.DBPool` | `*pgxpool.Pool` — raw connection pool for transactions, batch ops |
| `db_queries: true` | `env.Queries` | `*database.Queries` — sqlc query layer |
| `entity_resolver: true` | `env.EntityResolver` | `*ai.EntityResolver` — vendor/customer/account AI matching |

Fields not requested are `nil`. If your agent accesses `env.Queries` without declaring `db_queries: true`, it will panic.

---

## Key Files

| File | Role |
|---|---|
| `tap/agents/registry.go` | Global factory map + `Register()` / `GetRegistry()` functions |
| `tap/pkg/core/agent_types.go` | `Environment`, `AgentConfig`, `Runnable`, `EventBus`, `MemoryStore` |
| `tap/pkg/agent/base.go` | `BaseAgent` — handles subscription, almanac registration, signing |
| `tap/pkg/agent/supervisor.go` | Instantiates agents, manages lifecycle |
| `tap/pkg/daemon/daemon.go` | Bootstraps supervisor, iterates registry, calls `LoadAgents` |
| `go/internal/config/defaults.yaml` | Declarative agent list — the single source of truth |

---

## Generating a New Agent with LLM

A prompt template containing all required interfaces, struct signatures, messaging protocol, and the YAML format is available at:

```
.gemini/antigravity/brain/.../agent_generation_prompt.md
```

Fill in the specification block and send it to any capable LLM. The output drops directly into `tap/agents/<package>/agent.go`.
