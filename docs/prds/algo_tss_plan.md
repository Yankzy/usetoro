# Temporal Semantic Stitching (TSS) Engine Implementation Plan

This document outlines the strategy for implementing the TSS engine inside the Autonomous Semantic Engine (ASE) architecture.

## Background Context

TSS handles time-delayed data matching. It maintains a sliding 7-day window of incoming context fragments (e.g., Slack messages, email receipts) in Redis. When a bank settlement event arrives, TSS computes a Spatio-Temporal Affinity Score to match the context with the settlement without invoking costly LLMs. The equation is:

$$ A(e_a, e_c) = \cos(\theta) \cdot e^{-\lambda \Delta t} $$

where $\lambda = 0.05$ and $\Delta t$ is the absolute time distance in hours.

## User Review Required

> [!WARNING]
> Please review the architectural assumptions before we proceed with the implementation.
> The PRD specifies that the active graph matrix must be hosted entirely within RAM via optimized Redis hashes and the cosine similarity must be executed within 15ms using **native Go routines**. Therefore, I plan to compute the cosine similarity directly in Go (using the `[]float32` vectors stored in Redis) rather than executing `pgvector` SQL queries on every match evaluation.

## Open Questions

> [!IMPORTANT]
> 1. **Redis Key Prefix**: Are there any existing Redis key prefixes or namespace conventions for `usetoro` we should follow, or should we use `tss:tenant:<tenant_id>:contexts`?
> 2. **ASE Integration Point**: The TSS PRD mentions that TSS is called by the ASE when an anchor event hits. Should the `tss.Engine` be instantiated inside `go/internal/erp/ase/node.go`, or at the `store.go`/worker level?

## Proposed Changes

### `go/internal/erp/ase` (Existing Package)

To support hot-reloading of tuning parameters, we will update the `ASEConfig` struct in `config.go` to include a `TSS` block mapping to `ase.yml`.

#### [MODIFY] [config.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/ase/config.go)
Add `TSSConfig` struct to `ASEConfig`:
```go
type TSSConfig struct {
    SlidingWindowHours float64 `mapstructure:"sliding_window_hours"` // default: 168
    AffinityThreshold  float64 `mapstructure:"affinity_threshold"`   // default: 0.88
    CollapseThreshold  float64 `mapstructure:"collapse_threshold"`   // default: 0.94
    DecayLambda        float64 `mapstructure:"decay_lambda"`         // default: 0.05
    CollisionDelta     float64 `mapstructure:"collision_delta"`      // default: 0.05
}
```

### `go/internal/erp/tss` (New Package)

This entirely new package will encapsulate the TSS algorithms, structures, and memory-cache operations.

#### [NEW] [models.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/tss/models.go)
- Define `FloatingContextNode` containing EventID, TenantID, SourceChannel, PayloadVector (`[]float32`), Timestamp, and RawContent.
- Define `SettlementAnchorEvent` containing TransactionID, TenantID, Amount, BankLabel, PayloadVector (`[]float32`), and Timestamp.
- Define statuses such as `STATUS_COLLISION_LOCKED`.

#### [NEW] [math.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/tss/math.go)
- Implement `CosineSimilarity(a, b []float32) float64`.
- Implement `TemporalAffinity(a, b []float32, tA, tB time.Time) float64` based on the PRD equation: $\cos(\theta) \cdot e^{-0.05 \cdot \Delta t}$.

#### [NEW] [cache.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/tss/cache.go)
- Implement a `RedisCache` struct wrapping the Redis client.
- Provide functions to add a `FloatingContextNode` to Redis with a 7-day (168 hours) TTL.
- Provide functions to retrieve all floating nodes for a specific `TenantID`.
- Provide functions to remove nodes once successfully stitched.

#### [NEW] [engine.go](file:///Users/Yankz/programming/usetoro/go/internal/erp/tss/engine.go)
- Implement `TSSEngine` handling the core workflow:
    - **Match Evaluation**: Scan through a tenant's active context nodes, calculate temporal affinities.
    - **Thresholds**: Enforce the $\ge 0.88$ minimum match guardrail.
    - **Collision Management**: Detect if the top two matches are within $\le 0.05$ delta and return `STATUS_COLLISION_LOCKED`.
    - **State Collapse**: Return the highest matching node if above thresholds and no collision detected.

## Verification Plan

### Automated Tests
- Create `math_test.go` to strictly verify the exponential decay of the temporal affinity score given known timestamps and vector arrays.
- Create `engine_test.go` with mocked Redis data to ensure collisions are appropriately locked and unambiguous high-affinity nodes are successfully returned.

### Manual Verification
- Review Go logic confirming the mathematical constraints map 1-to-1 with the PRD (especially the 50% decay over 14 hours logic, which corresponds to $e^{-0.05 \times 14} \approx 0.496$).
