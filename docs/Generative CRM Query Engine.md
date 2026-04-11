# 1. Overview

## 1.1 Objective

Enable users to query CRM data using natural language, with the system translating user intent into safe, optimized SQL queries executed against a JSONB-backed PostgreSQL database.

---

## 1.2 Goals

* Allow non-technical users to query data conversationally
* Ensure all queries are safe, validated, and performant
* Abstract database complexity behind a structured query layer
* Support dynamic schemas defined by users

---

## 1.3 Non-Goals

* Direct SQL generation by LLM
* Arbitrary joins (Phase 1)
* Real-time schema mutation during query execution

---

# 2. Core Principles

1. LLM produces **structured intent**, not SQL
2. Backend enforces **validation and safety**
3. Workers handle **query compilation and execution**
4. JSONB remains the **primary storage layer**
5. Performance is ensured through **indexing and constraints**

---

# 3. System Architecture

---

## 3.1 High-Level Flow

```
User Input (Natural Language)
        ↓
Query Planning Agent (LLM)
        ↓
Query DSL (Structured JSON)
        ↓
Validation Layer
        ↓
Query Compiler Worker
        ↓
SQL Execution (PostgreSQL)
        ↓
Result Formatter
        ↓
Frontend Response
```

---

# 4. Functional Requirements

---

## 4.1 Natural Language Input

### Description

User submits a query via UI input field.

### Example Inputs

* “Show total budget by platform”
* “List campaigns with budget greater than 5000”
* “Top 5 campaigns by ROI”

---

## 4.2 Query Planning Agent

### Responsibility

Convert natural language into structured DSL.

---

### Input

```json
{
  "prompt": "Total budget by platform",
  "workflow_key": "ad_campaign_tracker",
  "schema": {...}
}
```

---

### Output (DSL)

```json
{
  "entity": "campaigns",
  "filters": [],
  "aggregations": [
    {"field": "budget", "op": "sum", "alias": "total_budget"}
  ],
  "group_by": ["platform"],
  "order_by": [
    {"field": "total_budget", "direction": "desc"}
  ],
  "limit": 10
}
```

---

### Requirements

* Must return valid JSON only
* Must conform to DSL schema
* Must not generate SQL

---

## 4.3 Query DSL Specification

---

### Root Structure

```json
{
  "entity": "string",
  "filters": [],
  "aggregations": [],
  "group_by": [],
  "order_by": [],
  "limit": number
}
```

---

### Filters

```json
{
  "field": "string",
  "op": "=",
  "value": "any"
}
```

---

### Supported Operators

* =
* !=
* >
* <
* > =
* <=
* IN

---

### Aggregations

* sum
* avg
* count
* min
* max

---

## 4.4 Validation Layer

---

### Responsibilities

* Validate DSL structure
* Ensure fields exist in schema
* Validate type compatibility
* Enforce query limits

---

### Rules

* All fields must exist in schema
* Aggregations require numeric fields
* Limit must not exceed 1000
* Entity must exist

---

### Error Response

```json
{
  "error": "Invalid field: budget_usd",
  "suggestion": "Did you mean 'budget'?"
}
```

---

## 4.5 Query Compiler Worker

---

### Responsibility

Convert validated DSL into parameterized SQL.

---

### Compilation Rules

---

#### Field Access

```sql
data->>'field_name'
```

---

#### Type Casting

* Numeric → `::numeric`
* Date → `::date`

---

### Example SQL

```sql
SELECT
  data->>'platform' AS platform,
  SUM((data->>'budget')::numeric) AS total_budget
FROM workflow_records
WHERE workflow_key = $1
  AND entity_name = $2
GROUP BY platform
ORDER BY total_budget DESC
LIMIT 10;
```

---

### Requirements

* Must use parameterized queries
* Must enforce workflow isolation
* Must prevent SQL injection

---

## 4.6 Execution Layer

---

### Responsibilities

* Execute compiled SQL
* Enforce timeouts
* Return results

---

### Constraints

* Timeout: 200ms–1000ms
* Row limit enforced
* Pagination required

---

## 4.7 Result Formatter

---

### Output Format

```json
{
  "columns": ["platform", "total_budget"],
  "rows": [
    ["Facebook", 50000]
  ]
}
```

---

### Additional Metadata

Optional:

* execution_time
* row_count

---

# 5. Non-Functional Requirements

---

## 5.1 Performance

* Query latency < 300ms
* Indexed JSONB access
* No full table scans for large datasets

---

## 5.2 Scalability

* Horizontal scaling via workers
* Stateless query execution
* Efficient indexing strategy

---

## 5.3 Reliability

* Graceful failure handling
* Retry mechanisms for transient errors

---

## 5.4 Security

* No raw SQL from LLM
* Strict validation layer
* Parameterized queries only
* Row-level isolation

---

# 6. Data Layer Requirements

---

## 6.1 JSONB Querying

All queries operate on:

```sql
workflow_records.data
```

---

## 6.2 Indexing Strategy

---

### Expression Index

```sql
CREATE INDEX idx_platform
ON workflow_records ((data->>'platform'));
```

---

### Numeric Index

```sql
CREATE INDEX idx_budget
ON workflow_records (((data->>'budget')::numeric));
```

---

## 6.3 Field Promotion (Optional)

Frequently queried fields may be promoted:

```sql
ALTER TABLE workflow_records ADD COLUMN budget NUMERIC;
```

---

# 7. Observability

---

## 7.1 Logging

* User query
* DSL output
* SQL generated
* Execution time

---

## 7.2 Metrics

* Query success rate
* Latency
* Validation failures

---

# 8. Failure Handling

---

## 8.1 Invalid DSL

* Return validation error

## 8.2 Slow Queries

* Enforce timeout
* Return partial or error

## 8.3 Missing Fields

* Suggest closest match

---

# 9. Future Enhancements

---

## 9.1 Semantic Layer

* Map synonyms to fields

---

## 9.2 Time Intelligence

* “last month”, “this year”

---

## 9.3 Multi-Entity Queries

* Support joins between entities

---

## 9.4 Materialized Views

* Precompute heavy aggregations

---

# 10. Success Metrics

---

* Query accuracy > 90%
* Query latency < 300ms
* Validation error rate < 5%
* Zero SQL injection incidents

---

# 11. Key Insight

This system is not an LLM querying a database.

It is:

> **A controlled query engine where natural language is compiled into a safe intermediate representation (DSL), then executed deterministically.**
