# 1. Product Overview

## 1.1 Objective

Build a **Generative CRM platform** where users (CPAs or businesses) can:

* Define CRM systems using:

  * AI (text or screenshot)
  * Templates
  * Low-code builder
* Store structured data dynamically
* Operate workflows via AI agents
* Render UI dynamically without hardcoded frontend

---

## 1.2 Core Principle

> Separate **Data**, **Schema**, and **UI Config**

* Data = JSONB (flexible)
* Schema = structured metadata
* UI = generated configuration (not source of truth)

---

# 2. System Architecture

## 2.1 High-Level Components

### Backend

* Orchestration Engine (Agents + Workers)
* PostgreSQL (JSONB + relational)
* Object Storage (files, screenshots)
* Message Bus (NATS)

### Frontend

* Dynamic Renderer (React)
* Builder Interface
* AI-assisted onboarding UI

---

## 2.2 Core Entities

### Tables

## `sessions`

Tracks workflow lifecycle

```
id (uuid)
user_id (fk)
workflow_key (string)
status (enum)
created_at
```

---

## `workflow_schemas`

Defines user-created CRM structures

```
id (uuid)
user_id (fk)
workflow_key (string)
name (string)
schema (jsonb)
version (int)
created_at
updated_at
```

### Example schema:

```json
{
  "entities": [
    {
      "name": "campaigns",
      "fields": [
        {"name": "name", "type": "string", "semantic": "title"},
        {"name": "budget", "type": "number", "semantic": "currency"},
        {"name": "platform", "type": "string", "semantic": "category"}
      ]
    }
  ]
}
```

---

## `workflow_records`

Stores actual CRM data

```
id (uuid)
workflow_key (string)
entity_name (string)
session_id (fk)
data (jsonb)
created_at
updated_at
```

---

## `workflow_ui_configs`

Stores generated UI config

```
id (uuid)
workflow_key (string)
config (jsonb)
version (int)
```

---

## `workflow_files`

Stores uploaded files (CSV, screenshots)

```
id (uuid)
session_id (fk)
file_url (string)
file_type (enum: csv, image)
```

---

# 3. User Flows

---

## 3.1 CRM Creation Flow

### Entry Options

1. Template selection
2. Manual builder
3. AI generation:

   * Text prompt
   * Screenshot upload

---

## 3.2 AI Generation Flow

### Step 1: Input

User provides:

* Screenshot OR
* Text description

---

### Step 2: Agent Pipeline

#### Agent 1: Input Understanding Agent

* Extract intent from text/image

#### Agent 2: Schema Generation Agent

* Output structured schema JSON

#### Agent 3: UI Config Generation Agent

* Output table/form configuration

---

### Step 3: Validation UI

Frontend renders:

* Editable table preview
* Field list

User can:

* Rename fields
* Change types
* Add/remove fields

---

### Step 4: Persist

Worker:

* Save schema → `workflow_schemas`
* Save UI config → `workflow_ui_configs`

---

## 3.3 Data Entry Flow

### Methods

1. Manual entry (UI forms)
2. CSV upload
3. API ingestion

---

### CSV Flow

#### Agent 1: CSV Schema Mapper

* Maps headers to schema

#### Agent 2: Data Cleaning Agent

* Normalize values

#### Agent 3: Enrichment Agent

* Add metadata

#### Worker:

* Insert into `workflow_records`

---

## 3.4 Data Visualization Flow

Frontend:

1. Fetch schema
2. Fetch UI config
3. Fetch records

Renderer builds:

* Table view
* Filters
* Sorting
* Forms

---

# 4. Agent Design

---

## 4.1 Schema Generation Agent

### Input:

* Text OR image

### Output:

```json
{
  "entities": [...],
  "relationships": [...]
}
```

### Constraints:

* Must infer:

  * field types
  * semantics
* Must remain editable

---

## 4.2 UI Config Agent

### Output:

```json
{
  "views": [
    {
      "type": "table",
      "entity": "campaigns",
      "columns": ["name", "platform", "budget"],
      "filters": ["platform"],
      "sortable": ["budget"]
    }
  ]
}
```

---

## 4.3 Validation Agent

### Purpose:

* Detect inconsistencies

### Examples:

* Missing primary field
* Invalid types
* Duplicate field names

---

## 4.4 Data Mapping Agent

Maps incoming CSV fields to schema

---

# 5. Worker Design

Workers perform:

* DB writes
* Index updates
* File processing
* Schema versioning

---

# 6. Schema Versioning

## Problem

Users modify schema over time

## Solution

### Versioned schemas

```
workflow_schemas.version
```

### Rules:

* New version on edit
* Old records remain unchanged
* Migration agent optional

---

# 7. Query Layer

---

## 7.1 JSONB Query Strategy

Use:

* GIN indexes
* Expression indexes

Example:

```
CREATE INDEX idx_platform
ON workflow_records ((data->>'platform'));
```

---

## 7.2 Filtering

Dynamic query builder:

* WHERE data->>'field' = value
* Range queries for numbers/dates

---

# 8. Frontend Architecture

---

## 8.1 Dynamic Renderer

Input:

* schema
* ui_config
* data

Output:

* Tables
* Forms
* Dashboards

---

## 8.2 Components

* TableRenderer
* FormRenderer
* FilterBuilder
* FieldEditor

---

## 8.3 Builder UI

### Modes:

1. AI-generated edit mode
2. Manual drag-drop mode

---

# 9. AI Usage Boundaries

---

## Allowed:

* Schema generation
* UI config generation
* Data enrichment
* Insights

---

## Not Allowed:

* Runtime UI rendering
* Data storage logic
* Query execution

---

# 10. Permissions & Multi-Tenancy

---

## Row isolation:

```
user_id required on:
- workflow_schemas
- workflow_records
```

---

## Access control:

* Owner
* Editor
* Viewer

---

# 11. Performance Considerations

---

## Indexing

* GIN on JSONB
* Expression indexes on frequent fields

---

## Caching

* UI config cached per workflow
* Schema cached

---

## Pagination

* Required for all queries

---

# 12. Observability

---

## Logs:

* Agent execution logs
* Worker logs

## Metrics:

* Schema generation success rate
* CSV mapping accuracy
* Query latency

---

# 13. Future Extensions

---

* Relationships (foreign keys between entities)
* Automation workflows (triggers)
* AI insights per CRM
* Marketplace for templates

---

# 14. Key Risks

---

## 1. Schema inconsistency

Mitigation: validation agent + forced UI review

## 2. JSONB performance

Mitigation: indexing + promotion to structured tables

## 3. Over-reliance on AI

Mitigation: human-in-the-loop validation

---

# 15. Success Criteria

---

* User creates CRM in < 2 minutes
* CSV ingestion accuracy > 90%
* Query latency < 200ms
* Schema edit without breaking system

---

# Final Note

This system is not just a CRM.

It is a:

> **General-purpose structured data generation engine powered by AI**

If implemented correctly, it can expand into:

* ERP
* Accounting systems
* Internal tools
* Vertical SaaS builders
