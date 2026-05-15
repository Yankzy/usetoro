# Toro ERP Rule Engine

The Toro ERP Rule Engine is a high-performance, deterministic evaluation engine designed for automatic categorization, splitting, and allocation of transactions based on user-defined logic.

## Overview

At its core, the Rule Engine evaluates a `Transaction` against a hierarchical tree of `RuleGroup` and `RuleCondition` objects. It is used to match transactions to specific vendors, target accounts, or allocations (splits), reducing manual bookkeeping effort.

## Core Concepts & Data Structures

### 1. `Transaction`
The inputs to the engine. Transactions provide string, numeric, and temporal fields that rules can test against:
- **String Fields**: `Vendor`, `Customer`, `Description`, `Category`, `Memo`, `Role`, `UUID`, `MCC`, `InvoiceText`, `SourceAccount`
- **Numeric Fields**: `Amount` (float64)
- **Temporal Fields**: `Date`, `Time`
- **Control Flags**: `Direction` (`INFLOW` or `OUTFLOW`). Inflow transactions are strictly isolated from Outflow rules.

### 2. `RuleGroup`
The `RuleGroup` represents a grouping of conditions or child rule groups. It acts as the routing payload if it produces a match.

**Key Attributes:**
- `Logic`: Determines how conditions and children are evaluated (`AND` or `OR`).
- `Active`: Indicates whether the rule is currently enabled.
- `Direction`: A strict cash direction boundary (`INFLOW` / `OUTFLOW`).
- `TargetEntityID`: The `pgtype.UUID` of the vendor or customer the transaction maps to.
- `RequiresReview`: Flag to park matched transactions in the UI for CPA manual review.
- `Allocations`: A slice of `Allocation` structs detailing how the transaction amount should be distributed among various accounts (e.g., standard 100% or splits like 50/50).

**Nesting:**
A `RuleGroup` can contain a list of `Conditions` and a list of `Children` (nested `RuleGroup`s). This allows infinitely complex boolean structures, such as:
```text
(Condition: Amount > 500 AND Condition: Category equals "Meals")
OR
(Child Group: Logic AND
  -> Condition: Vendor contains "Delta"
  -> Condition: Memo contains "Flight")
```

### 3. `RuleCondition`
A `RuleCondition` is a specific rule check applied to a single transaction field.

**Key Attributes:**
- `Field`: The field to test (e.g., `amount`, `description`).
- `Operator`: The logical comparison operator (e.g., `equals`, `gt`, `contains`).
- `Value`: The stringified target value.
- **Compiled State**: When a rule is compiled, dates, regexes, and JSON arrays are parsed into fast-access memory structures to avoid allocation during evaluation.

## Operators & Field Compatibility

To prevent logic errors, the engine enforces strict operator compatibility.

**String Fields** (`description`, `vendor`, `customer`, `category`, `memo`, `role`, `uuid`, `mcc`, `invoice_text`, `source_account`):
- `equals`, `equals_cs` (case-sensitive)
- `contains`, `contains_cs` (Whole word token match)
- `not_contains`
- `startswith`, `endswith`
- `in`, `not_in` (Value is a JSON array string `["A", "B"]`)
- `is_null`, `is_not_null`
- `regex`

**Numeric Fields** (`amount`):
- `equals`, `gt`, `gte`, `lt`, `lte`
- `in`, `not_in`
- `is_null`, `is_not_null`
*Note: Numeric evaluations use float-epsilon comparisons (`1e-5`) for safety against floating point math variance.*

**Temporal Fields** (`date`, `time`):
- `equals`, `gt`, `gte`, `lt`, `lte`
- `in`, `not_in` (For Dates)

## Engine Evaluation Flow

The process of routing a transaction is split into two phases:

### Phase 1: Candidate Filtering (Optimization)
Before full evaluation, the engine uses `FilterCandidates(tx, rules)`. 
- During `Compile()`, the engine scans all string conditions in a `RuleGroup` and its `Children`, extracting alphanumeric tokens into a `keywordSet`.
- When filtering, the `Transaction` is tokenized. The engine performs an $O(1)$ set overlap check.
- If there is no overlap between the transaction's tokens and the rule tree's keywords, the rule is safely skipped.
- *Descendant-Safety*: Because keywords from child groups are bubbled up to the parent's `keywordSet`, filtering does not falsely drop a parent rule if only a nested child condition matches.

### Phase 2: Deep Evaluation
The remaining candidate rules execute `Evaluate(tx)`.
- Conditions check the transaction fields.
- `AND` groups fast-fail on the first `false`. `OR` groups fast-pass on the first `true`.
- The evaluation returns a boolean result and a `MatchExplanation`.

## Audit Logging & Traceability

Given the importance of financial data, every evaluation generates a `MatchExplanation`.

```json
{
  "group_id": 102,
  "group_name": "Software Subscriptions",
  "logic": "OR",
  "final_result": true,
  "conditions": [
    {
      "condition_id": 45,
      "field": "vendor",
      "operator": "contains",
      "target_value": "stripe",
      "tx_value": "Stripe Payments",
      "result": true
    }
  ],
  "children": []
}
```

**`HumanReadableReason()`**
The explanation can generate a readable string for the user interface, parsing the execution trace to output:
`"Categorized by Rule: Software Subscriptions. Because vendor contains stripe."`

*Note: Audit persistence is intentionally separated from evaluation. The service layer handles writing the trace to the database only after the proposed transaction is successfully created to prevent referential integrity errors.*
