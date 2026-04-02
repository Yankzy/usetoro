## QBO Rule Engine Audit (CTO-ready)

- **Audited component**: `go/qbo/rule_engine.go` (package `quickbooks`)
- **Primary upstream**: `go/internal/services/accounting/rule_engine_service.go` → `go/internal/services/accounting/transaction_service.go`
- **Primary downstream**: `sql/schema/007_rule_engine.sql`, `sql/schema/008_rule_audit_logs.sql`, `sql/queries/rule_engine.sql` (sqlc: `go/internal/database/rule_engine.sql.go`)
- **Initial Audit date**: 2026-02-27
- **Re-Audit date**: 2026-04-02
- **Status**: **Fully Remediated**. All findings from the original audit have been addressed.

### Executive summary (Updated)

- **What works well (Unchanged)**
  - **Deterministic, explainable evaluation**: every condition (and nested child group) produces a structured trace (`MatchExplanation`) that can be serialized.
  - **Safe regex engine**: Go `regexp` is RE2 (no catastrophic backtracking), making user-supplied regex comparatively safe from ReDoS.
  - **Reasonable performance posture**: pre-compilation (`Compile()`), candidate filtering (`FilterCandidates()`), and service-level caching (Ristretto, 5m TTL) are the right ingredients for low-latency routing.

- **Resolved Risks (Previously High-Risk / Broken)**
  - **[RESOLVED] Audit logging foreign key failure**: `RuleGroup.Evaluate()` no longer attempts database persistence. `EvaluateTransaction()` returns the `MatchExplanation` and targets, and a separate `PersistAuditLog()` method on `RuleEngineService` safely handles persistence using the provided context *after* the `proposed_transactions` row is created.
  - **[RESOLVED] False negatives for nested rules**: `DeriveKeywords()` now properly recurses into and unions child keywords, ensuring descendant-safe filtering so parent rules aren't excluded when only a child condition would match.

### Code-level audit of `go/qbo/rule_engine.go`

*(Historical context: Semantics, such as date/time logic and string operators, remain unchanged and work as intended.)*

#### Core semantics (Correctness)
- **Operator/field compatibility is now enforced**: `Validate()` implements `validateCompatibility()`, which checks a rigid `validOperators` matrix. Rules that "compile but are not meaningful" are rejected upfront instead of silently degrading.
- **Normalized numeric set membership**: Float sets using `in` / `not_in` for amounts are no longer evaluated as stringified parameters; they compile into `[]float64` sets and use an epsilon (`numericEpsilon = 1e-5`) comparison exactly like the standard `equals` numeric evaluation.

#### Candidate selection (“optimization layer”)
- **Field coverage matched**: `extractTxTokens()` now captures all string fields used by conditions, including `Role` and `UUID`.
- **Pre-split keywords**: Keywords are pre-split during `Compile()` and cached in a `keywordSet` `map[string]struct{}`, vastly improving evaluation speed by skipping per-transaction tokenization (`strings.Fields`) in `FilterCandidates`.

### Database + service integration audit (`RuleEngineService`)

#### Rule loading and compilation
- **Invalid rules are excluded**: `getActiveRules()` calls `Validate()` and `Compile()` *before* caching. Any failing rules are skipped entirely. This prevents lazy compilation data races and unexpected "always false" degradation.

#### Target mapping retrieval
- **Zero-DB query overhead on match**: The initial model required re-querying groups for target IDs. `TargetAccountID` and `TargetVendorID` are now cached in memory on the `RuleGroup` struct, making target extraction entirely allocations-based instead of database-dependent.

#### Audit-log intent clarified
- Explicitly restricted to storing only the final evaluated outcomes via `PersistAuditLog()`, eliminating the noise of negative-log candidates and missing transaction IDs.

### Prioritized recommendations (Remediation Status)

#### P0 (must fix before relying on auditability)
- [x] **Fix audit-log foreign key and timing** - Service layer controls persistence after transaction creation.
- [x] **Stop using `context.Background()` for inserts** - Handled by the service explicitly accepting a `context.Context`.

#### P0 (correctness: prevent false negatives)
- [x] **Make candidate filtering descendant-safe** - Parent nodes form a keyword union of the full subtree.
- [x] **Align token extraction with supported fields** - Handled natively in extraction.

#### P1 (operational stability + performance)
- [x] **Exclude invalid rules from the cached slice** - Validation is front-loaded before memory storage.
- [x] **Eliminate the extra DB query on match** - Cached on rule structs.
- [x] **Make audit logging intent explicit** - Handled via `PersistAuditLog`.

#### P2 (semantics + maintainability)
- [x] **Enforce operator/field compatibility at validation time**
- [x] **Normalize numeric set membership**
- [x] **Pre-split keywords for faster filtering**

### Quick “what to tell the CTO” (Updated)

- The rule evaluation engine is structurally sound and **has successfully mitigated all integration and correctness bugs** from the previous audit.
- Durable audit logging works precisely as intended since `transaction_id` allocation is managed properly by the calling service.
- The path from DB-to-Cache is resilient, actively rejecting invalid rules to prevent corruption in shared memory states.
- Performance is unblocked: candidate filtering handles nested logic correctly, skips unnecessary string tokenizations, and target mapping queries zero the database mid-flight.