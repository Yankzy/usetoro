# Almanac System Audit — Agent Discovery, Negotiation, Escrow & Payment

**Prepared for:** Technical Investor
**Date:** 2026-05-23
**Scope:** Full-stack review of the Toro Agent Protocol (TAP) Almanac — the registry, discovery, RFP/negotiation, contract lifecycle, escrow, and payment systems.

---

## 1. Executive Summary

The **Almanac** is the agent registry and discovery backbone of the Toro platform — it is the "DNS for AI agents." Agents register their capabilities with the Almanac via NATS JetStream heartbeats, and other agents (or the Orchestrator, or the API gateway, or the General Agent's LLM) query it at runtime to discover who can perform a given task.

The system implements a **FIPA-ACL negotiation protocol** (Call for Proposal → Propose → Accept) over NATS for competitive bidding on tasks. A **contract machine** handles the state transitions from draft through locked to settled, with signature verification using Ed25519/DID identity. An **escrow interface** and an **auction interface** are defined but **not yet implemented**. A **micrion microtransaction system** exists for per-call billing of the Almanac query service itself, backed by a dual-layer accounting system (NATS KV for hot-path execution + PostgreSQL for canonical fiat records).

**Bottom line:** The discovery and negotiation layer is working in production code. The financial settlement layer (escrow, auction, actual funds transfer on contract settlement) is architected with clean interfaces but has stub implementations — this is the primary gap between the current system and a fully autonomous agent economy.

---

## 2. What Is the Almanac? (The "Why")

### 2.1 Purpose

The Almanac solves a core multi-agent problem: **how does Agent A discover Agent B at runtime without hardcoded addresses?** In a system where agents can spin up/down dynamically, have varying capabilities, and may be operated by different entities, you need a live registry.

The Almanac serves three roles:

1. **Dynamic DNS** — Maps agent DIDs (decentralized identifiers) to their NATS inbox endpoints so messages can be routed.
2. **Capability registry** — Agents advertise *what they can do* (e.g., `"perception.ocr"`, `"agents.accounting.classify_outflow"`, `"logistics.trucking"`), enabling capability-based discovery.
3. **Reputation-gated discovery** — Queries can filter by minimum reputation score, so callers can prefer high-quality agents.

### 2.2 Design Philosophy

From the codebase documentation (`tap/docs/agent.md:31-33`):

> "The Almanac serves as the dynamic DNS and capabilities registry for the hive."

And from `docs/composability.md:4`:

> "Dynamic registry via Almanac."

The Almanac is deliberately **not a blockchain or consensus system**. It is a soft-state, TTL-based registry backed by NATS JetStream for durability. Agents heartbeat their presence; entries expire if not refreshed. This is a pragmatic choice favoring liveness and simplicity over strong consistency.

---

## 3. How It Works (The "How")

### 3.1 Architecture Diagram

```
┌──────────────────────────────────────────────────────────────┐
│                    Protocol Daemon                           │
│              (tap/pkg/daemon/daemon.go)                      │
│                                                              │
│  ┌──────────────────────┐   ┌───────────────────────────┐   │
│  │   Almanac Registry   │   │     Orchestrator           │   │
│  │  (lookup/ package)   │   │  (workflows/ package)      │   │
│  │                      │   │                           │   │
│  │  In-memory store:    │   │  Reads YAML blueprints     │   │
│  │  map[DID]AlmanacEntry│   │  Dispatches workflow steps │   │
│  │  + TTL map           │   │  Resolves actors via       │   │
│  │                      │   │  Almanac at dispatch time  │   │
│  │  Cleanup loop: 30s   │   │                           │   │
│  └──────────┬───────────┘   └─────────────┬─────────────┘   │
└─────────────┼─────────────────────────────┼─────────────────┘
              │                             │
     ┌────────┴─────────── NATS JetStream ──┴────────────┐
     │                                                    │
     │  Stream: ALMANAC (replicas: 3, max_age: 72h)       │
     │  ┌──────────────────────────────────────────────┐  │
     │  │ almanac.register   — agent heartbeats        │  │
     │  │ almanac.query      — discovery request/reply │  │
     │  │ almanac.register.dlq — poisoned message DLQ  │  │
     │  └──────────────────────────────────────────────┘  │
     │                                                    │
     │  agents.<did>.inbox  — direct agent addressing     │
     │  tasks.<d>.<c>.<t>   — CFP broadcast channels      │
     │  workers.inbox.<id>  — worker addressing           │
     └────────────────────────────────────────────────────┘
              │
    ┌─────────┴──────────┐
    │                    │
┌───┴────────┐   ┌───────┴──────────┐
│  Agents    │   │  Workers         │
│            │   │                  │
│ Register   │   │ Internal DB ops  │
│ via        │   │ (not Almanac-    │
│ base.Start │   │  registered)     │
│ 24h TTL    │   │                  │
└────────────┘   └──────────────────┘
```

### 3.2 Data Model

#### Registration Payload (what agents publish)

```go
// tap/pkg/lookup/main.go:34-42
type RegistrationPayload struct {
    DID            string                   `json:"did"`
    Endpoints      []string                 `json:"endpoints"`       // NATS inbox subjects
    Capabilities   []RegistrationCapability `json:"capabilities"`
    Expiry         time.Time                `json:"expiry"`
    Signature      string                   `json:"signature"`       // Ed25519
    CertificatePEM string                   `json:"certificate_pem"`
}

type RegistrationCapability struct {
    Type string                 `json:"type"`            // e.g. "perception.ocr"
    Meta map[string]interface{} `json:"meta,omitempty"`  // e.g. {"formats": ["pdf","png"]}
}
```

#### AlmanacEntry (what is stored and returned on queries)

```go
// tap/pkg/lookup/main.go:21-25
type AlmanacEntry struct {
    DID          string                   `json:"did"`
    Endpoints    []string                 `json:"endpoints"`
    Capabilities []RegistrationCapability `json:"capabilities"`
}
```

#### AlmanacQuery (discovery request)

```go
// tap/pkg/lookup/main.go:12-18
type AlmanacQuery struct {
    CallerDID      string                 `json:"caller_did,omitempty"`      // For Micrion billing
    CapabilityType string                 `json:"capability_type,omitempty"` // e.g., "logistics.trucking"
    DID            string                 `json:"did,omitempty"`             // Direct DID lookup
    MetaFilter     map[string]interface{} `json:"meta_filter,omitempty"`     // e.g., {"location": "NY"}
    MinReputation  int                    `json:"min_rep,omitempty"`         // Quality gate
}
```

#### Registry (server-side, in-memory)

```go
// tap/pkg/lookup/almanac_server.go:22-31
type Registry struct {
    logger  *slog.Logger
    nc      *nats.Conn
    js      nats.JetStreamContext
    mu      sync.RWMutex
    entries map[string]AlmanacEntry  // keyed by DID
    ttls    map[string]time.Time     // keyed by DID
}
```

#### Contract (negotiation result)

```go
// tap/pkg/core/primitives.go:103-119
type Contract struct {
    ID             string          `json:"id"`
    ConversationID string          `json:"cid"`
    InitiatorDID   string          `json:"initiator_did"`
    AcceptorDID    string          `json:"acceptor_did"`
    Terms          json.RawMessage `json:"terms"`
    TermsHash      string          `json:"terms_hash"`
    Signatures     map[string]string `json:"signatures"`
    Status         ContractStatus  `json:"status"`
    CreatedAt      time.Time       `json:"created_at"`
}
```

#### Contract Status State Machine

```
DRAFT → PROPOSED → VALIDATED → ESCROWED → LOCKED → IN_PROGRESS → SETTLED
                                                            ↘ DISPUTED
```

#### NATS Subjects

| Subject | Pattern | Purpose | Source |
|---------|---------|---------|--------|
| `almanac.register` | Exact | Agent heartbeats (JetStream durable) | `topics.go:116` |
| `almanac.query` | Exact | Discovery request-reply | `topics.go:113` |
| `almanac.register.dlq` | Exact | Poisoned registration DLQ | `almanac_server.go:17` |
| `almanac.<domain>.<complexity>.<type>` | Pattern | Per-domain almanac routing | `topics.go:33-35` |
| `agents.<sanitized-did>.inbox` | Pattern | Direct agent addressing | `topics.go:50-52` |
| `tasks.<domain>.<complexity>.<type>` | Pattern | CFP broadcast channels | `topics.go:23-25` |
| `workers.inbox.<id>` | Pattern | Worker addressing | `topics.go:56-58` |
| `contracts.request` | Exact | Contract submission | `defaults.yml:61` |

#### JetStream Configuration

```yaml
# go/internal/config/defaults.yml:64-71
almanac:
  stream_name: ALMANAC
  jetstream:
    replicas: 3
    max_age: 72h
    storage: file
    subjects:
      - almanac.register
      - almanac.query
      - almanac.register.dlq
```

#### JSON Schema (API contract)

```json
// tap/api/json-schema/v1/almanac.json
{
  "required": ["did", "endpoints", "capabilities", "expiry"],
  "properties": {
    "did":          { "type": "string" },
    "endpoints":    { "type": "array", "items": { "type": "string" } },
    "capabilities": { "type": "array", "items": {
      "type": "object",
      "properties": {
        "type": { "type": "string" },
        "meta": { "type": "object" }
      }
    }},
    "expiry":    { "type": "string", "format": "date-time" },
    "signature": { "type": "string" }
  }
}
```

---

## 4. Agent Discovery — All Paths

Agents can be discovered through five distinct paths:

### Path A: SDK Client (programmatic)

```go
// tap/pkg/lookup/main.go:55-78
client := lookup.New(nc, "did:toro:caller")
agents, _ := client.FindAgents("perception.ocr", 2*time.Second)
agent, _  := client.ResolveByDID("did:toro:agent:ocr-v1", 2*time.Second)
agents, _ := client.ResolveByCapabilityType("logistics.trucking", 2*time.Second)
```

All methods send a synchronous NATS `Request` to `almanac.query` and unmarshal the response.

### Path B: Convenience Helpers (wrappers)

```go
// tap/pkg/lookup/almanac_helpers.go:1-59
RegisterAlmanac(bus, payload)
FindAgents(bus, capability, timeout)
ResolveAgentByDID(bus, did, timeout)
ResolveAgentsByCapability(bus, capability, timeout)
ResolveAgent(bus, query, timeout)
```

### Path C: Orchestrator at Dispatch Time

```go
// tap/workflows/orchestrator.go:128-163
func (o *Orchestrator) resolveActorByCapability(ctx context.Context, activityType string) (string, error) {
    // Sends AlmanacQuery to almanac.query with 5s timeout
    // Returns first matching agent's inbox (first endpoint)
}
```

Used when a workflow step has no explicit `TaskQueue` and the activity type is not a worker prefix (`workers.*`). This is the primary runtime dispatch path — the Orchestrator resolves "who can do `agents.accounting.classify_outflow`?" at the moment it needs to dispatch that step.

### Path D: General Agent LLM Tool

```go
// tap/agents/general_agent/agent.go:423-471
// The "AlmanacLookup" tool is exposed to the LLM so it can dynamically
// discover specialized agents at reasoning time.
//
// The LLM then uses the "Delegate" tool (line 473-536) to create
// dynamic sub-workflows that dispatch to the discovered agents.
```

This is the **composability primitive** — the General Agent can chain specialized agents together at runtime based on Almanac discovery results, without hardcoded workflow definitions.

### Path E: REST API (for frontend)

```go
// go/internal/api/system_handler.go:17-45
// GET /v1/system/actors → queries Almanac for all active agents
// Used by the admin dashboard/workflow visualizer
```

```go
// go/internal/api/system_handler.go:103-133
// discoverActorsForBlueprint() — called when loading a workflow status page
// Queries Almanac for every activity_type in the workflow blueprint
// Returns a map of activity_type → available agents for the visualizer
```

### Path F: Admin CLI Tool

```go
// go/cmd/almanac-check/main.go:1-109
// CLI tool that queries Almanac by DID or capability and prints results
```

---

## 5. Registration Lifecycle

### Agent Startup

```go
// tap/pkg/agent/base.go:191-258
func (b *BaseAgent) Start() error {
    // 1. Generate DID deterministically from activity type seed
    // 2. Build inbox subject: agents.<sanitized-did>.inbox
    // 3. Build registration payload with DID, inbox endpoint, capability type,
    //    and 24-hour expiry
    // 4. Publish to almanac.register (JetStream)
    // 5. Subscribe to private inbox (queue group, durable, explicit ack)
    // 6. Optionally subscribe to public task queue for CFP bidding
}
```

### Registry Handling

```go
// tap/pkg/lookup/almanac_server.go:126-153
func (r *Registry) handleRegistration(msg *nats.Msg) {
    // 1. Check delivery count — if ≥ 5, emit to DLQ and terminate
    // 2. Unmarshal RegistrationPayload
    // 3. Store AlmanacEntry in entries[DID]
    // 4. Set TTL (from payload or default 1 minute)
    // 5. Manual ACK
}
```

Key details:
- Registrations are **JetStream durable** (consumer name: `almanac-register`) — they survive server restarts
- **Max delivery limit: 5** — after 5 failed delivery attempts, the message is moved to `almanac.register.dlq` and terminated
- **Default TTL: 1 minute** — agents specify their own expiry (typically 24h) in the payload
- **No Redis, no database** — the registry is purely in-memory with a 30-second cleanup ticker evicting expired entries. This trades durability of the registry dataset for operational simplicity.

### Registration Signature Verification

The `RegistrationPayload` includes a `Signature` field (Ed25519) and `CertificatePEM`. The handler **parses these fields** but does **not currently verify** the signature against the certificate. This is a noted gap — in production, registrations should be authenticated.

---

## 6. RFP (Call for Proposal) Flow — FIPA Negotiation

The "RFP" concept maps directly to FIPA-ACL speech acts. There is no separate RFP data model — it is the FIPA negotiation protocol over NATS.

### 6.1 Step 1: CFP Broadcast

When a workflow step has `negotiate: true`, the Orchestrator broadcasts a **Call for Proposal** to a public task queue:

```go
// tap/workflows/orchestrator.go:842-883
taskDef := core.TaskDefinition{
    ID:             instanceID,
    Domain:         step.ActivityType,
    Payload:        payload,
    Complexity:     step.Complexity,
    WorkflowSchema: step.WorkflowSchema,
    SystemPrompt:   systemPrompt,
    Model:          step.Model,
    RBACPolicy:     step.RBACPolicy,
}
cfp := core.NewEnvelope(id, OrchestratorDID, "", convID, core.CFP, taskDef)
o.bus.Publish(queue, cfpBytes)  // tasks.<domain>.<complexity>.<type>
```

The TaskDefinition includes:
- **Reward** (in micrions) and **Currency** — the economic incentive
- **Domain** and **Complexity** — for routing and pricing tiers
- **Payload** — the actual work to be done (opaque to the protocol)
- **WorkflowSchema**, **SystemPrompt**, **Model** — optional hints for the agent

### 6.2 Step 2: Agents Reply with PROPOSE

Agents subscribed to the task queue receive the CFP, evaluate it, and reply with a **PROPOSE** envelope directly to the Orchestrator's inbox:

```
Example agents demonstrating this:
- billing-agent.go:83-98    — "finance.billing" capability
- simple_trucker.go:67-99   — "logistics.trucking" capability
```

The proposal contains the agent's bid (price, ETA, etc.).

### 6.3 Step 3: Orchestrator Selects Winner

```go
// tap/workflows/orchestrator.go:1049-1057
// TODO: implement bid selection strategy (price, ETA).
// For now, auto-accept the first proposal.
```

**Current state:** The PROPOSE handler is a **stub**. It auto-accepts the first proposal received. The bid selection strategy (price comparison, ETA optimization, reputation weighting) is a TODO.

### 6.4 Step 4: ACCEPT_PROPOSAL or REJECT_PROPOSAL

The Orchestrator sends `ACCEPT_PROPOSAL` to the winning agent's inbox and `REJECT_PROPOSAL` to the others (verb defined but reject dispatch not yet implemented in code).

### 6.5 Direct Dispatch (Non-Negotiated Path)

When `negotiate` is `false` (the common case), the Orchestrator bypasses the CFP process entirely:

```go
// tap/workflows/orchestrator.go:885-957
// Three dispatch strategies, in priority order:
// 1. Worker activities (workers.*) → derive worker inbox from activity type
// 2. Explicit TaskQueue → use it directly as the inbox
// 3. Neither → query Almanac to resolve a live actor's inbox
```

For worker activities, the performative is `REQUEST` (not `ACCEPT_PROPOSAL`) since there's no negotiation. For agent activities dispatched directly, it uses `ACCEPT_PROPOSAL`.

---

## 7. Contract Lifecycle

### 7.1 Contract Machine

```go
// tap/pkg/contract/main.go:19-28
type Machine struct {
    repo       store.Repository      // Contract & Identity persistence
    Reputation reputation.Engine     // Post-settlement reputation scoring
    Incentive  incentive.Engine      // Pricing/incentive rules
    Constraint constraint.Engine     // Pre-execution gate checks
    Verify     verification.Engine   // Proof verification
    Dispute    dispute.Engine        // Dispute resolution
    EscrowMgr  incentive.EscrowManager // Fund locking/release
}
```

### 7.2 State Transitions

| Method | Transition | Status | Notes |
|--------|-----------|--------|-------|
| `Propose()` | DRAFT → PROPOSED | Working | Runs constraint engine checks |
| `Escrow()` | PROPOSED → ESCROWED | **STUB** | Comment: "To be fully implemented: Escrow.Lock" |
| `Lock()` | ESCROWED → LOCKED | Working | Validates both party Ed25519 signatures against stored DIDs, persists to repo |
| (implicit) | LOCKED → IN_PROGRESS | Not in code | Agent performs work |
| `Settle()` | LOCKED → SETTLED | Working | Validates proof type (GPS/Classification/API) and worker signature, updates status, submits reputation evaluation |
| (not implemented) | * → DISPUTED | Not in code | Dispute engine referenced but not wired |

### 7.3 Signature Verification

```go
// tap/pkg/contract/main.go:138-164
func (m *Machine) verifySig(ctx context.Context, did, data, sig string) bool {
    // 1. Fetch Identity from repository by DID
    // 2. Find first VerificationMethod (public key)
    // 3. Verify Ed25519 signature against the data
}
```

DIDs follow the W3C DID specification format (`did:toro:<id>`). The `Identity` struct includes verification methods, verifiable credentials (W3C-compatible), service endpoints, and a capability vector.

### 7.4 Settlement & Reputation

```go
// tap/pkg/contract/main.go:117-132
// On successful settlement:
// - Async goroutine submits SUCCESS outcome to Reputation Engine
// - Evaluation includes: TaskID, Submitter (initiator), Target (acceptor),
//   Outcome: "SUCCESS", Weight: 1.0
```

Notable: only `SUCCESS` outcomes are submitted. There is no `FAILURE` or `DISPUTED` outcome submission path.

### 7.5 Proof Validation

```go
// tap/pkg/core/primitives.go:132-143
type Proof struct {
    TaskID    string    `json:"task_id"`
    Type      ProofType `json:"type"`      // proof.gps | proof.classification | proof.api
    Timestamp int64     `json:"ts"`
    Data      json.RawMessage `json:"data"`
    Signature string    `json:"sig"`       // Worker's Ed25519 signature of Data
}
```

The `Settle()` method validates that the proof type is one of the known types AND that the worker's signature over the proof data is valid.

---

## 8. Escrow System — Current State

### 8.1 Interface (Defined, Not Implemented)

```go
// tap/pkg/incentive/escrow.go:10-19
type EscrowManager interface {
    Lock(ctx context.Context, contract *core.Contract, amount int64) error
    Release(ctx context.Context, contractID string) error
    Slash(ctx context.Context, contractID string, penaltyAmount int64) error
}
```

Three operations:
- **Lock** — Hold the task reward + any risk collateral from the agent
- **Release** — Transfer escrowed funds on successful verification/settlement
- **Slash** — Burn or transfer collateral on SLA violations

### 8.2 Integration Status

| Location | What exists | Status |
|----------|------------|--------|
| `contract/main.go:28` | `EscrowMgr` field on `Machine` struct | Wired as dependency |
| `contract/main.go:30` | Constructor accepts `EscrowManager` | Injectable |
| `contract/main.go:58-62` | `Escrow()` method | **STUB** — only sets status, does NOT call `EscrowMgr.Lock()` |
| `contract/main.go:87-136` | `Settle()` method | Does NOT call `EscrowMgr.Release()` for fund transfer |
| `incentive/escrow.go` | Interface definition | No concrete implementation anywhere in codebase |

### 8.3 What's Missing for Escrow

1. **No concrete `EscrowManager` implementation** — no struct in the codebase satisfies the interface
2. **No asset ledger** — there is no account/balance system for holding escrowed funds between contract parties
3. **No `EscrowMgr.Lock()` call** — the `Escrow()` method on `ContractMachine` just sets a status
4. **No `EscrowMgr.Release()` call** — settlement transitions the contract but does not move money
5. **No `EscrowMgr.Slash()` call** — no penalty enforcement path exists

---

## 9. Auction System — Current State

### 9.1 Interface (Defined, Not Implemented)

```go
// tap/pkg/incentive/auction.go:19-25
type AuctionManager interface {
    ValidateBid(ctx context.Context, task *core.TaskDefinition, bidAmount int64) (bool, error)
    SettleAuction(ctx context.Context, taskID string) (winnerDID string, finalPrice int64, err error)
}
```

Three auction types defined:
- `FIRST_PRICE_AUCTION`
- `SECOND_PRICE_AUCTION`
- `ADAPTIVE_AUCTION`

**Status:** No concrete implementation. No code in the Orchestrator's PROPOSE handler calls any auction logic.

---

## 10. Payment System — Micrion Microtransactions

The **Micrion** system (`tap/pkg/micrion/`) is a separate payment system focused on **billing for Almanac query usage** (the "tollbooth"), NOT on contract settlement payouts. It uses a dual-layer accounting architecture.

### 10.1 The Micrion Unit

```
1 USD = 1,000,000 µC (micrions)
```

Suggested task rewards by complexity tier:
- **Entry (1):** 1,000,000 µC ($1.00)
- **Junior (5):** 5,000,000 µC ($5.00)
- **Senior (10):** 10,000,000 µC ($10.00)

### 10.2 Dual-Layer Architecture

**Layer 1 — NATS KV (hot execution path):**
```go
// tap/pkg/micrion/store.go
// Bucket: "micrions"
// Key format: "agent.<sanitized-did>.balance"
// Value: {"entity_id": "...", "balance": 5000000, "uncommitted_burns": 0, "uncommitted_count": 0}
//
// Operations:
// - TopUp(agentDID, amount, entityID) — CAS-based credit
// - MicroBurn(agentDID, toll) — CAS-based debit with 100-retry loop
```

**Layer 2 — PostgreSQL (canonical fiat ledger):**
```go
// tap/pkg/micrion/wallet.go:12-22
type FiatLedger interface {
    LogPurchase(ctx, entityID, stripeSessionID, usdAmount, micrionAmount) error
    LogBulkBurn(ctx, entityID, agentDID, burnedAmount, natsRevision) error  // idempotent
    GetFiatPurchased(ctx, entityID) (int64, error)
}
```

### 10.3 Rollup Mechanism (Two-Generals Idempotency)

```go
// tap/pkg/micrion/store.go:151-161
// Every 50 micro-burns, the system:
// 1. Synchronously flushes uncommitted burns to Postgres (LogBulkBurn)
// 2. Uses the NATS KV revision as an idempotency key
// 3. Resets the uncommitted counters for the next NATS CAS update
// This prevents double-charging even under concurrent CAS collisions.
```

### 10.4 Tollbooth Middleware

```go
// tap/pkg/micrion/middleware.go:11-44
func TollboothMiddleware(wm *WalletManager, toll int64) func(http.Handler) http.Handler {
    // Extracts X-Agent-DID header
    // Calls MicroBurn() to deduct toll
    // Returns 402 Payment Required if insufficient funds
    // Sets X-Micrion-Balance header on success
}
```

### 10.5 Stripe Integration (Fiat On-Ramp)

```go
// tap/pkg/micrion/wallet.go:39-51
func (m *WalletManager) HandleStripePurchase(ctx, entityID, agentDID, txID, usdAmount, micrionAmount) error {
    // 1. LogPurchase to Postgres fiat ledger
    // 2. TopUp the NATS KV execution layer
}
```

### 10.6 Key Distinction: Micrion vs. Contract Settlement

| System | What it does | Status |
|--------|-------------|--------|
| **Micrion** | Bills agents per Almanac query (API metering) | Implemented (KV + Postgres + Stripe) |
| **EscrowManager** | Locks/releases funds for contract settlement | Interface only, no implementation |
| **Contract.Settle()** | Transitions contract to SETTLED + reputation | Implemented, but no funds move |

These are **separate systems** — the Micrion tollbooth is for API access metering; the Escrow system is supposed to handle the actual contract payment settlement. They are not yet integrated.

---

## 11. Identity System

The Almanac and contract systems depend on Toro's W3C-compatible DID identity layer:

```go
// tap/pkg/core/primitives.go:149-179
type Identity struct {
    ID                    string                   `json:"id"`              // did:toro:<id>
    Controller            string                   `json:"controller"`      // Owning entity
    VerificationMethods   []VerificationMethod     `json:"verificationMethod"`
    VerifiableCredentials []VerifiableCredential   `json:"verifiableCredential,omitempty"`
    Services              []ServiceEndpoint        `json:"service,omitempty"`
    CapabilityVector      map[string]float64       `json:"capabilityVector,omitempty"`
    Created               time.Time                `json:"created"`
    Updated               time.Time                `json:"updated"`
    Version               int64                    `json:"version"`
    Status                string                   `json:"status"`          // active | revoked | deprecated
}
```

This is a full W3C DID implementation with:
- Ed25519 verification methods
- Verifiable credentials (W3C-compatible with `@context`, issuer, proof chains)
- Service endpoints for agent interaction
- Capability vectors for competency scoring
- Lifecycle management (version, status)

The identity system is used for:
1. **Registration:** Agents include a `CertificatePEM` in their Almanac registration
2. **Contract signing:** `Lock()` verifies both parties' signatures against their DIDs
3. **Proof verification:** `Settle()` verifies the worker's proof signature
4. **Reputation:** Outcomes are attributed to specific DIDs

---

## 12. File Inventory

### Core Implementation

| File | Lines | Role |
|------|-------|------|
| `tap/pkg/lookup/main.go` | 1-156 | Domain types, Client struct, all query methods |
| `tap/pkg/lookup/almanac_server.go` | 1-208 | Registry server, durable subscription, DLQ, cleanup |
| `tap/pkg/lookup/almanac_helpers.go` | 1-59 | Convenience wrappers (RegisterAlmanac, FindAgents, etc.) |
| `tap/pkg/lookup/almanac_helpers_test.go` | 1-105 | Tests for helpers |
| `tap/pkg/core/topics.go` | 1-117 | All NATS subject definitions, queue normalization |
| `tap/pkg/core/primitives.go` | 1-284 | Task, Contract, Proof, Identity structs; complexity/reward tiers |
| `tap/pkg/core/verbs.go` | — | FIPA performatives (CFP, PROPOSE, ACCEPT_PROPOSAL, etc.) |

### Contract & Incentive

| File | Lines | Role |
|------|-------|------|
| `tap/pkg/contract/main.go` | 1-165 | ContractMachine: Propose, Escrow (stub), Lock, Settle |
| `tap/pkg/incentive/escrow.go` | 1-20 | EscrowManager interface (no implementation) |
| `tap/pkg/incentive/auction.go` | 1-26 | AuctionManager interface (no implementation) |

### Payment (Micrion)

| File | Lines | Role |
|------|-------|------|
| `tap/pkg/micrion/wallet.go` | 1-61 | WalletManager, FiatLedger interface, Stripe integration |
| `tap/pkg/micrion/store.go` | 1-179 | NATS KV CAS operations, rollup logic |
| `tap/pkg/micrion/middleware.go` | 1-44 | HTTP tollbooth middleware (402 Payment Required) |
| `tap/pkg/micrion/interceptors.go` | — | Additional interceptors |

### Agent & Registration

| File | Lines | Role |
|------|-------|------|
| `tap/pkg/agent/base.go` | 191-258 | Start(): registration + subscription setup |
| `tap/pkg/agent/skill.go` | 1-82 | Use(): resolve DID via Almanac, send signed envelope |
| `tap/agents/simple_trucker.go` | 52-64 | Example: registers "logistics.trucking", responds to CFP |
| `tap/agents/ocr-agent.go` | 58-83 | Example: registers "perception.ocr" with metadata |
| `tap/agents/billing-agent.go` | 77, 135-147 | Example: registers "finance.billing", discovers OCR agents |
| `tap/agents/general_agent/agent.go` | 423-536 | AlmanacLookup + Delegate LLM tools |

### Orchestrator

| File | Lines | Role |
|------|-------|------|
| `tap/workflows/orchestrator.go` | 125-163 | resolveActorByCapability() — live NATS query to Almanac |
| `tap/workflows/orchestrator.go` | 842-957 | dispatchStep() — CFP broadcast vs direct dispatch |
| `tap/workflows/orchestrator.go` | 1049-1057 | PROPOSE handler (TODO stub) |

### API & Workers

| File | Lines | Role |
|------|-------|------|
| `go/internal/api/system_handler.go` | 1-134 | HandleListActors, discoverActorsForBlueprint |
| `go/internal/api/router.go` | 66 | GET /v1/system/actors |
| `go/internal/workers/general_agent_ingress_worker.go` | 245-263 | resolveAgentInbox() — Almanac query for general agent |

### Configuration & Schema

| File | Role |
|------|------|
| `go/internal/config/defaults.yml:64-71` | ALMANAC JetStream stream config |
| `tap/api/json-schema/v1/almanac.json` | Registration payload JSON schema |
| `tap/api/asyncapi.yml:8-14` | almanac.query channel spec |

### Tooling

| File | Role |
|------|------|
| `go/cmd/almanac-check/main.go` | CLI diagnostic tool |
| `tap/pkg/tools/orchestrator.go:18` | AlmanacLookup listed as concurrent-safe |
| `tap/pkg/tools/builtin/agent.go:13-14` | Specialized agent comment |

### Docs

| File | Role |
|------|------|
| `tap/docs/agent.md:30-33` | "Dynamic DNS and capabilities registry for the hive" |
| `docs/composability.md` | Architecture doc: durable discovery via JetStream |
| `docs/workflow.md` | Workflow integration with Almanac |
| `docs/tap_primitives_audit.md:5-9` | "Almanac: Built (Agent Registry)" |
| `tap/api/api.md:67-98` | API docs for registration schema |

---

## 13. Gap Analysis

| # | Gap | Location | Severity | Impact |
|---|-----|----------|----------|--------|
| 1 | **No EscrowManager implementation** | `incentive/escrow.go` | **HIGH** | Contract funds are never actually locked or released. The escrow state transition is a no-op. |
| 2 | **ContractMachine.Escrow() is a stub** | `contract/main.go:58-62` | **HIGH** | The escrow step doesn't call `EscrowMgr.Lock()`. Funds never leave the initiator's wallet. |
| 3 | **No funds release on Settle** | `contract/main.go:87-136` | **HIGH** | Settlement transitions state but never calls `EscrowMgr.Release()`. Agent is never paid. |
| 4 | **No AuctionManager implementation** | `incentive/auction.go` | **MEDIUM** | Bid validation and auction settlement are not implemented. The PROPOSE handler auto-accepts the first bid. |
| 5 | **PROPOSE handler is a TODO** | `orchestrator.go:1049-1057` | **MEDIUM** | Bid selection strategy (price, ETA, reputation) is not implemented. First proposal always wins. |
| 6 | **Registration signatures not verified** | `almanac_server.go:133-138` | **MEDIUM** | Any agent can register under any DID. No cryptographic proof of identity is required. |
| 7 | **General agent DID hardcoded** | `general_agent_ingress_worker.go:248` | **MEDIUM** | Capability type `"agents.general.purpose"` is hardcoded rather than configurable. |
| 8 | **No dispute resolution** | `dispute` package referenced but unused | **MEDIUM** | The `Dispute` engine is a dependency of `ContractMachine` but no dispute flow is implemented. |
| 9 | **No failure reputation** | `contract/main.go:117-132` | **LOW** | Only `SUCCESS` outcomes submitted to reputation. No `FAILURE`, `TIMEOUT`, or `DISPUTED` outcomes. |
| 10 | **Escrow and Micrion not integrated** | Cross-cutting | **MEDIUM** | The Micrion payment system (API metering) and the Escrow system (contract settlement) are separate with no shared ledger or fund transfer path. |
| 11 | **No persistent registry storage** | `almanac_server.go:28-30` | **LOW** | The registry is purely in-memory. A full NATS cluster restart loses all registrations (agents re-register within their TTL window, so this is a liveness gap, not a correctness gap). |

---

## 14. What's Production-Ready vs. What's Scaffolding

### Production-Ready (Working in Code)

- Agent registration with JetStream durability and DLQ protection
- Multi-path agent discovery (SDK, Orchestrator, LLM tools, REST API, CLI)
- FIPA CFP broadcast and PROPOSE/ACCEPT_PROPOSAL envelope routing
- Direct dispatch via Almanac resolution or explicit task queues
- Contract state machine (Propose → Lock → Settle) with Ed25519 signature verification
- W3C-compatible DID identity with verification methods and verifiable credentials
- Proof submission and validation (GPS, Classification, API)
- Reputation engine integration (SUCCESS outcomes)
- Micrion microtransaction system (NATS KV CAS + Postgres rollup + Stripe fiat on-ramp)
- Tollbooth middleware for per-query API billing (402 Payment Required)
- Constraint engine gate checks on contract proposal
- 3-replica JetStream stream for Almanac data
- REST API for actor listing and workflow visualization

### Scaffolding (Interfaces Defined, Not Implemented)

- EscrowManager (Lock/Release/Slash)
- AuctionManager (ValidateBid/SettleAuction)
- Bid selection strategy in PROPOSE handler
- Dispute resolution flow
- Registration signature verification
- Actual fund movement on contract settlement
- Escrow-Micrion integration (shared ledger)

---

## 15. Architectural Observations

### Strengths

1. **Clean separation of concerns** — The Almanac registry is a standalone service with a well-defined NATS interface. It doesn't know about contracts, escrow, or payments. Each concern has its own package.

2. **Protocol-native discovery** — The Orchestrator resolves actors at dispatch time via the Almanac, meaning workflows can be written against capability types rather than specific agent addresses. New agents can join the network and immediately receive work.

3. **LLM-composable agent chains** — The General Agent's `AlmanacLookup` + `Delegate` tools let the LLM dynamically discover and orchestrate specialized agents. This is a powerful primitive for emergent agent collaboration.

4. **Dual-layer accounting** — The Micrion system's hot-path (NATS KV CAS) + cold-path (Postgres rollup) architecture is a well-considered pattern for high-throughput micro-billing with eventual consistency guarantees and idempotency protection via NATS revision numbers.

5. **Durable registration** — Using JetStream durable consumers for `almanac.register` means registrations survive server restarts. The DLQ protects against poison messages corrupting the stream.

6. **W3C identity** — The DID/VC implementation provides a standards-compatible foundation for agent identity, key rotation, and third-party attestations.

### Areas for Investment

1. **Escrow implementation** is the critical path to a functioning agent economy. Without it, agents work on trust with no financial guarantee. The interface is clean and injectable — the implementation is the hard part (see below).

2. **Bid selection** needs a strategy. The current auto-accept-first approach works for a single-agent scenario but doesn't create a market. A proper auction mechanism (even a simple first-price sealed-bid) would demonstrate the economic model.

3. **Registration authentication** is important for production multi-tenant deployments. Without signature verification, any agent can impersonate any other agent in the registry.

4. **Persistent registry state** (Redis or Postgres backing) would improve cold-start recovery time after a full cluster restart, though the current in-memory + JetStream replay design is reasonable for now.

---

## 16. Path to a Functioning Agent Economy

The gap between the current system and a fully autonomous agent economy where agents discover each other, bid on work, receive escrowed payments, and get paid on proof of completion is well-understood:

1. **Implement `EscrowManager`** — A concrete implementation backed by the Micrion KV store (or a separate escrow KV bucket) that:
   - `Lock(contract, amount)` — debits the initiator's micrion balance into an escrow holding account
   - `Release(contractID)` — credits the acceptor's micrion balance from escrow
   - `Slash(contractID, penalty)` — burns or redistributes collateral

2. **Wire escrow into ContractMachine** — Call `EscrowMgr.Lock()` in `Escrow()` and `EscrowMgr.Release()` in `Settle()`.

3. **Implement `AuctionManager`** — A first-price sealed-bid auction that:
   - Validates bids against task reward bounds
   - Selects the winner (lowest price, with reputation weighting)
   - Settles the auction by returning the winner's DID and final price

4. **Complete the PROPOSE handler** — Replace the TODO stub with actual bid collection (with timeout), auction settlement, and ACCEPT_PROPOSAL/REJECT_PROPOSAL dispatch.

5. **Add dispute flow** — Timeout-based auto-settlement for unresponsive agents, with slash penalties.

6. **Add failure reputation** — Submit FAILURE/TIMEOUT outcomes to the reputation engine.

The architecture is well-factored for this — each step is a discrete, injectable interface with the plumbing already in place.
