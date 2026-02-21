-- =========================================================================
-- QBO Connection / Token Management
-- =========================================================================

-- name: UpsertQBOTokens :exec
INSERT INTO toro_core.qbo_connections (entity_id, realm_id, access_token, refresh_token, expires_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (realm_id)
DO UPDATE SET
    access_token  = $3,
    refresh_token = $4,
    expires_at    = $5,
    updated_at    = NOW();

-- name: GetQBOTokens :one
SELECT access_token, refresh_token, expires_at, entity_id
FROM toro_core.qbo_connections
WHERE realm_id = $1;

-- name: GetQBOConnection :one
SELECT * FROM toro_core.qbo_connections
WHERE entity_id = $1;

-- name: UpdateLastSyncTimestamp :exec
UPDATE toro_core.qbo_connections
SET last_sync_timestamp = $2, updated_at = NOW()
WHERE realm_id = $1;

-- name: GetAllActiveConnections :many
SELECT realm_id, entity_id, last_sync_timestamp
FROM toro_core.qbo_connections
ORDER BY last_sync_timestamp ASC;

-- name: GetRealmsForEntities :many
SELECT DISTINCT realm_id FROM toro_core.qbo_connections
WHERE entity_id = ANY(@authorized_entity_ids::uuid[])
  AND access_token IS NOT NULL;

-- =========================================================================
-- Webhook Timestamp Tracking (for event-driven CDC)
-- =========================================================================

-- name: UpdateLastWebhookAccount :exec
UPDATE toro_core.qbo_connections
SET last_webhook_account = $2, updated_at = NOW()
WHERE realm_id = $1;

-- name: UpdateLastWebhookVendor :exec
UPDATE toro_core.qbo_connections
SET last_webhook_vendor = $2, updated_at = NOW()
WHERE realm_id = $1;

-- name: UpdateLastWebhookCustomer :exec
UPDATE toro_core.qbo_connections
SET last_webhook_customer = $2, updated_at = NOW()
WHERE realm_id = $1;

-- name: UpdateLastWebhookInvoice :exec
UPDATE toro_core.qbo_connections
SET last_webhook_invoice = $2, updated_at = NOW()
WHERE realm_id = $1;

-- name: UpdateLastWebhookBill :exec
UPDATE toro_core.qbo_connections
SET last_webhook_bill = $2, updated_at = NOW()
WHERE realm_id = $1;

-- name: GetConnectionWithWebhookTimes :one
SELECT
    realm_id,
    entity_id,
    last_sync_timestamp,
    last_webhook_account,
    last_webhook_vendor,
    last_webhook_customer,
    last_webhook_invoice,
    last_webhook_bill
FROM toro_core.qbo_connections
WHERE realm_id = $1;

-- =========================================================================
-- shadow_erp Entity Queries (Upsert / Soft-Delete)
-- =========================================================================

-- name: UpsertAccount :exec
INSERT INTO shadow_erp.accounts (
    qbo_id, realm_id, name, account_type, account_sub_type, classification,
    fully_qualified_name, active, sync_token, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
ON CONFLICT (realm_id, qbo_id) DO UPDATE SET
    name                 = EXCLUDED.name,
    account_type         = EXCLUDED.account_type,
    account_sub_type     = EXCLUDED.account_sub_type,
    classification       = EXCLUDED.classification,
    fully_qualified_name = EXCLUDED.fully_qualified_name,
    active               = EXCLUDED.active,
    sync_token           = EXCLUDED.sync_token,
    updated_at           = NOW(),
    deleted_at           = NULL;

-- name: UpsertVendor :exec
INSERT INTO shadow_erp.vendors (
    qbo_id, realm_id, display_name, sync_token, last_known_account_id,
    ai_synonyms, created_at, updated_at
)
VALUES (
    $1, $2, $3, $4,
    (SELECT id FROM shadow_erp.accounts WHERE shadow_erp.accounts.qbo_id = sqlc.narg('last_known_account_qbo_id') AND shadow_erp.accounts.realm_id = $2),
    $5, NOW(), NOW()
)
ON CONFLICT (realm_id, qbo_id) DO UPDATE SET
    display_name          = EXCLUDED.display_name,
    sync_token            = EXCLUDED.sync_token,
    last_known_account_id = EXCLUDED.last_known_account_id,
    ai_synonyms           = EXCLUDED.ai_synonyms,
    updated_at            = NOW(),
    deleted_at            = NULL;

-- name: UpsertCustomer :exec
INSERT INTO shadow_erp.customers (
    qbo_id, realm_id, display_name, sync_token, created_at, updated_at
)
VALUES ($1, $2, $3, $4, NOW(), NOW())
ON CONFLICT (realm_id, qbo_id) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    sync_token   = EXCLUDED.sync_token,
    updated_at   = NOW(),
    deleted_at   = NULL;

-- name: UpsertInvoice :exec
INSERT INTO shadow_erp.invoices (
    qbo_id, realm_id, customer_id, doc_number, total_amount, balance,
    due_date, txn_date, sync_token, created_at, updated_at
)
VALUES (
    $1, $2,
    (SELECT id FROM shadow_erp.customers WHERE shadow_erp.customers.qbo_id = sqlc.narg('customer_qbo_id') AND shadow_erp.customers.realm_id = $2),
    $3, $4, $5, $6, $7, $8, NOW(), NOW()
)
ON CONFLICT (realm_id, qbo_id) DO UPDATE SET
    customer_id  = EXCLUDED.customer_id,
    doc_number   = EXCLUDED.doc_number,
    total_amount = EXCLUDED.total_amount,
    balance      = EXCLUDED.balance,
    due_date     = EXCLUDED.due_date,
    txn_date     = EXCLUDED.txn_date,
    sync_token   = EXCLUDED.sync_token,
    updated_at   = NOW(),
    deleted_at   = NULL;

-- name: UpsertBill :exec
INSERT INTO shadow_erp.bills (
    qbo_id, realm_id, vendor_id, doc_number, total_amount, balance,
    due_date, txn_date, sync_token, created_at, updated_at
)
VALUES (
    $1, $2,
    (SELECT id FROM shadow_erp.vendors WHERE shadow_erp.vendors.qbo_id = sqlc.narg('vendor_qbo_id') AND shadow_erp.vendors.realm_id = $2),
    $3, $4, $5, $6, $7, $8, NOW(), NOW()
)
ON CONFLICT (realm_id, qbo_id) DO UPDATE SET
    vendor_id    = EXCLUDED.vendor_id,
    doc_number   = EXCLUDED.doc_number,
    total_amount = EXCLUDED.total_amount,
    balance      = EXCLUDED.balance,
    due_date     = EXCLUDED.due_date,
    txn_date     = EXCLUDED.txn_date,
    sync_token   = EXCLUDED.sync_token,
    updated_at   = NOW(),
    deleted_at   = NULL;

-- name: SoftDeleteAccount :exec
UPDATE shadow_erp.accounts
SET deleted_at = $1, updated_at = $1
WHERE realm_id = $2 AND qbo_id = $3;

-- name: SoftDeleteVendor :exec
UPDATE shadow_erp.vendors
SET deleted_at = $1, updated_at = $1
WHERE realm_id = $2 AND qbo_id = $3;

-- name: SoftDeleteCustomer :exec
UPDATE shadow_erp.customers
SET deleted_at = $1, updated_at = $1
WHERE realm_id = $2 AND qbo_id = $3;

-- name: SoftDeleteInvoice :exec
UPDATE shadow_erp.invoices
SET deleted_at = $1, updated_at = $1
WHERE realm_id = $2 AND qbo_id = $3;

-- name: SoftDeleteBill :exec
UPDATE shadow_erp.bills
SET deleted_at = $1, updated_at = $1
WHERE realm_id = $2 AND qbo_id = $3;

-- =========================================================================
-- AI Vector Sync State
-- =========================================================================

-- name: GetVectorSyncState :one
SELECT * FROM shadow_erp.vector_sync_state
WHERE realm_id = $1;

-- name: UpsertVectorSyncState :exec
INSERT INTO shadow_erp.vector_sync_state (
    realm_id, last_coa_sync, coa_vector_count
)
VALUES ($1, $2, $3)
ON CONFLICT (realm_id) DO UPDATE SET
    last_coa_sync   = $2,
    coa_vector_count = $3,
    updated_at      = NOW();

-- name: UpdateVendorVectorSync :exec
UPDATE shadow_erp.vector_sync_state
SET last_vendor_sync = $2, vendor_vector_count = $3, updated_at = NOW()
WHERE realm_id = $1;

-- name: UpdateCustomerVectorSync :exec
UPDATE shadow_erp.vector_sync_state
SET last_customer_sync = $2, customer_vector_count = $3, updated_at = NOW()
WHERE realm_id = $1;

-- =========================================================================
-- AI Corrections
-- =========================================================================

-- name: RecordAICorrection :exec
INSERT INTO shadow_erp.ai_corrections (
    realm_id, user_id, raw_input, ai_prediction, user_correction, correction_type, confidence_score
)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetRecentCorrections :many
SELECT * FROM shadow_erp.ai_corrections
WHERE realm_id = $1 AND correction_type = $2
ORDER BY created_at DESC
LIMIT $3;

-- =========================================================================
-- Entity Read Queries
-- =========================================================================

-- name: GetVendorByNameOrSynonym :one
SELECT * FROM shadow_erp.vendors
WHERE realm_id = $1
  AND deleted_at IS NULL
  AND (
    display_name ILIKE $2
    OR ai_synonyms @> $3::jsonb
  )
LIMIT 1;

-- name: UpdateVendorSynonyms :exec
UPDATE shadow_erp.vendors
SET ai_synonyms = $3, updated_at = NOW()
WHERE realm_id = $1 AND id = $2;

-- name: UpdateVendorSynonymsByQBOID :exec
UPDATE shadow_erp.vendors
SET ai_synonyms = $3, updated_at = NOW()
WHERE realm_id = $1 AND qbo_id = $2;

-- name: GetAllAccountsForRealms :many
SELECT * FROM shadow_erp.accounts
WHERE realm_id = ANY(@realm_ids::text[]) AND deleted_at IS NULL
ORDER BY name ASC;

-- name: GetAllVendorsForRealms :many
SELECT * FROM shadow_erp.vendors
WHERE realm_id = ANY(@realm_ids::text[]) AND deleted_at IS NULL
ORDER BY display_name ASC;

-- name: GetAllCustomersForRealms :many
SELECT * FROM shadow_erp.customers
WHERE realm_id = ANY(@realm_ids::text[]) AND deleted_at IS NULL
ORDER BY display_name ASC;

-- name: GetCustomerByName :one
SELECT * FROM shadow_erp.customers
WHERE realm_id = $1
  AND deleted_at IS NULL
  AND display_name ILIKE $2
LIMIT 1;

-- name: GetAmbiguousProposals :many
SELECT * FROM shadow_erp.proposed_transactions
WHERE realm_id = $1
  AND confidence_score < $2
  AND sync_status = 'PENDING'
ORDER BY created_at DESC;

-- name: GetVendor :one
SELECT * FROM shadow_erp.vendors
WHERE realm_id = $1 AND id = $2;

-- name: GetVendorByQBOID :one
SELECT * FROM shadow_erp.vendors
WHERE realm_id = $1 AND qbo_id = $2;

-- name: GetAccountByQBOID :one
SELECT * FROM shadow_erp.accounts
WHERE realm_id = $1 AND qbo_id = $2;

-- name: GetCustomerByQBOID :one
SELECT * FROM shadow_erp.customers
WHERE realm_id = $1 AND qbo_id = $2;

-- name: GetAccountsUpdatedSince :many
SELECT * FROM shadow_erp.accounts
WHERE realm_id = $1 AND updated_at > $2 AND deleted_at IS NULL
ORDER BY updated_at ASC;

-- name: GetVendorsUpdatedSince :many
SELECT * FROM shadow_erp.vendors
WHERE realm_id = $1 AND updated_at > $2 AND deleted_at IS NULL
ORDER BY updated_at ASC;

-- name: GetCustomersUpdatedSince :many
SELECT * FROM shadow_erp.customers
WHERE realm_id = $1 AND updated_at > $2 AND deleted_at IS NULL
ORDER BY updated_at ASC;
