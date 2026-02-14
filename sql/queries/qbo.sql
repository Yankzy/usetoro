-- Token Management Queries (existing functionality)

-- name: UpsertQBOTokens :exec
INSERT INTO qbo_connections (realm_id, access_token, refresh_token, expires_at, tenant_id)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (realm_id) 
DO UPDATE SET 
    access_token = $2, 
    refresh_token = $3, 
    expires_at = $4,
    updated_at = NOW();

-- name: GetQBOTokens :one
SELECT access_token, refresh_token, expires_at, tenant_id
FROM qbo_connections
WHERE realm_id = $1;

-- name: GetQBOConnection :one
SELECT * FROM qbo_connections
WHERE tenant_id = $1;

-- QBO Entity Queries
-- These queries handle upserting and soft-deleting QBO entities in the shadow database

-- name: UpsertAccount :exec
INSERT INTO qbo.accounts (
    id, realm_id, name, account_type, account_sub_type, classification, 
    fully_qualified_name, active, sync_token, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
ON CONFLICT (realm_id, id) DO UPDATE SET
    name = EXCLUDED.name,
    account_type = EXCLUDED.account_type,
    account_sub_type = EXCLUDED.account_sub_type,
    classification = EXCLUDED.classification,
    fully_qualified_name = EXCLUDED.fully_qualified_name,
    active = EXCLUDED.active,
    sync_token = EXCLUDED.sync_token,
    updated_at = NOW(),
    deleted_at = NULL;

-- name: UpsertVendor :exec
INSERT INTO qbo.vendors (
    id, realm_id, display_name, sync_token, last_known_account_id, 
    ai_synonyms, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
ON CONFLICT (realm_id, id) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    sync_token = EXCLUDED.sync_token,
    last_known_account_id = EXCLUDED.last_known_account_id,
    ai_synonyms = EXCLUDED.ai_synonyms,
    updated_at = NOW(),
    deleted_at = NULL;

-- name: UpsertCustomer :exec
INSERT INTO qbo.customers (
    id, realm_id, display_name, sync_token, created_at, updated_at
)
VALUES ($1, $2, $3, $4, NOW(), NOW())
ON CONFLICT (realm_id, id) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    sync_token = EXCLUDED.sync_token,
    updated_at = NOW(),
    deleted_at = NULL;

-- name: UpsertInvoice :exec
INSERT INTO qbo.invoices (
    id, realm_id, customer_id, doc_number, total_amount, balance, 
    due_date, txn_date, sync_token, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
ON CONFLICT (realm_id, id) DO UPDATE SET
    customer_id = EXCLUDED.customer_id,
    doc_number = EXCLUDED.doc_number,
    total_amount = EXCLUDED.total_amount,
    balance = EXCLUDED.balance,
    due_date = EXCLUDED.due_date,
    txn_date = EXCLUDED.txn_date,
    sync_token = EXCLUDED.sync_token,
    updated_at = NOW(),
    deleted_at = NULL;

-- name: UpsertBill :exec
INSERT INTO qbo.bills (
    id, realm_id, vendor_id, doc_number, total_amount, balance, 
    due_date, txn_date, sync_token, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
ON CONFLICT (realm_id, id) DO UPDATE SET
    vendor_id = EXCLUDED.vendor_id,
    doc_number = EXCLUDED.doc_number,
    total_amount = EXCLUDED.total_amount,
    balance = EXCLUDED.balance,
    due_date = EXCLUDED.due_date,
    txn_date = EXCLUDED.txn_date,
    sync_token = EXCLUDED.sync_token,
    updated_at = NOW(),
    deleted_at = NULL;

-- name: SoftDeleteAccount :exec
UPDATE qbo.accounts
SET deleted_at = $1, updated_at = $1
WHERE realm_id = $2 AND id = $3;

-- name: SoftDeleteVendor :exec
UPDATE qbo.vendors
SET deleted_at = $1, updated_at = $1
WHERE realm_id = $2 AND id = $3;

-- name: SoftDeleteCustomer :exec
UPDATE qbo.customers
SET deleted_at = $1, updated_at = $1
WHERE realm_id = $2 AND id = $3;

-- name: SoftDeleteInvoice :exec
UPDATE qbo.invoices
SET deleted_at = $1, updated_at = $1
WHERE realm_id = $2 AND id = $3;

-- name: SoftDeleteBill :exec
UPDATE qbo.bills
SET deleted_at = $1, updated_at = $1
WHERE realm_id = $2 AND id = $3;

-- CDC Queries

-- name: UpdateLastSyncTimestamp :exec
UPDATE qbo_connections
SET last_sync_timestamp = $2, updated_at = NOW()
WHERE realm_id = $1;

-- name: GetAllActiveConnections :many
SELECT realm_id, tenant_id, last_sync_timestamp
FROM qbo_connections
ORDER BY last_sync_timestamp ASC;

-- Webhook Timestamp Tracking (for event-driven CDC)

-- name: UpdateLastWebhookAccount :exec
UPDATE qbo_connections
SET last_webhook_account = $2, updated_at = NOW()
WHERE realm_id = $1;

-- name: UpdateLastWebhookVendor :exec
UPDATE qbo_connections
SET last_webhook_vendor = $2, updated_at = NOW()
WHERE realm_id = $1;

-- name: UpdateLastWebhookCustomer :exec
UPDATE qbo_connections
SET last_webhook_customer = $2, updated_at = NOW()
WHERE realm_id = $1;

-- name: UpdateLastWebhookInvoice :exec
UPDATE qbo_connections
SET last_webhook_invoice = $2, updated_at = NOW()
WHERE realm_id = $1;

-- name: UpdateLastWebhookBill :exec
UPDATE qbo_connections
SET last_webhook_bill = $2, updated_at = NOW()
WHERE realm_id = $1;

-- name: GetConnectionWithWebhookTimes :one
SELECT 
    realm_id, 
    tenant_id, 
    last_sync_timestamp,
    last_webhook_account,
    last_webhook_vendor,
    last_webhook_customer,
    last_webhook_invoice,
    last_webhook_bill
FROM qbo_connections
WHERE realm_id = $1;

-- AI Vector Sync State Queries

-- name: GetVectorSyncState :one
SELECT * FROM qbo.vector_sync_state 
WHERE realm_id = $1;

-- name: UpsertVectorSyncState :exec
INSERT INTO qbo.vector_sync_state (
    realm_id, last_coa_sync, coa_vector_count
)
VALUES ($1, $2, $3)
ON CONFLICT (realm_id) DO UPDATE SET
    last_coa_sync = $2,
    coa_vector_count = $3,
    updated_at = NOW();

-- name: UpdateVendorVectorSync :exec
UPDATE qbo.vector_sync_state
SET last_vendor_sync = $2, vendor_vector_count = $3, updated_at = NOW()
WHERE realm_id = $1;

-- name: UpdateCustomerVectorSync :exec
UPDATE qbo.vector_sync_state
SET last_customer_sync = $2, customer_vector_count = $3, updated_at = NOW()
WHERE realm_id = $1;

-- AI Corrections Queries (for learning from user feedback)

-- name: RecordAICorrection :exec
INSERT INTO qbo.ai_corrections (
    realm_id, user_id, raw_input, ai_prediction, user_correction, correction_type, confidence_score
)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetRecentCorrections :many
SELECT * FROM qbo.ai_corrections
WHERE realm_id = $1 AND correction_type = $2
ORDER BY created_at DESC
LIMIT $3;

-- name: GetVendorByNameOrSynonym :one
SELECT * FROM qbo.vendors
WHERE realm_id = $1 
  AND deleted_at IS NULL
  AND (
    display_name ILIKE $2 
    OR ai_synonyms @> $3::jsonb
  )
LIMIT 1;

-- name: UpdateVendorSynonyms :exec
UPDATE qbo.vendors
SET ai_synonyms = $3, updated_at = NOW()
WHERE realm_id = $1 AND id = $2;

-- name: GetAllAccountsForRealm :many
SELECT * FROM qbo.accounts
WHERE realm_id = $1 AND deleted_at IS NULL
ORDER BY name ASC;

-- name: GetAllVendorsForRealm :many
SELECT * FROM qbo.vendors
WHERE realm_id = $1 AND deleted_at IS NULL
ORDER BY display_name ASC;

-- name: GetAllCustomersForRealm :many
SELECT * FROM qbo.customers
WHERE realm_id = $1 AND deleted_at IS NULL
ORDER BY display_name ASC;

-- name: GetAmbiguousProposals :many
SELECT * FROM qbo.proposed_transactions
WHERE realm_id = $1 
  AND confidence_score < $2
  AND sync_status = 'PENDING'
ORDER BY created_at DESC;

-- name: GetRealmsByTenant :many
SELECT realm_id FROM qbo_connections
WHERE tenant_id = $1;

-- name: GetVendorByID :one
SELECT * FROM qbo.vendors
WHERE realm_id = $1 AND id = $2;

-- name: GetAccountsUpdatedSince :many
SELECT * FROM qbo.accounts
WHERE realm_id = $1 AND updated_at > $2 AND deleted_at IS NULL
ORDER BY updated_at ASC;

-- name: GetVendorsUpdatedSince :many
SELECT * FROM qbo.vendors
WHERE realm_id = $1 AND updated_at > $2 AND deleted_at IS NULL
ORDER BY updated_at ASC;

-- name: GetCustomersUpdatedSince :many
SELECT * FROM qbo.customers
WHERE realm_id = $1 AND updated_at > $2 AND deleted_at IS NULL
ORDER BY updated_at ASC;
