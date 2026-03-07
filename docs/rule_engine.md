# Rule Group

Rule groups are the core unit of the **rule engine**: they define how bank/transaction data is matched to QuickBooks targets (accounts, vendors). When a transaction matches a rule group, the engine returns that group’s mapping so the system can categorize without calling AI or a CPA.

**Code:** `go/qbo/rule_engine.go`  
**Service:** `go/internal/services/accounting/rule_engine_service.go` (loads rules from DB, caches, runs engine)

---

## Overview

- A **rule group** has a name, **AND** or **OR** logic, optional **priority**, and can be **nested** (child groups).
- Each group has **conditions**: “field **operator** value” (e.g. `Vendor contains "Uber"`, `Amount > 50`).
- The engine **filters candidates** by keywords (from rules vs transaction text), then **evaluates** each candidate; the first matching group wins.
- **Keywords** are auto-derived from string conditions and used only for the candidate filter (exact-token match). Rules with no keywords are catch-all (always evaluated).

---

## Data Model

### RuleGroup

| Property    | Type          | Description |
|------------|---------------|-------------|
| `ID`       | int           | Primary key. |
| `Name`     | string        | Display name (e.g. "Software Subscriptions"). |
| `Logic`    | `AND` \| `OR` | How to combine conditions and child groups. |
| `Priority` | int           | Optional; used when ordering/choosing among matches. |
| `Keywords` | string        | Auto-generated tokens for candidate filter (space-separated). |
| `Active`   | bool          | If false, group is skipped. |
| `Conditions` | []*RuleCondition | Conditions on transaction fields. |
| `Children` | []*RuleGroup  | Nested groups (same logic applies recursively). |
| `Parent`   | *RuleGroup    | Set when building the tree; used for circular-dependency checks. |

### RuleCondition

| Property   | Type     | Description |
|-----------|----------|-------------|
| `ID`      | int      | Primary key. |
| `Field`   | Field    | Which transaction field to test. |
| `Operator`| Operator | Comparison type. |
| `Value`   | string   | Literal or JSON array (for `in` / `not_in`). |

---

## Fields

Conditions are evaluated against these **transaction** fields:

| Field           | Type    | Example use |
|----------------|---------|-------------|
| `vendor`       | string  | Payee name (e.g. "AMAZON WEB SERVICES"). |
| `customer`     | string  | Customer name on the transaction. |
| `description`  | string  | Line-item or bank description. |
| `category`     | string  | Bank-assigned category. |
| `memo`         | string  | User or system memo. |
| `invoice_text` | string  | Extracted invoice or receipt text. |
| `mcc`          | string  | Merchant Category Code. |
| `role`         | string  | Account role identifier. |
| `uuid`         | string  | Account UUID. |
| `amount`       | float64 | Transaction amount. |
| `date`         | date    | Transaction date (evaluated in UTC). |
| `time`         | time    | Time-of-day component (H/M/S, evaluated in UTC). |

---

## Operators

### String (case-insensitive unless marked)

| Operator      | Value format | Description |
|---------------|--------------|-------------|
| `equals`      | string       | Exact match, case-insensitive. |
| `equals_cs`   | string       | Exact match, case-sensitive. |
| `contains`    | string       | **Whole-word** match: value must appear as a full token (e.g. "Uber" matches "UBER EATS"; "ube" does not match "Uber Eats"). |
| `not_contains`| string       | Whole-word NOT match: passes if value does **not** appear as a full token. |
| `contains_cs` | string       | Whole-word match, case-sensitive. |
| `startswith`  | string       | Prefix match, case-insensitive. |
| `endswith`    | string       | Suffix match, case-insensitive. |
| `in`          | JSON array   | Field value (normalized) must be in the list, e.g. `["Uber", "Lyft"]`. |
| `not_in`      | JSON array   | Field value must not be in the list. |
| `regex`       | pattern      | Go regex; matched against raw string. |
| `is_null`     | —            | Field is empty. |
| `is_not_null` | —            | Field is non-empty. |

### Numeric (`amount`)

| Operator | Value format   | Description |
|----------|----------------|-------------|
| `gt`, `gte`, `lt`, `lte` | number  | Strict comparison. |
| `equals` | number         | Equality with small epsilon (~1e-5). |
| `in`     | JSON array     | e.g. `[100.50, 50.00]`. |
| `not_in` | JSON array     | Amount not in list. |

### Date (`date`)

| Operator | Value format   | Description |
|----------|----------------|-------------|
| `gt`, `gte`, `lt`, `lte` | `YYYY-MM-DD` | Comparison in UTC. |
| `equals` | `YYYY-MM-DD`   | Same calendar day in UTC. |
| `in`    | JSON array      | e.g. `["2023-01-01", "2023-01-02"]`. |
| `not_in`| JSON array      | Date not in list. |

**Date semantics:** Transaction dates are normalized to **UTC midnight** before comparison so timezone does not change the result.

### Time (`time`)

| Operator | Value format   | Description |
|----------|----------------|-------------|
| `gt`, `gte`, `lt`, `lte` | `HH:MM` or `HH:MM:SS` | Comparison in UTC. |
| `equals` | `HH:MM` or `HH:MM:SS` | Same time-of-day in UTC. |

**Time semantics:** Only the H/M/S components are compared; the date portion is ignored. Values are normalized to UTC before comparison.

---

## Logic: AND vs OR

- **AND:** The group matches only if **every** condition and **every** active child group matches.
- **OR:** The group matches if **any** condition or **any** active child group matches.

Conditions and children are combined by the same logic. Inactive children are not evaluated and do not appear in the match explanation.

---

## Nested groups (children)

- A rule group can have **child** rule groups (`Children`).
- Each child is evaluated like a top-level group; its result is a single true/false for the parent.
- The parent combines condition results and child results with its `Logic` (AND/OR).
- **Circular dependency** is forbidden: `Validate()` walks `Parent` and fails if the same group ID is seen again.

---

## Pipeline (how a transaction is matched)

1. **Load** active rule groups for the realm (from DB or cache).
2. **Compile** each group: compile conditions (regex, numeric/date parsing, set for `in`/`not_in`). Invalid conditions cause compile/validate to fail for that group.
3. **Filter candidates:**
   - Extract **transaction tokens**: from `vendor`, `customer`, `description`, `category`, `memo`, `mcc`, `invoice_text` (split on non-alphanumeric, lowercase, tokens length ≥ 2).
   - For each rule: if `Keywords` is empty → include (catch-all). Else include only if at least one keyword appears as an **exact token** in the transaction.
4. **Evaluate** each candidate in order:
   - Evaluate all conditions and active children.
   - Combine with the group’s AND/OR logic.
   - If the group matches, return it (first match wins).
5. If no group matches, the service returns “no match” (AI/CPA can then categorize).

---

## Keywords (candidate filter)

- **DeriveKeywords** builds a space-separated list of tokens from:
  - String conditions with `contains`, `equals`, `startswith`, or `endswith`: tokens from `Value` (length ≥ 2).
  - String conditions with `in` or `not_in`: tokens from each element of the JSON array.
  - Applies to string fields: `vendor`, `customer`, `description`, `category`, `memo`, `role`, `uuid`, `mcc`, `invoice_text`.
- Only **exact token** match between rule keywords and transaction tokens is used. Substring or partial-word match is **not** used (by design: avoids mis-categorization).
- Rules with no keywords are treated as **catch-all** and run for every transaction.

---

## Validation and compilation

- **Validate():**
  - Detects circular dependency via parent chain.
  - Compiles each condition (regex, numeric, date, `in`/`not_in`); invalid values return an error.
  - Recurses into children.
- **Compile():** Compiles all conditions and children so the group is ready for evaluation.
- Invalid regex or invalid JSON for `in`/`not_in` causes compile to fail; such groups are typically skipped at load time (e.g. logged and not cached).

---

## Audit and match explanation

When a rule group is evaluated with a non-nil DB interface (e.g. from the service), a row can be written to **shadow_erp.rule_audit_logs** with:

- Match result (matched or not).
- **Match explanation:** which conditions and child groups ran and whether they passed.
- **Human-readable reason:** e.g. “Categorized by Rule: Software. Because the description contained 'aws' and the amount was greater than $50.”

Only **top-level** evaluations are persisted; child evaluations are included in the explanation but not as separate audit rows.

---

## Summary

| Topic | Detail |
|-------|--------|
| **Purpose** | Deterministic, keyword-optimized matching of transactions to QBO rule groups (account/vendor mapping). |
| **Structure** | Rule groups with AND/OR logic, conditions (field + operator + value), optional nested children. |
| **Fields** | vendor, customer, description, category, memo, invoice_text, mcc, role, uuid (string); amount (numeric); date (UTC date); time (UTC time-of-day). |
| **Contains / Not Contains** | Whole-word only; no substring match. |
| **Dates** | Compared in UTC (midnight-normalized). |
| **Times** | H/M/S compared in UTC; date portion ignored. |
| **Failure mode** | No match → fall back to AI/CPA; invalid rules fail compile/validate and are skipped. |
