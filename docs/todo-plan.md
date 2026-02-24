# Implementation Audit: Known TODOs from `docs/report.md`

> Re-audited: 2026-02-24
> Previous plan written: 2026-02-24 (same session)

---

## Status Summary

| # | Item | Status |
|---|------|--------|
| 1 | Invoice/Bill numeric fields in shadow upserts | ✅ Done |
| 2 | QBOConnector.Fetch real implementation | ✅ Done |
| 3 | P&L reconciliation (`compareProfitAndLoss`) | ✅ Done |
| 4a | REST `POST /transactions/{id}/approve` | ✅ Done |
| 4b | REST `POST /reconcile/{realmId}` | ✅ Done |
| 5 | TAP cryptographic signature verification | ✅ Done |
| 6 | GraphQL OTP flows (`requestOTP` / `verifyOTP`) | ✅ Done |

All six items from the original TODO list are resolved. Detailed findings below.

---

## Item 1 — Invoice/Bill numeric fields ✅

**File:** `go/internal/connectors/qbo.go`

All four TODO placeholders replaced with real conversions across all call sites
(`upsertEntity` Invoice/Bill cases + `batchUpsertInvoices` + `batchUpsertBills`):

```go
TotalAmount: jsonNumberToNumeric(invoice.TotalAmt),
Balance:     jsonNumberToNumeric(invoice.Balance),
DueDate:     pgtype.Date{Time: invoice.DueDate.Time, Valid: !invoice.DueDate.IsZero()},
TxnDate:     pgtype.Date{Time: invoice.TxnDate.Time, Valid: !invoice.TxnDate.IsZero()},
```

Bill uses the identical pattern (`bill.TotalAmt`, `bill.Balance`, `bill.DueDate`,
`bill.TxnDate`). The existing `jsonNumberToNumeric` helper handles the `json.Number →
pgtype.Numeric` conversion.

---

## Item 2 — QBOConnector.Fetch ✅

**File:** `go/internal/connectors/qbo.go`

`Fetch` now delegates to the three full-sync methods rather than just calling
`FindCompanyInfo`. The `tenantID → realmID` resolution is handled internally by each
sync method when `realmID` is passed as `""`:

```go
func (c *QBOConnector) Fetch(ctx context.Context, tenantID string) error {
    if _, err := c.SyncFullChartOfAccounts(ctx, tenantID, ""); err != nil {
        return fmt.Errorf("fetch CoA: %w", err)
    }
    if _, err := c.SyncFullCustomers(ctx, tenantID, ""); err != nil {
        return fmt.Errorf("fetch customers: %w", err)
    }
    if _, err := c.SyncFullVendors(ctx, tenantID, ""); err != nil {
        return fmt.Errorf("fetch vendors: %w", err)
    }
    return nil
}
```

---

## Item 3 — P&L reconciliation ✅

**File:** `go/internal/services/accounting/reconciliation_service.go`

Two private helpers were extracted to eliminate duplication and enable P&L parity:

- `buildAccountMap(accounts []database.ShadowErpAccount) map[string]database.ShadowErpAccount`
  — O(1) lookup index on QBO ID.
- `traverseRows(rows, localMap, report, source string)` — recursive row walker,
  now parameterised with a `source` label (`"BalanceSheet"` or `"ProfitAndLoss"`)
  so discrepancies are tagged by origin.

`compareBalanceSheet` and `compareProfitAndLoss` both now call these helpers:

```go
func (s *ReconciliationService) compareProfitAndLoss(ctx context.Context, realmID string, pl *quickbooks.Report, report *DiscrepancyReport) {
    s.logger.Info("📈 Comparing Profit And Loss", "realm_id", realmID)
    localAccounts, err := s.q.GetAllAccountsForRealms(ctx, []string{realmID})
    if err != nil {
        s.logger.Error("Failed to fetch local accounts for reconciliation", "error", err)
        return
    }
    traverseRows(&pl.Rows, buildAccountMap(localAccounts), report, "ProfitAndLoss")
}
```

The old stub `report.TotalChecked++` is gone; real discrepancy detection runs on P&L
rows the same way it does on the Balance Sheet.

---

## Item 4a — REST `POST /transactions/{id}/approve` ✅

**Files:** `go/internal/api/cpa_review_handler.go`, `go/internal/api/handler.go`,
`go/internal/api/server.go`, `sql/queries/qbo.sql`, `go/internal/database/qbo.sql.go`

Two SQL queries were added:

```sql
-- name: GetProposedTransactionByID :one
SELECT * FROM shadow_erp.proposed_transactions WHERE id = $1;

-- name: ApproveProposedTransaction :one
UPDATE shadow_erp.proposed_transactions
SET predicted_account_id = $2,
    predicted_vendor_id  = $3,
    sync_status          = 'APPROVED',
    updated_at           = NOW()
WHERE id = $1
RETURNING *;
```

A `TransactionApprover` interface was added to `handler.go`, keeping `SecretGetter`
focused on its existing responsibilities. `Handler` gained an `Approver
TransactionApprover` field wired from `server.go` via `st.Queries`.

The handler now:
1. Parses path UUID and request body.
2. Calls `h.Approver.GetProposedTransactionByID` — returns 404 if not found.
3. Builds `ApproveProposedTransactionParams` (account UUID required, vendor UUID
   optional).
4. Calls `h.Approver.ApproveProposedTransaction` and JSON-encodes the returned row.

---

## Item 4b — REST `POST /reconcile/{realmId}` ✅

**Files:** `go/internal/api/cpa_review_handler.go`, `go/internal/api/handler.go`,
`go/internal/api/server.go`

`Handler` gained a `Reconciler *accounting.ReconciliationService` field. In `server.go`
the connector is constructed first, then:

```go
reconciler := accounting.NewReconciliationService(logger, st.Queries, connector.ClientForRealm)
```

`HandleReconcileMonth` now calls `h.Reconciler.ReconcileMonth(ctx, realmID)` and
JSON-encodes the `*DiscrepancyReport`. A 500 is returned if reconciliation fails.

---

## Item 5 — TAP cryptographic signature verification ✅

**File:** `tap/pkg/contract/main.go`

`verifySig` now performs real Ed25519 verification using the `identity` package:

```go
func (m *Machine) verifySig(did, data, sig string) bool {
    pubKeyHex, err := identity.PubKeyFromDID(did)
    if err != nil {
        return false
    }
    ok, err := identity.Verify(pubKeyHex, []byte(data), sig)
    return err == nil && ok
}
```

`identity.PubKeyFromDID` extracts the hex-encoded public key from the `did:toro:<hex>`
format. `identity.Verify` performs the standard `ed25519.Verify` check against the
decoded bytes. No new dependencies are introduced — both functions are already in the
`tap/pkg/identity` package.

---

## Item 6 — GraphQL OTP flows ✅

**File:** `go/cmd/graphql/graph/schema.resolvers.go`

Both resolvers are fully implemented; neither panics.

**`requestOTP`:**
- Looks up user by email (ignores `ErrNoRows` to avoid user-enumeration leaks).
- Generates a 6-digit OTP via `crypto/rand` (not `math/rand`).
- Stores `otp:<email>` in Redis with 10-minute TTL.
- Logs the OTP (`// TODO: wire email sender` comment marks the delivery gap).
- Returns `true`.

**`verifyOTP`:**
- Fetches `otp:<email>` from Redis; returns error if key is missing/expired.
- Uses `subtle.ConstantTimeCompare` to prevent timing attacks.
- Deletes the Redis key immediately (single-use).
- Fetches user via `GetUserByEmail`, generates tokens via `generateTokens`.
- Persists refresh token to DB and Redis (identical pattern to `Login`).
- Returns `AuthPayload`.

---

## Remaining gaps / watch-list

The six original TODOs are closed. The following minor points were observed during
the audit and are worth tracking:

1. ~~**OTP email delivery**~~ — **RESOLVED.** `requestOTP` now calls `r.EmailSender.SendOTP(email, otp)`.
   `auth.EmailSender` (SMTP) is already wired into `Resolver` via `server.go` using
   `EMAIL_HOST` / `EMAIL_HOST_USER` / `EMAIL_HOST_PASSWORD` / `DEFAULT_FROM_EMAIL` env vars.

2. ~~**`qboConnection` company name**~~ — **RESOLVED.** `QboConnection` resolver now reads
   `company_name` from `shadow_erp.company_info` (populated by `SyncCompanyInfo` at
   connect time and on every CDC cycle). Falls back to `"QuickBooks Linked Company"` if
   the row hasn't been synced yet.

3. **P&L floating-point comparison** — `traverseRows` does a string equality check
   after stripping commas. A tolerance-based float comparison (`math.Abs(a-b) <
   epsilon`) would reduce false positives on rounding differences between QBO and the
   shadow DB.

4. **`make sqlc` prerequisite** — Items 4a/4b required running `make sqlc` to
   regenerate `go/internal/database/qbo.sql.go`. Confirm the generated file is
   committed and the migration (if any schema column was added) has been applied.
