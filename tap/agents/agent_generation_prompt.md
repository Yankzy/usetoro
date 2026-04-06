 TAP Agent Generation Prompt

Copy the block below and fill in the `[PLACEHOLDERS]` before sending it to an LLM.

---

````
You are an expert Go developer. Your task is to generate a complete, compilable Go source file for a new internal TAP agent.

Agent Specification

- Agent Name: [e.g. "Invoice Deduplication Agent"]
- Package Name: [e.g. `invoice_dedup`]
- `internal_module` key (must match defaults.yaml): [e.g. `"invoice-dedup-agent"`]
- NATS subject it listens on: [e.g. `"proof.accounting.cleanup.columns"`]
- NATS queue group: [e.g. `"invoice-dedup-group"`]
- Durable consumer name: [e.g. `"invoice-dedup-durable"`]
- Output NATS subject (what it publishes to): [e.g. `"proof.accounting.dedup.done"`]
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
    DB             *database.Queries    // nil if dependencies.database = false
    DBPool         *pgxpool.Pool        // nil if dependencies.database = false
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
    agentType string,       // e.g. "accounting.dedup"
    topic string,           // NATS subject to subscribe to
    queueGroup string,
    durableName string,
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

6. Messaging Protocol — TAP Envelopes

All inter-agent messages use `core.Envelope`. Incoming messages are expected to contain a specific `Performative` (verb):

```go
// Performatives
const (
    CFP     Performative = "cfp"     // Call For Proposal — initiates negotiation
    PROPOSE Performative = "propose" // Bid/quote response
    ACCEPT  Performative = "accept"  // Accept a proposal
    REJECT  Performative = "reject"
    INFORM  Performative = "inform"  // Deliver result/proof (most common output verb)
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

7. Proof — Standard Output Payload

When your agent completes work, wrap output in a `core.Proof` and publish it inside an `INFORM` envelope:

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

8. Poison Pill Pattern (Required)

Every message handler MUST include this guard:

```go
meta, metaErr := msg.Metadata()
if metaErr == nil && meta.NumDelivered > 3 {
    a.Logger.Error("Poison pill detected", "subject", msg.Subject)
    msg.Term()
    return
}
```

9. `defaults.yaml` Entry (include this in your response)

```yaml
- did: "did:toro:agent:<your_did_suffix>"
  name: "<Human Readable Name>"
  model: "gpt-4o-mini"
  engine: "internal"
  internal_module: "<internal_module_key>"
  dependencies:
    database: true|false
    entity_resolver: true|false
```

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
    "github.com/Yankzy/usetoro/internal/database"     // only if database dependency = true
)
```

---

What to Return

Return only the Go source file (`agent.go`) for the new agent package, plus the YAML snippet for `defaults.yaml`. Do not return anything else.
````
