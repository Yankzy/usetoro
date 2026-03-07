# QBO Rule Engine Documentation

The Rule Engine provides a fast, deterministic, and hierarchical system to evaluate transactions against user-defined conditions (e.g., string matching, regex, amount thresholds). It is designed to evaluate logic securely and rapidly without relying on slower, non-deterministic AI models.

## Overview

At its core, the Rule Engine evaluates a `quickbooks.Transaction` against a tree of `RuleGroup` and `RuleCondition` objects. If a transaction matches the logic of a group (and its parents), the engine yields the associated mapping targets (`TargetAccountID`, `TargetVendorID`), which are then used to shortcut AI routing.

## Core Components

The core logic resides in `go/qbo/rule_engine.go`.

1. **`RuleGroup`**: Represents a logical grouping of conditions (e.g., `AND` / `OR`). Groups can be nested (using `ParentID`) to form hierarchical rules. Each group maps to a potential outcome via `TargetAccountID` and `TargetVendorID`.
2. **`RuleCondition`**: Represents a single boolean evaluation. Supported fields include Description, Vendor, Category, Amount, Date, and Memo. Supported operators include String matching (`equals`, `contains`, `startswith`, `regex`), Numeric comparisons (`gt`, `lt`), and set logic (`in`, `not_in`).
3. **`MatchExplanation`**: When a rule executes, it produces a detailed JSON trace of every condition evaluated. This trace is persisted for audit logging and debugging.

## Database Schema

The rules and their execution logs live inside the `shadow_erp` schema, binding them directly to the multi-tenant architecture (`realm_id`).

- **`shadow_erp.rule_groups`**: Stores metadata, tree hierarchy (`parent_id`), and mapping targets (`target_account_id`, `target_vendor_id`).
- **`shadow_erp.rule_conditions`**: The specific boolean condition tied to a `rule_group_id`.
- **`shadow_erp.rule_audit_logs`**: Whenever a transaction is fully evaluated and matched at the top level, an audit log is saved here referencing the `transaction_id`, `rule_group_id`, and the JSON `match_info` tree.

## Integration & Usage in `cmd/sync`

The Rule Engine is heavily integrated into the `TransactionService` to optimize expense generation.

### 1. `RuleEngineService`
Located at `internal/services/accounting/rule_engine_service.go`, this service acts as the orchestration layer between the database and the raw QBO structs.
- **Caching**: It uses `ristretto.Cache` to hold fully-compiled tree structures of `RuleGroup`s in memory (indexed by `realm_id`).
- **Compilation**: Fetches rules from the database, builds the parent-child relationships, compiles Regex and Numeric types, and caches them for fast repeated access.
- **Evaluation**: Exposes `EvaluateTransaction(ctx, tx)`, filtering rules optimally by "Keywords" (derived tokens) before executing the full logic tree.

### 2. `TransactionService` Bypassing AI
Located at `internal/services/accounting/transaction_service.go`.
- Before a given `ExpenseInput` falls back to the expensive, high-latency `EntityResolver` or `CoAMapper`, the `RuleEngineService` evaluates it.
- **Deterministic Override**: If the Rule Engine finds a matching `RuleGroup`, it extracts the `TargetAccountID` and `TargetVendorID`, resolving them into their respective string `QboID`s via the database.
- It then assigns these IDs directly onto `input.AccountHint` and `input.VendorHint`.
- **Latency Optimization**: Because the hints are now explicitly provided by the deterministic rules, the system skips all AI resolution logic, generating the `Purchase` or `Bill` instantly. 

### Audit Logging
Upon an evaluation match inside `TransactionService`, `EvaluateTransaction()` automatically saves the full execution trace to `shadow_erp.rule_audit_logs`. This ensures that every automated override can be audited backward to see exactly *which* condition forced the categorization.
