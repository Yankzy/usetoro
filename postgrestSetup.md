# Technical PRD: Multi-Tenancy Implementation

**Strategy:** Row-Level Security (RLS) with Shared Database  
**Date:** January 2026

## 1. Overview: Why RLS?

We utilize a **Shared Database, Shared Schema** approach for operational efficiency, but rely on **PostgreSQL Row-Level Security (RLS)** for data isolation. This ensures that a coding error (forgetting a `WHERE` clause) does not result in a data leak. The database kernel itself enforces the separation.

- **Logical Isolation:** Every row belongs to a `tenant_id`.
- **Enforcement:** Policies are checked by the database engine on every `SELECT`, `UPDATE`, and `DELETE`.
- **Performance:** High, as Postgres is optimized for this pattern.

## 2. Schema Implementation

### Step 1: The `tenants` Table (The Registry)

This table acts as the source of truth for who exists in the system. It is generally not RLS-protected in the same way, or protected via a super-admin policy, because the application needs to lookup tenant IDs during authentication.

```sql
-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE tenants (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL,
    status TEXT DEFAULT 'active', -- 'active', 'suspended', 'archived'
    plan_tier TEXT DEFAULT 'basic', -- 'basic', 'pro', 'enterprise'
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Index for authentication lookups
CREATE INDEX idx_tenants_status ON tenants(status);
```

### Step 2: RLS-Enabled Data Tables

Every table containing customer data **MUST** have a `tenant_id` column and RLS enabled.

**Example: `transactions` Table**

```sql
CREATE TABLE transactions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    
    external_id TEXT NOT NULL,
    amount_micros BIGINT NOT NULL,
    description TEXT,
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    
    -- Performance: Index tenant_id for RLS lookups
    UNIQUE(tenant_id, external_id)
);

-- CRITICAL: Enable RLS on the table
ALTER TABLE transactions ENABLE ROW LEVEL SECURITY;
```

## 3. Defining Security Policies

Policies tell Postgres how to filter rows. We use a session variable `app.current_tenant` to pass the context from Go to Postgres.

### The Standard Isolation Policy

This policy applies to `SELECT`, `UPDATE`, and `DELETE` operations.

```sql
-- 1. Create the Policy for Transactions
CREATE POLICY tenant_isolation_policy ON transactions
    FOR ALL -- Applies to SELECT, INSERT, UPDATE, DELETE
    USING (
        -- The row's tenant_id must match the current session variable
        tenant_id = current_setting('app.current_tenant')::UUID
    )
    WITH CHECK (
        -- Ensures new rows are also inserted with the correct tenant_id
        tenant_id = current_setting('app.current_tenant')::UUID
    );
```

> **Note on FORCE ROW LEVEL SECURITY:**  
> For extra safety, if the table owner (usually the migration user) needs to be restricted, you can use:
> ```sql
> ALTER TABLE transactions FORCE ROW LEVEL SECURITY;
> ```
> However, standard RLS is usually sufficient for application users (`toro_app`).

## 4. Go Implementation (pgx Middleware)

The application layer must set the `app.current_tenant` variable inside every transaction before running business logic.

### 4.1 The Secure Transaction Wrapper

Do not use raw `db.Query`. Use this wrapper function.

**File:** `core/store/store.go`

```go
package store

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
    "toro/core/db" // Generated sqlc code
)

type Store struct {
    Pool *pgxpool.Pool
    Queries *db.Queries
}

// ExecTx runs a function within a secure, tenant-isolated transaction.
func (s *Store) ExecTx(ctx context.Context, tenantID string, fn func(*db.Queries) error) error {
	// 1. Start a database transaction
    tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
    // Defer rollback in case of panic or error
	defer tx.Rollback(ctx) 

	// 2. SET THE TENANT CONTEXT (The Critical Step)
    // `SET LOCAL` ensures this variable only exists for the duration of this specific transaction.
    // It is safe to use with connection pooling.
	_, err = tx.Exec(ctx, "SET LOCAL app.current_tenant = $1", tenantID)
	if err != nil {
		return fmt.Errorf("failed to set tenant context: %w", err)
	}

	// 3. Initialize sqlc queries with this transaction
	qTx := s.Queries.WithTx(tx)

	// 4. Execute the Business Logic
	if err := fn(qTx); err != nil {
		return err
	}

	// 5. Commit the transaction
	return tx.Commit(ctx)
}
```

### 4.2 Usage in API Handlers

When an HTTP request comes in, extract the Tenant ID from the JWT/Session and use `ExecTx`.

```go
func (h *Handler) GetDashboard(w http.ResponseWriter, r *http.Request) {
    // 1. Extract Tenant ID (Simulated)
    // In production, get this from the verified JWT token claims
    tenantID := r.Context().Value("tenant_id").(string) 

    // 2. Run Secure Query
    err := h.store.ExecTx(r.Context(), tenantID, func(q *db.Queries) error {
        
        // This query: "SELECT * FROM transactions"
        // Postgres automatically adds: "WHERE tenant_id = '...'"
        txns, err := q.ListTransactions(r.Context())
        if err != nil {
            return err
        }
        
        // ... return JSON response ...
        return nil
    })

    if err != nil {
        http.Error(w, "Internal Error", 500)
    }
}
```

## 5. Bypassing RLS (Admin Tasks)

Sometimes you need to see everything (e.g., "Total Platform Revenue"). To do this, you have two options:

1.  **BYPASS RLS Attribute:** Create a specific Postgres user `toro_admin` with the `BYPASSRLS` attribute. Use a separate connection pool for admin dashboards.
2.  **Explicit Logic:** Since `SET LOCAL` is only for the transaction, if you simply don't set the variable and connect as a superuser/owner, you might see everything (depending on `FORCE RLS` settings).

**Recommendation:** Stick to **Option 1** for dedicated analytics services.

## 6. Migration Management (goose)

Schema changes must include RLS commands.

**File:** `migrations/002_add_invoices.sql`

```sql
-- +goose Up
CREATE TABLE invoices (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    total_amount BIGINT NOT NULL,
    status TEXT
);

-- Don't forget this!
ALTER TABLE invoices ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation_invoices ON invoices
    USING (tenant_id = current_setting('app.current_tenant')::UUID)
    WITH CHECK (tenant_id = current_setting('app.current_tenant')::UUID);

-- +goose Down
DROP POLICY tenant_isolation_invoices ON invoices;
DROP TABLE invoices;
```

## 7. Testing Strategy

Do not assume it works. Write a test case that tries to "hack" it.

```go
func TestTenantIsolation(t *testing.T) {
    // 1. Create Tenant A and Tenant B
    idA := createTenant("A")
    idB := createTenant("B")

    // 2. Insert Data for A
    insertTransaction(idA, "txn_a_1")

    // 3. Try to read as Tenant B
    store.ExecTx(ctx, idB, func(q *db.Queries) error {
        txns, _ := q.ListTransactions(ctx)
        
        // ASSERT: Result must be empty!
        if len(txns) != 0 {
            t.Fatal("SECURITY LEAK: Tenant B saw Tenant A's data")
        }
        return nil
    })
}
```
