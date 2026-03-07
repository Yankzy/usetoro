## QBO Rule Engine Audit (CTO-ready)

- **Audited component**: `go/qbo/rule_engine.go` (package `quickbooks`)
- **Primary upstream**: `go/internal/services/accounting/rule_engine_service.go` → `go/internal/services/accounting/transaction_service.go`
- **Primary downstream**: `sql/schema/007_rule_engine.sql`, `sql/schema/008_rule_audit_logs.sql`, `sql/queries/rule_engine.sql` (sqlc: `go/internal/database/rule_engine.sql.go`)
- **Audit date**: 2026-02-27

### Executive summary

- **What works well**
  - **Deterministic, explainable evaluation**: every condition (and nested child group) produces a structured trace (`MatchExplanation`) that can be serialized.
  - **Safe regex engine**: Go `regexp` is RE2 (no catastrophic backtracking), making user-supplied regex comparatively safe from ReDoS.
  - **Reasonable performance posture**: pre-compilation (`Compile()`), candidate filtering (`FilterCandidates()`), and service-level caching (Ristretto, 5m TTL) are the right ingredients for low-latency routing.

- **What is high-risk / currently broken**
  - **P0: Audit logging cannot succeed in the current integration**: `shadow_erp.rule_audit_logs.transaction_id` is `NOT NULL` and references `shadow_erp.proposed_transactions(id)` (`sql/schema/008_rule_audit_logs.sql`), but the only caller (`TransactionService`) evaluates rules *before* creating a `proposed_transactions` row and does not set `quickbooks.Transaction.ID`. `RuleGroup.Evaluate()` therefore inserts a NULL `transaction_id`, the insert fails, and the error is silently swallowed (stdout warning only).
  - **P0: Candidate filtering can create false negatives for nested rules**: `DeriveKeywords()` intentionally does **not** recurse into children. If a top-level group has keywords that don’t intersect the transaction, but a child group would match, the entire tree can be excluded by `FilterCandidates()` and never evaluated.

### System context (how it is used in production)

#### Upstream call path (today)

- `TransactionService.postExpense()` builds a `quickbooks.Transaction` (realm, description, amount, date/time, vendor/customer/memo/mcc/invoice_text) and calls:
  - `RuleEngineService.EvaluateTransaction(ctx, tx)`
    - Loads + caches rules per realm (`getActiveRules`)
    - Runs `quickbooks.FilterCandidates(tx, rules)`
    - Iterates candidates in DB order (priority asc, id asc) and calls `rule.Evaluate(ctx, tx, s.q)`
    - If matched, it re-queries rule groups to extract `target_account_id` / `target_vendor_id` and returns them so `TransactionService` can bypass AI.

#### Downstream state + side effects

- Rules live in DB:
  - `shadow_erp.rule_groups` (tree via `parent_id`, includes `keywords`, includes mapping targets `target_account_id` and `target_vendor_id`)
  - `shadow_erp.rule_conditions` (field/operator/value)
- Audit logs are intended to land in:
  - `shadow_erp.rule_audit_logs` (JSONB trace + human readable reason)

### Code-level audit of `go/qbo/rule_engine.go`

#### Core semantics (correctness)

- **Group evaluation**:
  - A `RuleGroup` aggregates: its own conditions + each active child group result.
  - `AND`: all results must be true; `OR`: any result true.
  - **No short-circuit**: it evaluates all conditions and all active children to produce a complete explanation trace (good for auditability; costs CPU).

- **String operators**:
  - `contains` / `not_contains` / `contains_cs` implement **whole-token matching**, not substring matching. Tokenization is “split on non-alphanumeric”.
  - This is a deliberate precision/recall trade-off (reduces accidental matches; surprises users expecting substring semantics).

- **Numeric (`amount`)**:
  - `equals` uses an epsilon (\(\pm 1e{-5}\)), which is generally sensible for float comparisons but can be surprising for finance logic.
  - `in` / `not_in` compares **stringified float keys**, not numeric equality.

- **Date (`date`) / Time (`time`)**:
  - Date comparisons normalize transaction dates to **UTC midnight**; time comparisons normalize to **UTC H:M:S** anchored to a zero date.
  - This makes evaluation stable across time zones *for a given instant*, but means “local calendar day” rules can fail near midnight (expected, but must be explicit in product semantics).

- **Operator/field compatibility is not enforced**:
  - Example: a condition with `field=amount` and `operator=regex` can compile a regex but will evaluate via `evalNumeric`, which ignores `regex` and returns false.
  - `Validate()` currently checks circularity and “can compile”, but not “is meaningful”.

#### Candidate selection (“optimization layer”)

- `FilterCandidates()` creates a token set from a subset of transaction fields and intersects it with `RuleGroup.Keywords`.
- **False-negative risk**:
  - Keywords are **group-local** (non-recursive); a parent with keywords can exclude child-only matches.
- **Field coverage mismatch**:
  - `Transaction` supports `role` and `uuid`, and `DeriveKeywords()` will emit tokens for those fields, but `extractTxTokens()` does **not** include `Role` or `UUID`. Any rule depending on those tokens will be filtered out unless `Keywords` is empty.

#### Audit-log persistence (`RuleGroup.Evaluate`)

- Persistence is attempted when:
  - `queries != nil` and `g.Parent == nil` (top-level only)
- Current behavior issues:
  - **Uses `context.Background()`** for the insert, so request cancellation/timeouts are ignored (and it’s still synchronous).
  - **Logging on failure is `fmt.Printf`** (stdout, unstructured).
  - For non-matches, it sets `rule_group_id` to NULL (even though the evaluated group is known), which makes “why didn’t it match?” auditing much less useful if you ever log negatives.
  - **Most importantly**: integration currently passes an empty `Transaction.ID`, but DB requires `transaction_id NOT NULL` → inserts fail.

### Database + service integration audit

#### Rule loading and compilation (`RuleEngineService.getActiveRules`)

- **Tree building** is correct and sets parent pointers.
- **Validation/Compile failures are logged but not excluded**:
  - The service continues without removing invalid rules from `topLevelRules`, and still caches the entire slice.
  - Because `Evaluate()` has a “lazy compile safety” that ignores compile errors, invalid rules can silently degrade into “always false” conditions.
  - If any uncompiled condition is hit concurrently across requests, the lazy compile path introduces potential **data races** (it mutates shared cached structs).

#### Target mapping retrieval (`EvaluateTransaction`)

- On match, it **re-queries** `GetActiveRuleGroupsByRealm` to retrieve `target_account_id` / `target_vendor_id`.
  - This is an avoidable DB hit: those targets are already in the original `dbGroups` slice used to build the cached rule tree, but they are not stored in the in-memory structure.

#### Audit-log volume / intent mismatch

- `EvaluateTransaction()` passes `s.q` into `rule.Evaluate(...)` for **every candidate**, meaning it intends to log every candidate evaluation.
- But `RuleGroup.Evaluate()` only sets `rule_group_id` when matched, so “per-candidate negative logs” would be mostly unidentifiable.
- Combined with the `transaction_id` issue, the current implementation likely emits repeated stdout warnings under load instead of producing durable audit logs.

### Test posture

- `go/qbo/rule_engine_audit_test.go` is extensive and covers:
  - token/whole-word semantics, memo + new string fields, date/time timezone normalization, numeric set membership, float audit formatting, recursion/child behavior, and validation errors.
- **Gap**: there is no integration-level test proving that rule evaluation actually produces a durable row in `shadow_erp.rule_audit_logs` (and in production it currently cannot, due to the NOT NULL FK).

### Prioritized recommendations

#### P0 (must fix before relying on auditability)

- **Fix audit-log foreign key and timing**
  - Either:
    - **Move rule evaluation after `proposed_transactions` creation** and set `quickbooks.Transaction.ID` to that UUID, or
    - Change schema to allow nullable `transaction_id` and/or reference a different transaction identity that exists at evaluation time, or
    - Add a two-step mechanism: evaluate → later attach audit record to the created proposed transaction.
- **Stop using `context.Background()` for inserts**
  - Use the caller’s `ctx` for bounded latency and cancellation safety (or make the insert explicitly async with a bounded queue + background worker).

#### P0 (correctness: prevent false negatives)

- **Make candidate filtering descendant-safe**
  - Compute `keywords` as a **union over the full subtree** (parent + all children) so `FilterCandidates()` can’t exclude a child-only match.
  - If you keep non-recursive keywords, you must guarantee top-level keywords stay empty when children introduce new match tokens (hard to enforce operationally).
- **Align token extraction with supported fields**
  - Either include `Role` and `UUID` in `extractTxTokens()` or remove them from supported keyword derivation + product surface area (avoid “rules that never run”).

#### P1 (operational stability + performance)

- **Exclude invalid rules from the cached slice**
  - If `Validate()`/`Compile()` fails for a rule tree, remove it from `topLevelRules` before caching to avoid repeated work and data-race potential from lazy compilation.
- **Eliminate the extra DB query on match**
  - Store `target_account_id` / `target_vendor_id` in the in-memory representation used by the engine service, or maintain a `map[groupID]targets` alongside the cached rules.
- **Make audit logging intent explicit**
  - Decide between:
    - “Log only the winning match (or a single no-match record)” vs
    - “Log every candidate evaluation”
  - Then implement consistently (including `rule_group_id` population for negatives if you want negative logs).

#### P2 (semantics + maintainability)

- **Enforce operator/field compatibility at validation time**
  - Prevent “compiles but can never match” rule conditions.
- **Normalize numeric set membership**
  - Avoid float-to-string key comparisons for `in` / `not_in` on `amount` (prefer parsing JSON numbers into float64 and comparing within epsilon, or represent amounts as integer cents if product semantics allow).
- **Pre-split keywords for faster filtering**
  - If rule sets grow, avoid `strings.Fields(ruleKw)` per rule per transaction by caching a parsed keyword slice/set.

### Quick “what to tell the CTO”

- The core rule engine logic is solid and well-tested for evaluation semantics, but the current system **does not actually produce durable audit logs** as wired, due to a schema/integration mismatch around `transaction_id`.
- Candidate filtering needs one design change (subtree keyword union, plus role/uuid token coverage) to avoid correctness regressions as rules become more complex/nested.
- The service layer has a few easy performance wins (avoid re-query on match, drop invalid rules from cache) and a logging hygiene fix (no stdout, no background context for DB writes).