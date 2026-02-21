# Querying the Database in a Hierarchical Multi-Tenant Architecture

In a standard flat app, every query is scoped by a single tenant:

```sql
WHERE tenant_id = $1;
```

This breaks in a hierarchical ownership model. If an **Apex CPA** entity logs in and you filter by `entity_id = $1` (their own ID), their dashboard will be empty — because they don't own transactions directly. Their subsidiaries and end-clients do.

To serve the correct data, queries must fetch rows belonging to the entity **and all of its descendants**.

---

## The Wrong Approaches

### Flat query (broken)

```sql
-- name: GetTransactions :many
SELECT * FROM shadow_erp.transactions
WHERE entity_id = $1;
```

**Problem:** Returns nothing for intermediate or apex entities that own data only through their descendants.

### Inline recursive CTE (slow)

```sql
-- name: GetTransactions :many
WITH RECURSIVE auth_tree AS (...)
SELECT * FROM shadow_erp.transactions
WHERE entity_id IN (SELECT id FROM auth_tree);
```

**Problem:** If a page makes 5 database calls, the recursive tree is computed 5 times per request, causing significant CPU load.

---

## The Correct Approach: Pre-computed ID Arrays

Resolve the entity tree **once per HTTP request** in Go, then pass the resulting list of authorized UUIDs into every `sqlc` query.

### SQL standard: `ANY(@ids::uuid[])`

Every query that fetches tenant-scoped data must use the Postgres `= ANY()` operator:

```sql
-- name: GetTransactionsHierarchical :many
SELECT * FROM shadow_erp.transactions
WHERE entity_id = ANY(@authorized_entity_ids::uuid[])
ORDER BY tx_date DESC
LIMIT $1;

-- name: GetRulesHierarchical :many
SELECT * FROM toro_core.rules
WHERE entity_id = ANY(@authorized_entity_ids::uuid[]);
```

### Go middleware pipeline

1. **Authenticate** — Parse the JWT to read `user_id` and `entity_id`.
2. **Resolve the tree** — Query the database (or cache via Redis) to retrieve all descendant entity UUIDs for the authenticated entity.
3. **Inject into context** — Attach the resulting `[]uuid.UUID` slice to the request `context.Context`.
4. **Pass to sqlc** — Handlers read the slice from context and pass it to `sqlc` generated functions.

```go
func (h *TxHandler) ListTransactions(w http.ResponseWriter, r *http.Request) {
    // 1. Read pre-computed authorized IDs from middleware context
    authorizedIDs := r.Context().Value("authorized_entity_ids").([]uuid.UUID)

    // 2. Pass to sqlc — no recursive SQL required here
    txs, err := h.queries.GetTransactionsHierarchical(r.Context(), database.GetTransactionsHierarchicalParams{
        AuthorizedEntityIds: authorizedIDs,
        Limit:               100,
    })

    // ... write JSON response
}
```

---

## Rules for Writing SQL

### Rule 1 — No naked entity filters

Never write `WHERE entity_id = $1` for data-fetching queries. Always use:

```sql
WHERE entity_id = ANY(@auth_ids::uuid[])
```

The only exception is when updating or reading a single known configuration row by exact ID.

### Rule 2 — Server-side ID derivation only

Never trust the client to provide the `entity_id` for queries. Always derive the authorized ID list server-side from the authenticated session.

### Rule 3 — Downward visibility only

A user may only query their own entity and its descendants. Ancestors and sibling branches are never included in the authorized ID array.

---

## Why This Works at Scale

- The recursive tree traversal runs **once** per request, not once per query.
- Postgres evaluates `= ANY(array)` against an indexed `entity_id` column efficiently, handling millions of rows in milliseconds.
- If the middleware does not include a UUID in the array, Postgres **cannot** return the corresponding row — data isolation is enforced at the database layer.