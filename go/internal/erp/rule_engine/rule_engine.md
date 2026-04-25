# Toro ERP: Accounting Rule Engine Documentation

The Rule Engine is the deterministic firewall of the Toro ERP architecture. It provides a hyper-fast, hierarchical Abstract Syntax Tree (AST) system to evaluate financial transactions against complex, multi-layered conditions (e.g., regex, amount thresholds, date bounds, double-entry context).

By sitting directly in front of the AI Categorization pipeline, the Rule Engine catches known patterns instantly, routing transactions to the correct ledger accounts while preventing them from consuming expensive LLM resources.

---

## 1. Economic & Architectural Benefits

* **$0 API Costs:** Every transaction matched by the Rule Engine bypasses the LLM layer completely, saving token costs and API overhead at scale.
* **Microsecond Latency:** Rule evaluation happens entirely in-memory using pre-compiled regex and mapped token sets via a `ristretto` cache. It takes microseconds compared to the 3-10 seconds required for an LLM response.
* **100% Determinism:** The engine eliminates AI hallucinations. If a CPA defines a strict accounting mapping, the engine guarantees that mapping will be executed flawlessly every single time.
* **Double-Entry Context Safety:** Rules are not simply bound to a "Vendor". They evaluate the `SourceAccount` (the exact bank or credit card used). This guarantees personal vs. corporate expense separation, eliminating cross-contamination.

---

## 2. Core Components & AST Structure

The core engine (`internal/erp/rule_engine/rule_engine.go`) evaluates a `Transaction` against a nested tree of nodes.

### The Rule Objects
* **`RuleGroup`**: A logical node (`AND` / `OR`). Groups can be nested infinitely using a `ParentID` to form complex hierarchical ASTs (e.g., `Amount > 100` AND (`Vendor = Uber` OR `Vendor = Lyft`)).
* **`RuleCondition`**: A single, atomic boolean evaluation containing the `Field`, `Operator`, and `Value`.
* **`CashDirection`**: A hard-coded boundary (`INFLOW` vs `OUTFLOW`). The engine enforces this strictly at the root level so a deposit rule can never accidentally match a purchase.
* **`Allocations`**: A JSONB array mapping the transaction to one or more Chart of Account IDs (handling line-item splits).

### Supported Operators Matrix

| Data Type | Allowed Operators |
| :--- | :--- |
| **String (Case-Insensitive)** | `equals`, `contains`, `not_contains`, `startswith`, `endswith`, `in`, `not_in`, `regex` |
| **String (Case-Sensitive)** | `equals_cs`, `contains_cs` |
| **Numeric & Date** | `gt`, `gte`, `lt`, `lte`, `equals`, `in`, `not_in` |
| **Existence** | `is_null`, `is_not_null` |

---

## 3. Database Schema (`shadow_erp`)

The engine is inherently multi-tenant, binding all rules to a `realm_id`.

* **`shadow_erp.rule_groups`**: Stores metadata, tree hierarchy (`parent_id`), strict `direction`, and mapping targets (`target_entity_id`, `allocations`). It also holds the `requires_review` flag for complex routing (e.g., parking Payroll transactions for manual CPA review).
* **`shadow_erp.rule_conditions`**: The specific boolean conditions tied to a `rule_group_id`.
* **`shadow_erp.rule_audit_logs`**: Stores the verbose `match_info` JSON tree and a human-readable explanation every time a rule fires successfully.

---

## 4. The Execution Lifecycle

### Phase A: Rule Generation
Rules enter the system via the `RuleEngineService.CreateRule` method from three primary sources:
1. **The Bootstrapper:** Analyzes historical QBO consensus data to generate safe, 1-to-1 strict rules (`equals_cs`). It secures the rule by locking both the target (Vendor) and the context (Source Bank Account).
2. **The AI Worker:** Analyzes messy, unknown bank feeds and writes dynamic AST JSON payloads back to the engine to catch future variations (e.g., using Regex for dynamic merchant IDs).
3. **The CPA (Manual):** Users can define hyper-specific rules via the UI.

> **Idempotency Gatekeeper:** Before any rule is saved, the service recursively checks the database. If an exact logical duplicate exists (matching conditions, allocations, and direction), it silently skips creation to prevent database bloat.

### Phase B: Candidate Optimization
To prevent evaluating thousands of rules against every transaction, the engine optimizes the candidate pool in O(1) time:
1. **Compilation:** Upon loading from the DB into the cache, strings are split into token sets and Regex/Dates are pre-compiled. The engine auto-derives a `keywordSet` from the rule's conditions.
2. **Hard Boundary Filtering:** If a transaction is an `OUTFLOW`, the engine instantly drops all `INFLOW` rules.
3. **Keyword Intersection:** The engine tokenizes the incoming transaction. If there is no overlap between the transaction tokens and the rule's `keywordSet`, the rule is skipped entirely.

### Phase C: Worker Evaluation
Transactions from CSV uploads or Plaid webhooks are intercepted by the `RuleEvaluationWorker`:
1. The transaction is mapped to the engine's standard `Transaction` struct.
2. The engine filters candidates and runs the AST logic.
3. If a match is found, the worker updates the `fignode.staging_transactions` row with the `rule_group_id` and predicted ledger IDs.
4. If `requires_review = false`, the transaction is instantly moved to `SWIPED_APPROVED`.

---

## 5. API & Payload Specifications

### Creating a Rule (AST Payload)
Rules are ingested using the `CreateRuleRequest` struct. Nested logic is supported via `ChildGroups`.

```json
{
  "RealmID": "9341456276406470",
  "Name": "Auto-Generated: Chin's Gas (via Checking)",
  "Logic": "AND",
  "Priority": 10,
  "Active": true,
  "Direction": "OUTFLOW",
  "TargetEntityID": "uuid-for-chins-gas",
  "Allocations": [
    {
      "AccountID": "uuid-for-auto-expense",
      "Percentage": 100.0
    }
  ],
  "RequiresReview": false,
  "Conditions": [
    {
      "Field": "vendor",
      "Operator": "equals_cs",
      "Value": "Chin's Gas and Oil"
    },
    {
      "Field": "source_account",
      "Operator": "equals_cs",
      "Value": "Checking"
    }
  ]
}
```

### Auditability & Traceability
Accountants require a strict paper trail. When a rule executes, it produces a `MatchExplanation` object. This JSON trace is saved to `rule_audit_logs` along with a translated, human-readable sentence.

```json
{
  "group_id": 1,
  "group_name": "Auto-Generated: Chin's Gas (via Checking)",
  "logic": "AND",
  "final_result": true,
  "conditions": [
    {
      "condition_id": 101,
      "field": "vendor",
      "operator": "equals_cs",
      "target_value": "Chin's Gas and Oil",
      "tx_value": "Chin's Gas and Oil",
      "result": true
    }
  ]
}
```
**Human-Readable Output:** > *"Categorized by Rule: Auto-Generated: Chin's Gas (via Checking). Because the vendor was exactly 'Chin's Gas and Oil' and the source_account was exactly 'Checking'."*


### The QuickBooks "Anonymous Deposit" Problem
Our SQL query for the deposit bootstrapper:
```sql
WHERE customer_id IS NOT NULL AND income_account_id IS NOT NULL
```
Our Rule Engine is extremely strict. To create a rule, it **must** have an anchor entity (a Vendor for purchases, or a Customer for deposits). 

In QuickBooks, when people manually create Expenses, they almost always fill out the "Payee" (Vendor). But when they create **Deposits**, they notoriously leave the **"Received From" (Customer)** column completely blank. 

If deposits are "Opening Balance Equity" or general bank transfers, they likely do not have a Customer attached to them in QBO. Because there is no Customer, `customer_id` is `NULL`. The SQL query filters them out, returns 0 rows, and our bootstrapper safely creates 0 rules.

if we insert in db:
docker compose -f container/docker-compose.yml exec -T db psql -U toro -d toro << 'EOF'

-- 1. Insert the Mock Customer (Added sync_token)
INSERT INTO shadow_erp.customers (id, realm_id, erp_id, sync_token, display_name)
VALUES (gen_random_uuid(), '9341456276406470', 'CUST-999', '1', 'Mock Customer LLC')
ON CONFLICT DO NOTHING;

-- 2. Insert the Mock Bank Account (Added sync_token)
INSERT INTO shadow_erp.accounts (id, realm_id, erp_id, sync_token, name, account_type)
VALUES (gen_random_uuid(), '9341456276406470', 'BANK-777', '1', 'Mock Checking Account', 'Bank')
ON CONFLICT DO NOTHING;

-- 3. Insert the Mock Income Account (Added sync_token)
INSERT INTO shadow_erp.accounts (id, realm_id, erp_id, sync_token, name, account_type)
VALUES (gen_random_uuid(), '9341456276406470', 'INC-888', '1', 'Mock Services Income', 'Income')
ON CONFLICT DO NOTHING;

EOF

rule engine will create rule for it.

