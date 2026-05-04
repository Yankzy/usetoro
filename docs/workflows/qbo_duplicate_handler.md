Here is the updated, operational PRD. This version completely rips out the N+1 query bottleneck and replaces it with the high-performance **Batching Architecture**. 

This document defines exactly how your AI Worker will pull 50 transactions at a time, check them against your local ledger in a single millisecond query, and push the safe ones to QuickBooks using their bulk endpoint.



---

# Operational PRD: The Anti-Duplicate Batch Engine

## 1. Objective and Scope
**Goal:** Process staging transactions in high-volume batches (e.g., 50 at a time) to eliminate N+1 database queries and API rate limits, while mathematically guaranteeing zero duplicates are pushed to the QuickBooks Online (QBO) ledger.
**Impact:** Allows the system to process thousands of CSV rows instantly, safely matching existing QBO data and bulk-creating the rest.

## 2. Definitions of Failure (The Vectors)
1. **The Double Upload (Ingestion Vector):** The CPA uploads the exact same CSV twice.
2. **The Manual Meddler (Ledger Vector):** The business owner already manually entered the transaction into QBO before our agent processed it.
3. **The Network Hiccup (API Vector):** The Agent batch-posts to QBO, the internet drops the response, and the Agent blindly retries the batch.

---

## 3. Phase 1: The Ingestion Shield (Database Layer)
This layer stops *exact* file-level duplicates before they ever enter the batching queue.

### Operational Requirements:
1.  **Plaid Webhooks:** `plaid_transaction_id` MUST be mapped to a `UNIQUE` constraint in `staging_transactions`.
2.  **CSV Uploads (The Hash):** The Go parser generates a deterministic `transaction_hash`.

**The Hashing Algorithm:**
To allow valid recurring transactions (same amount, diff day) but block double-uploads, the Date MUST be in the hash.
```go
// Format: AccountID | YYYY-MM-DD | Amount | Description
hashInput := fmt.Sprintf("%s|%s|%s|%s", accountID, standardDate, rawAmount, rawDescription)
transactionHash := hex.EncodeToString(sha256.Sum256([]byte(hashInput)))
```
*Database Action:* `INSERT ON CONFLICT (transaction_hash) DO NOTHING`.

---

## 4. Phase 2: The Bulk Pre-Flight Ledger Check (Worker Layer)
Instead of the Agent asking QBO or the local DB about transactions one by one, the Agent grabs a chunk of 50 pending transactions and asks the local `shadow_erp` database to check them all simultaneously.

### Technical Implementation (SQL):
The Go worker passes the 50 `staging_transactions` into a single Postgres Common Table Expression (CTE).

```sql
-- The Go worker dynamically generates this VALUES list
WITH batch_transactions AS (
    SELECT * FROM (VALUES 
        ('stg-uuid-1', -50.00, '2026-04-28'::date, 'checking-uuid'),
        ('stg-uuid-2', -12.50, '2026-04-29'::date, 'checking-uuid'),
        -- ... up to 50 rows ...
    ) AS t(staging_id, amount, txn_date, source_account_id)
)
-- Bulk-JOIN those 50 rows against the shadow_erp ledger
SELECT 
    b.staging_id, 
    p.erp_transaction_id AS matched_erp_id
FROM batch_transactions b
JOIN shadow_erp.purchases p 
    ON p.source_account_id = b.source_account_id
    AND p.total_amount = b.amount
    -- The magic +/- 2 day window, evaluated for all 50 rows!
    AND p.txn_date BETWEEN (b.txn_date - INTERVAL '2 days') AND (b.txn_date + INTERVAL '2 days');
```

### Worker Logic Handling:
* The query returns a list of Staging IDs that *matched* existing ledger entries (e.g., `stg-uuid-2` was found).
* **The Matches:** The worker runs a bulk `UPDATE staging_transactions SET status = 'POSTED_AS_MATCH', erp_transaction_id = [matched_erp_id]` for those rows. They are removed from the queue.
* **The Safe List:** Any Staging ID from the original 50 that was *not* returned by the query is guaranteed safe. Move them to Phase 3.

---

## 5. Phase 3: The QBO Batch API Dance (API Layer)
We now have a "Safe List" of (for example) 47 transactions. We will use the QBO Batch API (`POST /v3/company/realm_id/batch`) to push them. *Note: QBO enforces a strict limit of 30 items per batch request. We must chunk our 47 safe items into a chunk of 30, and a chunk of 17.*

### Operational Requirements:
1. **The Smuggled ID:** QBO does not support idempotency headers. We MUST smuggle our `staging_id` into the `PrivateNote` of every payload to recover from network timeouts.
2. **The Batch ID (`bId`):** QBO requires a unique string (`bId`) for every operation inside the batch so we can map the response back to our staging rows.

### Technical Implementation (JSON Payload):
```json
{
  "BatchItemRequest": [
    {
      "bId": "stg-uuid-1",     // 🚨 Used by QBO to tell us if THIS specific row succeeded
      "operation": "create",
      "Purchase": {
        "AccountRef": { "value": "41" },
        "PaymentType": "CreditCard",
        "TotalAmt": 50.00,
        "PrivateNote": "Uber Trip [stg_id: stg-uuid-1]" // 🚨 The Idempotency Smuggle
      }
    },
    {
      "bId": "stg-uuid-3",
      "operation": "create",
      "Purchase": { ... }
    }
  ]
}
```

### Processing the QBO Batch Response:
When QBO returns the `200 OK` for the batch, it replies with an array of results mapped to your `bId`s. Some may succeed, some may fail (e.g., if an account was deleted).

```json
{
  "BatchItemResponse": [
    {
      "bId": "stg-uuid-1",
      "Purchase": { "Id": "qbo-992" } // SUCCESS!
    },
    {
      "bId": "stg-uuid-3",
      "Fault": { "Error": [{"Message": "Account Invalid"}] } // FAILED!
    }
  ]
}
```

**Worker Cleanup:**
The Go worker iterates over the response:
* For `stg-uuid-1`: `UPDATE staging_transactions SET status = 'POSTED', erp_transaction_id = 'qbo-992'`
* For `stg-uuid-3`: `UPDATE staging_transactions SET status = 'FAILED_API', error_message = 'Account Invalid'`

---

## 6. The Idempotency Recovery (Handling the 504 Timeout)
What happens if Phase 3 executes, QBO saves the data, but our server drops the connection before we get the Batch Response?

1. Ten minutes later, the Agent retries the batch.
2. Before assembling the JSON payload, the Agent checks the QBO API for the smuggled IDs of the current batch:
   `GET /v3/company/realm_id/query?query=SELECT Id, PrivateNote FROM Purchase WHERE PrivateNote LIKE '%stg_id:%'` *(Filter this locally in Go to find the IDs from your current batch).*
3. If it finds `stg-uuid-1` already in QBO, it removes it from the retry payload and marks it `POSTED` locally. It only resends the items that genuinely failed.

## Summary of the Developer Workflow
1. Write the Go func `ProcessStagingBatch(limit int)` that pulls 50 rows.
2. Write the CTE SQL to check all 50 against `shadow_erp` instantly.
3. Chunk the remaining "safe" items into groups of 30.
4. Construct the QBO Batch JSON, using `staging_id` as the `bId`.
5. Map the QBO responses back to the staging table to mark them `POSTED` or `FAILED_API`.