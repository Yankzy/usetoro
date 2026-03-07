Backend Operational PRD: The "Toro Ripple" (Bulk Reclassification Engine)

Epic: Agentic Ledger Orchestration
Target Stack: Golang (Go), PostgreSQL
Trigger Sources: Mobile App (Swipe Left / Reclassify) & Web App (Dropdown Edit)

1. Executive Summary

The "Toro Ripple" eliminates the need for manual, year-end spreadsheet cleanups. When a user changes the category of a single transaction, the backend intercepts the mutation, checks for historical patterns or deterministic rule conflicts, and proposes a bulk retroactive update. If accepted, the Go backend executes an ACID-compliant bulk update, updates the tenant's deterministic rules, and securely logs a cryptographic audit trail.

2. Database Schema Requirements (PostgreSQL)

To support this, we need to ensure our database can handle rules and audit trails.

A. tenant_rules Table

Stores the deterministic categorization rules for a specific CPA firm / client.

id (UUID, Primary Key)

tenant_id (UUID, Indexed)

vendor_name_pattern (String, e.g., "%Capital Grille%")

target_category (String, e.g., "Meals & Entertainment")

created_by (UUID)

updated_at (Timestamp)

B. transaction_audit_logs Table

Crucial for compliance. If the IRS audits, we must prove why a transaction category changed.

id (UUID, Primary Key)

transaction_id (UUID, Indexed)

tenant_id (UUID)

user_id (UUID) - The CPA who initiated the Ripple

old_category (String)

new_category (String)

reason (String) - e.g., "Toro Ripple: Franchisee meetings"

created_at (Timestamp)

3. Phase 1: The Interceptor & "Dry Run" (Impact Analysis)

When the frontend (Mobile or Web) attempts to reclassify a single transaction, it first calls the standard reclassification endpoint. The Go backend must perform a silent "Dry Run" to detect ripple opportunities.

Endpoint: POST /api/v1/transactions/{id}/reclassify

Golang Logic Flow:

Receive: new_category from the payload.

Fetch: Get the target transaction details (vendor_name, old_category, date).

Detect Conflict: Query the tenant_rules table. Does a rule exist for this vendor_name pointing to the old_category?

Or, dynamically query the transactions table: SELECT count(*) FROM transactions WHERE vendor_name = X AND category = Y AND date >= [Start of Fiscal Year].

Return Payload: If count > 1, do NOT just return a simple 200 OK. Return a 206 Partial Content or a specific JSON structure indicating a Ripple opportunity.

JSON Response (Go struct representation):

{
  "status": "success",
  "transaction_updated": true,
  "ripple_opportunity": {
    "detected": true,
    "vendor_name": "Capital Grille",
    "affected_count": 42,
    "affected_total_amount": 14500.00,
    "old_category": "Owner's Draw",
    "new_category": "Meals & Entertainment",
    "message": "Update rule for 'Capital Grille' and retroactively apply to 42 transactions?"
  }
}


4. Phase 2: The Ripple Execution

If the user accepts the Ripple prompt on the frontend, the client fires a second request to execute the bulk update.

Endpoint: POST /api/v1/transactions/ripple-execute

Request Payload:

{
  "tenant_id": "uuid-1234",
  "vendor_name": "Capital Grille",
  "old_category": "Owner's Draw",
  "new_category": "Meals & Entertainment",
  "fiscal_year_start": "2026-01-01T00:00:00Z",
  "audit_reason": "Client confirmed franchisee meetings",
  "update_future_rule": true
}


Golang Execution Flow (Must be inside a database/sql Transaction tx):

Begin DB Transaction: tx, err := db.BeginTx(ctx, nil)

Update Rule (Upsert):

If update_future_rule is true, UPSERT into tenant_rules setting the target_category to the new_category for this vendor_name.

Bulk Update Transactions:

Execute SQL: UPDATE transactions SET category = $1, updated_at = NOW() WHERE tenant_id = $2 AND vendor_name = $3 AND category = $4 AND date >= $5 RETURNING id

Capture the returned IDs of the modified rows.

Batch Insert Audit Logs:

Iterate over the returned transaction IDs and perform a bulk INSERT into transaction_audit_logs.

Commit/Rollback: * If any step fails, tx.Rollback().

If all succeed, tx.Commit().

Async Telemetry (Goroutine): * Fire a Go channel or WebSocket event: go emitLedgerUpdatedEvent(tenant_id) so the frontend CFO Dashboards recalculate runway and AP/AR immediately.

5. Implementation Notes for Go Engineers

Performance: Ensure tenant_id, vendor_name, and date have compound indexes in PostgreSQL to make the Dry Run SELECT count(*) query sub-millisecond.

ACID Compliance: Step 4 (The Ripple Execution) touches three tables (transactions, tenant_rules, transaction_audit_logs). It is critical that this happens inside a single Go sql.Tx block to prevent partial ledger updates if the server crashes mid-execution.

Idempotency: The Ripple endpoint should be idempotent. If clicked twice, the second query should naturally find 0 affected rows and exit gracefully.