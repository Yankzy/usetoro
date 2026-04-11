 TAP Agent Generation Prompt

Copy the block below and fill in the `[PLACEHOLDERS]` before sending it to an LLM.

---

````
You are an expert Go developer. Your task is to generate a complete, compilable Go source file for a new internal TAP agent.

Agent Specification

- Agent Name: "[AGENT_NAME]" (e.g. shoebox)
- Package Name: `[PACKAGE_NAME]`
- `internal_module` key (must match defaults.yaml): `"[INTERNAL_MODULE_KEY]"`
- `activity_type` (the semantic activity this agent fulfills): `"[ACTIVITY_TYPE]"` (e.g. `"agents.accounting.map_csv"`)
- Task Queue (NATS public topic where the Orchestrator broadcasts CFPs): `"[TASK_QUEUE]"`
- Durable consumer name: `"[DURABLE_NAME]"`
- Purpose / Business Logic:
  [Describe what the agent does in plain English. e.g. "Reads mapped rows from the cleanup stage, queries the database for existing transactions with the same description+amount+date, and marks duplicates before passing to reconciliation."]
- Dependencies needed:
  - `database: true` / `false`
  - `entity_resolver: true` / `false`

---

Framework Contracts You MUST Follow

1. Registration (`init()`)

Every agent MUST register itself in `init()` using:

```go
import "github.com/Yankzy/usetoro/tap/agents"

func init() {
    agents.Register("<internal_module_key>", NewAgent)
}
```

2. Constructor Signature

The constructor MUST match exactly:

```go
func NewAgent(env core.Environment) core.Runnable
```

3. The `core.Environment` Struct (your only constructor parameter)

```go
// package: github.com/Yankzy/usetoro/tap/pkg/core

type Environment struct {
    Logger         *slog.Logger
    Bus            EventBus             // NATS abstraction
    Config         AgentConfig          // Parsed from defaults.yaml
    Memory         MemoryStore          // Long-term RAG memory
    Queries        *database.Queries    // nil if dependencies.db_queries = true
    DBPool         *pgxpool.Pool        // nil if dependencies.database = true
    EntityResolver *ai.EntityResolver   // nil if dependencies.entity_resolver = false
}
```

4. Interfaces

```go
// EventBus — publish and subscribe over NATS JetStream
type EventBus interface {
    Publish(subject string, data []byte) error
    RequestWithContext(ctx context.Context, subject string, data []byte) (*nats.Msg, error)
    QueueSubscribe(subj, queue string, cb nats.MsgHandler, opts ...nats.SubOpt) (*nats.Subscription, error)
}

// MemoryStore — optional long-term memory
type MemoryStore interface {
    Recall(ctx context.Context, realmID, query string) (string, error)
    Learn(ctx context.Context, realmID, trigger, instruction string) error
}

// Runnable — what the supervisor manages
type Runnable interface {
    Start() error
    Stop() error
}
```

5. `BaseAgent` — Use This, Don't Reimplement It

Embed `*agent.BaseAgent` in your struct. It handles:
- Almanac registration (DID discovery)
- JetStream queue subscription with durable consumer
- Ed25519 keypair generation (`b.KP`)

Instantiate it with:

```go
// package: github.com/Yankzy/usetoro/tap/pkg/agent
func NewBaseAgent(
    logger *slog.Logger,
    bus core.EventBus,
    cfg core.AgentConfig,
    mem core.MemoryStore,
    handler nats.MsgHandler,
) *BaseAgent
```

`BaseAgent` fields available inside your agent:
```go
b.Logger      *slog.Logger
b.Bus         core.EventBus
b.Cfg         core.AgentConfig    // b.Cfg.DID is your agent's DID
b.Mem         core.MemoryStore
b.KP          *identity.KeyPair   // for signing envelopes/proofs
b.Sub         *nats.Subscription
```

6. Agent Runtime — Reasoning & ExecWithPaging

Standard agents should use `agent.Runtime` for LLM interaction. This provides `ExecWithPaging` which supports structural document fetching and tool calling.

```go
// In your NewAgent constructor:
a.rt = agent.NewRuntime(env.Logger, env.Bus, env.Config, env.Memory)

// Usage:
resp, err := a.rt.ExecWithPaging(ctx, prompt, nil, nil)
```

7. Redux Integration — `ExecuteGlobalWorkflow`

If your agent uses an LLM to generate state mutations, you MUST run those patches through the Redux engine via `ExecuteGlobalWorkflow`. This provides RBAC, schema validation, array bans, payload limits, and a circuit-breaker retry loop.

```go
// LLMCallback receives faults from previous Redux rejections so the LLM can self-correct.
type LLMCallback func(previousErrors []redux.DomainFault, currentSeq uint64, baseState []byte) ([]json.RawMessage, error)

// WorkflowConfig holds per-invocation Redux tuning.
type WorkflowConfig struct {
    SchemaString string           // JSON Schema for post-patch drift validation
    RBAC         redux.RBACPolicy // Actor path-authorization boundaries
    InitialState []byte           // Base JSON state (nil defaults to "{}")
}

// Usage:
// workflowID is a pgtype.UUID
err := a.ExecuteGlobalWorkflow(
    ctx,
    a.Queries,
    workflowID,
    agent.WorkflowConfig{
        SchemaString: `{"type": "object", "properties": {"status": {"type": "string"}}}`,
        RBAC: redux.RBACPolicy{
            AllowedPrefixes: map[string][]string{
                a.Cfg.DID: {"/status", "/mapped_rows"},
            },
        },
    },
    llmCallback,
    func(nextState []byte) error {
        // [onComplete]: Called ONLY after Redux validates the final state.
        // This is a closure used for side effects after the state is persisted.

        // 1. Extract data from the nextState (validated JSON)
        var validatedState map[string]json.RawMessage
        json.Unmarshal(nextState, &validatedState)

        // 2. Perform side effects (e.g., Publish a Proof, trigger another service)
        // Standard pattern: Publish "informed" result/proof to JetStream.
        return nil
    },
)
```

Key behaviors:
- The circuit breaker retries up to 3 times, feeding `DomainFault`s back to the LLM callback.
- If all 3 attempts produce faults, the workflow returns an error.
- The `onComplete` handler only fires after Redux validates the final state. This ensures that any "proof" published is backed by a valid, persisted database state.
- JetStream trace events are published automatically to `workflow.trace.<workflowID>`.

8. Messaging Protocol — TAP Envelopes

All inter-agent messages use `core.Envelope`. Incoming messages are expected to contain a specific `Performative` (verb):

```go
// Performatives (from tap/pkg/core/verbs.go)
const (
    CFP             Performative = "cfp"             // Call For Proposal — initiates negotiation
    PROPOSE         Performative = "propose"         // Bid/quote response
    ACCEPT_PROPOSAL Performative = "accept-proposal" // Accepted bid (The "Deal")
    REJECT_PROPOSAL Performative = "reject-proposal"
    INFORM          Performative = "inform"          // Deliver result/proof (most common output verb)
)

type Envelope struct {
    ID             string          `json:"id"`
    Timestamp      time.Time       `json:"ts"`
    SenderDID      string          `json:"src"`
    ReceiverDID    string          `json:"dst,omitempty"`
    Performative   Performative    `json:"perf"`
    ConversationID string          `json:"cid,omitempty"`
    Body           json.RawMessage `json:"body"`
    Signature      string          `json:"sig"`
}

// Helper
func NewEnvelope(id, src, dst, cid string, verb Performative, body interface{}) (*Envelope, error)
```

9. Proof — Standard Output Payload

When your agent completes work, wrap output in a `core.Proof` and publish it inside an `INFORM` envelope to the `orchestrator.inbox` subject:

```go
type Proof struct {
    TaskID    string          `json:"task_id"`
    Type      ProofType       `json:"type"`
    Timestamp int64           `json:"ts"`
    Data      json.RawMessage `json:"data"`
    Signature string          `json:"sig"`
}

const ProofAPI ProofType = "proof.api"
```

10. Poison Pill Pattern (Required)

Every message handler MUST include this guard:

```go
meta, metaErr := msg.Metadata()
if metaErr == nil && meta.NumDelivered > 3 {
    a.Logger.Error("Poison pill detected", "subject", msg.Subject)
    msg.Term()
    return
}
```

11. `defaults.yaml` Entry (include this in your response)

**IMPORTANT:** Agents in `defaults.yaml` define their BRAIN ONLY (DID, model, system prompt, dependencies). They do NOT declare `subscribe_to` or `publish_to` — the Workflow Orchestrator owns topology routing via the `task_queue` field in the pipeline YAML.

```yaml
- did: "did:toro:agent:<your_did_suffix>"
  name: "<Human Readable Name>"
  model: "gpt-4o-mini"
  engine: "internal"
  internal_module: "<internal_module_key>"
  activity_type: "<activity_type>" # e.g. agents.accounting.map_csv
  system_prompt: |
    [Your system prompt here]
  dependencies:
    database: true|false
    entity_resolver: true|false
```

12. FIPA Handshake (How Agents Interact with the Workflow Orchestrator)

When `negotiate: true` is set on a workflow step, the Orchestrator will broadcast a `CFP` to the public `task_queue` topic. Your agent must:

1. **Listen on the Task Queue**: Subscribe to the public NATS `task_queue` subject with a durable JetStream consumer.
2. **Send a `PROPOSE`**: Reply with a bid envelope to `core.BuildAgentInbox(env.SenderDID)` (the Orchestrator's inbox).
3. **Wait for `ACCEPT_PROPOSAL`**: Once the Orchestrator selects the winner, it dispatches the payload via an `ACCEPT_PROPOSAL` envelope directly to your agent's private inbox (`agent.inbox.{YOUR_DID}`).
4. **Execute and Prove**: Perform computation via `ExecuteGlobalWorkflow`. When the `onComplete` closure fires, publish a `core.Proof` wrapped in an `INFORM` envelope to `orchestrator.inbox`.

```go
// Pattern for public Task Queue subscription:
// handler bound via agent.NewBaseAgent() — it fires on incoming CFP AND on incoming ACCEPT_PROPOSAL.
// Detect performative:
if env.Performative == core.CFP {
    // Reply with PROPOSE to orchestrator inbox
}
if env.Performative == core.ACCEPT_PROPOSAL {
    // Execute the task and prove back to orchestrator
    return a.executeTask(env)
}
```

12. Architecture Boundaries (Workers vs Agents)

- **CRITICAL RULE**: Agents **DO NOT** write to Postgres directly (no `db.InsertX` or `db.UpdateX`).
- Agents are stateless intelligence units. They fetch necessary context, perform LLM computation, validate state via Redux, and use the `onComplete` closure to output a `core.Proof` via an `INFORM` envelope.
- If you need to write to the database, that is the job of a **Worker**.

---

Standard Imports

```go
import (
    "context"
    "encoding/json"

    "github.com/google/uuid"
    "github.com/nats-io/nats.go"

    "github.com/Yankzy/usetoro/tap/agents"          // for agents.Register()
    "github.com/Yankzy/usetoro/tap/pkg/agent"         // for agent.BaseAgent, agent.NewBaseAgent
    "github.com/Yankzy/usetoro/tap/pkg/core"          // for core.Environment, core.Envelope, core.Proof etc.
    "github.com/Yankzy/usetoro/tap/pkg/redux"         // for redux.RBACPolicy, redux.DomainFault
    "github.com/jackc/pgx/v5/pgtype"                  // for workflowID (pgtype.UUID)
    "github.com/Yankzy/usetoro/internal/database"     // only if database dependency = true
)
```

---

What to Return

Return only the Go source file (`agent.go`) for the new agent package, plus the YAML snippet for `defaults.yaml`. Do not return anything else.
````
