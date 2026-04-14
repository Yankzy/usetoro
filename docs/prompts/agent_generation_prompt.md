TAP Agent Generation Prompt

Copy the block below and fill in the `[PLACEHOLDERS]` before sending it to an LLM.

---

````
You are an expert Go developer. Generate a complete, compilable `agent.go` file for a new internal TAP agent, plus a matching `defaults.yaml` snippet.

Agent Specification

- Agent Display Name: "[AGENT_DISPLAY_NAME]"
- Package Name: `[PACKAGE_NAME]` (directory: `tap/agents/[PACKAGE_NAME]/`)
- `internal_module` key: `"[INTERNAL_MODULE_KEY]"`
- `activity_type`: `"[ACTIVITY_TYPE]"` (must start with `agents.`)
- Purpose / Business Logic:
  [Describe exact behavior and side effects.]
- Input Envelope Shape:
  [Describe what arrives in `env.Body` and expected performative(s).]
- Output Proof Shape:
  [Describe what to publish in `core.Proof.Data` and when.]
- Uses Redux (`ExecuteGlobalWorkflow`): `[YES|NO]`
- If Redux=YES, allowed state paths for this agent DID:
  `["/status", "/...optional_paths..."]`
- Dependencies required in `defaults.yaml`:
  - `database: true|false`
  - `db_queries: true|false`
  - `entity_resolver: true|false`
- System Prompt:
  [Provide a concrete system prompt, or "none" if not needed.]

Framework Contracts You MUST Follow

1. Registration + Constructor

```go
import "github.com/Yankzy/usetoro/tap/agents"

func init() {
    agents.Register("[INTERNAL_MODULE_KEY]", NewAgent)
}

func NewAgent(env core.Environment) core.Runnable
```

2. Use `agent.BaseAgent` (do not hand-roll subscription wiring)

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

Important runtime behavior:
- `BaseAgent` derives DID, queue group, durable name, and canonical task queue routing from `activity_type`.
- Do NOT hardcode queue group/durable names inside the agent.
- Public task queue routing is orchestrator-owned.

3. Constructor Environment + Dependencies

```go
// package: github.com/Yankzy/usetoro/tap/pkg/core
type Environment struct {
    Logger         *slog.Logger
    Bus            EventBus
    Config         AgentConfig
    Memory         MemoryStore
    Queries        *database.Queries
    DBPool         *pgxpool.Pool
    EntityResolver *ai.EntityResolver
}
```

Dependency rules:
- If you use `env.Queries`, require `db_queries: true`.
- If you use `env.DBPool`, require `database: true`.
- If you use `env.EntityResolver`, require `entity_resolver: true`.

4. Handler Rules (Ack/Nak/Term)

Your handler must:
- implement poison-pill guard (`msg.Metadata().NumDelivered > 3` -> `msg.Term()`)
- unmarshal `core.Envelope`
- validate performative using `core.IsValidPerformative`
- ignore unsupported performatives by returning `nil`
- return `error` only for transient failures

Suggested poison-pill guard:

```go
meta, metaErr := msg.Metadata()
if metaErr == nil && meta.NumDelivered > 3 {
    a.Logger.Error("Poison pill detected, terminating message", "subject", msg.Subject)
    msg.Term()
    return
}
```

5. Orchestrator Handshake Compatibility

Current orchestrator behavior may not always send a follow-up `ACCEPT_PROPOSAL` for negotiated steps yet.

For `CFP` flows, implement this pattern:
- parse CFP
- send `PROPOSE` to `core.BuildAgentInbox(env.SenderDID)`
- execute from the same CFP envelope immediately

Also support `ACCEPT_PROPOSAL` by routing it to the same execute path for forward compatibility.

6. TAP Envelope + Proof Contracts

```go
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

type Proof struct {
    TaskID    string          `json:"task_id"`
    Type      ProofType       `json:"type"`
    Timestamp int64           `json:"ts"`
    Data      json.RawMessage `json:"data"`
    Signature string          `json:"sig"`
}
```

Completion convention:
- publish `core.Envelope{perf: inform, cid: original conversation id, body: core.Proof}` to `workflows.OrchestratorInbox` (`"orchestrator.inbox"`).

7. TaskDefinition Shape (Orchestrator dispatch body)

```go
type TaskDefinition struct {
    ID         string
    Domain     string
    Complexity core.TaskComplexity
    Reward     int64
    Currency   string
    Payload    json.RawMessage
    ExpiresAt  int64
}
```

Notes:
- `task.ID` is the workflow instance UUID.
- Some payment-related fields may be zero/empty depending on orchestrator phase.

8. Redux Path (only if `Uses Redux = YES`)

Use `a.ExecuteGlobalWorkflow(...)` with:
- `workflowID` parsed from `task.ID` into `pgtype.UUID`
- `agent.WorkflowConfig` with `SchemaString` + strict RBAC allowed prefixes
- retry-aware LLM callback signature:

```go
func(previousFaults []redux.DomainFault, currentSeq uint64, baseState []byte) ([]json.RawMessage, error)
```

`onComplete` semantics (important):
- called only after Redux reduction succeeds and trace publish succeeds
- trace topic: `workflow.trace.<workflowID>`
- use `onComplete` for side effects like publishing proof envelopes
- do not claim DB rollup persistence happened synchronously in `onComplete`

9. One-shot Learning Example (from `tap/agents/csv_mapping/agent.go`)

Use this as a style anchor. Mirror the flow and structure, then swap in your own domain types.

```go
type CSVMappingAgent struct {
    *agent.BaseAgent
    rt      *agent.Runtime
    queries *database.Queries
}

func init() {
    agents.Register("csv-mapping-agent", NewAgent)
}

func NewAgent(env core.Environment) core.Runnable {
    var a CSVMappingAgent
    a.rt = agent.NewRuntime(env.Logger, env.Bus, env.Config, env.Memory)
    a.queries = env.Queries

    handler := func(msg *nats.Msg) {
        // poison-pill guard
        // a.handleCFP(msg)
        // ack on nil error, nak on transient
    }

    a.BaseAgent = agent.NewBaseAgent(env.Logger, env.Bus, env.Config, env.Memory, handler)
    return &a
}

func (a *CSVMappingAgent) handleCFP(msg *nats.Msg) error {
    var env core.Envelope
    _ = json.Unmarshal(msg.Data, &env)
    if env.Performative != core.CFP {
        return nil
    }

    // send PROPOSE back to sender inbox
    proposal := map[string]interface{}{"price": 1, "eta": "10s"}
    replyEnv, _ := core.NewEnvelope(uuid.New().String(), a.Cfg.DID, env.SenderDID, env.ConversationID, core.PROPOSE, proposal)
    replyEnv.Signature = a.KP.Sign(replyEnv.Body)
    replyBytes, _ := json.Marshal(replyEnv)
    if err := a.Bus.Publish(core.BuildAgentInbox(env.SenderDID), replyBytes); err != nil {
        return err
    }

    // current negotiated-path compatibility: execute immediately from CFP
    return a.executeTask(env)
}

func (a *CSVMappingAgent) executeTask(cfpEnv core.Envelope) error {
    // parse TaskDefinition, build Redux llmCallback + onComplete
    // onComplete publishes INFORM(core.Proof) to workflows.OrchestratorInbox
    return a.ExecuteGlobalWorkflow(...)
}
```

Critical behaviors to copy from this one-shot:
- `CFP` -> `PROPOSE` -> execute immediately.
- Redux callback applies constrained RFC6902 patches.
- `onComplete` extracts validated state and publishes proof to `orchestrator.inbox`.
- clear distinction between workflow ID (`task.ID`) and business/session IDs in payload.

10. `defaults.yaml` Snippet Rules

Generate a matching snippet for `go/internal/config/defaults.yaml`.
Include:
- `name`, `model`, `engine: internal`, `internal_module`, `activity_type`
- `system_prompt` when relevant
- `dependencies` including `db_queries`
- `workflow_schema` when Redux is used

Do NOT include:
- `task_queue`
- queue group
- durable name

Template:

```yaml
- did: "did:toro:agent:[DID_SUFFIX]"
  name: "[AGENT_DISPLAY_NAME]"
  model: "gpt-5.4-mini"
  engine: "internal"
  internal_module: "[INTERNAL_MODULE_KEY]"
  activity_type: "[ACTIVITY_TYPE]"
  workflow_schema: '[OPTIONAL_JSON_SCHEMA_STRING_IF_REDUX]'
  system_prompt: |
    [SYSTEM_PROMPT]
  dependencies:
    database: [true|false]
    db_queries: [true|false]
    entity_resolver: [true|false]
```

Quality Bar

- Must compile without placeholder tokens.
- Use exact signatures from this prompt.
- No pseudocode or TODO stubs.
- Include all necessary imports only.
- Keep logic deterministic and production-safe.

What to Return

Return exactly two blocks:
1. `agent.go` source
2. `defaults.yaml` snippet

Return nothing else.
````

Placeholder Values

```env
AGENT_DISPLAY_NAME="OCR_Agent"
PACKAGE_NAME="ocr_agent"
INTERNAL_MODULE_KEY="ocr_agent"
ACTIVITY_TYPE="ocr"
DID_SUFFIX="ocr_agent"
OPTIONAL_JSON_SCHEMA_STRING_IF_REDUX=""
SYSTEM_PROMPT="You are an OCR agent that analyzes incoming MMS images, receipts, and other documents, and extracts structured context from them."
```
