-- =========================================================================
-- QBO Connection / Token Management
-- =========================================================================

-- name: UpsertERPTokens :exec
-- Evict any connection the incoming entity already owns under a different realm
-- before upserting, so the UNIQUE(entity_id) constraint never blocks a transfer.
WITH evict AS (
    DELETE FROM toro_core.erp_connections
    WHERE entity_id = $1 AND realm_id != $2 AND erp_system = $3
)
INSERT INTO toro_core.erp_connections (entity_id, realm_id, erp_system, access_token, refresh_token, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (erp_system, realm_id)
DO UPDATE SET
    entity_id     = EXCLUDED.entity_id,
    access_token  = EXCLUDED.access_token,
    refresh_token = EXCLUDED.refresh_token,
    expires_at    = EXCLUDED.expires_at,
    updated_at    = NOW();

-- name: GetERPTokens :one
SELECT access_token, refresh_token, expires_at, entity_id
FROM toro_core.erp_connections
WHERE erp_system = $1 AND realm_id = $2;

-- name: UpdateERPTokens :exec
UPDATE toro_core.erp_connections
SET access_token = $3, refresh_token = $4, expires_at = $5, updated_at = NOW()
WHERE erp_system = $1 AND realm_id = $2;

-- name: GetERPConnection :one
SELECT * FROM toro_core.erp_connections
WHERE entity_id = $1;

-- name: GetERPConnectionByRealm :one
SELECT * FROM toro_core.erp_connections
WHERE erp_system = $1 AND realm_id = $2;

-- name: UpdateLastSyncTimestamp :exec
UPDATE toro_core.erp_connections
SET last_sync_timestamp = $3, updated_at = NOW()
WHERE erp_system = $1 AND realm_id = $2;

-- name: GetAllActiveConnections :many
SELECT erp_system, realm_id, entity_id, last_sync_timestamp
FROM toro_core.erp_connections
ORDER BY last_sync_timestamp ASC;

-- name: GetRealmsForEntities :many
SELECT DISTINCT realm_id FROM toro_core.erp_connections
WHERE entity_id = ANY(@authorized_entity_ids::uuid[])
  AND erp_system = 'quickbooks_online'
  AND access_token IS NOT NULL;

-- =========================================================================
-- Webhook Timestamp Tracking (for event-driven CDC)
-- =========================================================================

-- name: UpdateLastWebhookAccount :exec
UPDATE toro_core.erp_connections
SET last_webhook_account = $3, updated_at = NOW()
WHERE erp_system = $1 AND realm_id = $2;

-- name: UpdateLastWebhookVendor :exec
UPDATE toro_core.erp_connections
SET last_webhook_vendor = $3, updated_at = NOW()
WHERE erp_system = $1 AND realm_id = $2;

-- name: UpdateLastWebhookCustomer :exec
UPDATE toro_core.erp_connections
SET last_webhook_customer = $3, updated_at = NOW()
WHERE erp_system = $1 AND realm_id = $2;

-- name: UpdateLastWebhookInvoice :exec
UPDATE toro_core.erp_connections
SET last_webhook_invoice = $3, updated_at = NOW()
WHERE erp_system = $1 AND realm_id = $2;

-- name: UpdateLastWebhookBill :exec
UPDATE toro_core.erp_connections
SET last_webhook_bill = $3, updated_at = NOW()
WHERE erp_system = $1 AND realm_id = $2;

-- name: UpdateLastWebhookTransaction :exec
UPDATE toro_core.erp_connections
SET last_webhook_transaction = $3, updated_at = NOW()
WHERE erp_system = $1 AND realm_id = $2;

-- name: UpdateLastWebhookDeposit :exec
UPDATE toro_core.erp_connections
SET last_webhook_deposit = $3, updated_at = NOW()
WHERE erp_system = $1 AND realm_id = $2;

-- name: GetConnectionWithWebhookTimes :one
SELECT
    erp_system,
    realm_id,
    entity_id,
    last_sync_timestamp,
    last_webhook_account,
    last_webhook_vendor,
    last_webhook_customer,
    last_webhook_invoice,
    last_webhook_bill,
    last_webhook_transaction,
    last_webhook_deposit
FROM toro_core.erp_connections
WHERE erp_system = $1 AND realm_id = $2;

-- =========================================================================
-- shadow_erp Entity Queries (Upsert / Soft-Delete)
-- =========================================================================

-- name: UpsertAccount :exec
INSERT INTO shadow_erp.accounts (
    erp_id, realm_id, name, account_type, account_sub_type, classification,
    fully_qualified_name, active, sync_token,
    domain, currency_ref_name, currency_ref_value, current_balance_with_sub_accounts,
    sparse, erp_created_time, erp_updated_time, current_balance, sub_account,
    event_source, created_at, updated_at
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9,
    $10, $11, $12, $13, $14, $15, $16, $17, $18,
    'erp_sync', NOW(), NOW()
)
ON CONFLICT (realm_id, erp_id) DO UPDATE SET
    name                 = EXCLUDED.name,
    account_type         = EXCLUDED.account_type,
    account_sub_type     = EXCLUDED.account_sub_type,
    classification       = EXCLUDED.classification,
    fully_qualified_name = EXCLUDED.fully_qualified_name,
    active               = EXCLUDED.active,
    sync_token           = EXCLUDED.sync_token,
    domain               = EXCLUDED.domain,
    currency_ref_name    = EXCLUDED.currency_ref_name,
    currency_ref_value   = EXCLUDED.currency_ref_value,
    current_balance_with_sub_accounts = EXCLUDED.current_balance_with_sub_accounts,
    sparse               = EXCLUDED.sparse,
    erp_created_time     = EXCLUDED.erp_created_time,
    erp_updated_time     = EXCLUDED.erp_updated_time,
    current_balance      = EXCLUDED.current_balance,
    sub_account          = EXCLUDED.sub_account,
    event_source         = 'erp_sync',
    updated_at           = NOW(),
    deleted_at           = NULL;

-- name: UpsertVendor :exec
INSERT INTO shadow_erp.vendors (
    erp_id, realm_id, display_name, sync_token, last_known_account_id,
    ai_synonyms, event_source, created_at, updated_at
)
VALUES (
    $1, $2, $3, $4,
    (SELECT id FROM shadow_erp.accounts WHERE shadow_erp.accounts.erp_id = sqlc.narg('last_known_account_erp_id') AND shadow_erp.accounts.realm_id = $2),
    $5, 'erp_sync', NOW(), NOW()
)
ON CONFLICT (realm_id, erp_id) DO UPDATE SET
    display_name          = EXCLUDED.display_name,
    sync_token            = EXCLUDED.sync_token,
    last_known_account_id = EXCLUDED.last_known_account_id,
    ai_synonyms           = EXCLUDED.ai_synonyms,
    event_source          = 'erp_sync',
    updated_at            = NOW(),
    deleted_at            = NULL;

-- name: UpsertCustomer :exec
INSERT INTO shadow_erp.customers (
    erp_id, realm_id, display_name, sync_token, event_source, created_at, updated_at
)
VALUES ($1, $2, $3, $4, 'erp_sync', NOW(), NOW())
ON CONFLICT (realm_id, erp_id) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    sync_token   = EXCLUDED.sync_token,
    event_source = 'erp_sync',
    updated_at   = NOW(),
    deleted_at   = NULL;

-- name: UpsertInvoice :exec
INSERT INTO shadow_erp.invoices (
    erp_id, realm_id, customer_id, doc_number, total_amount, balance,
    due_date, txn_date, sync_token, event_source, created_at, updated_at
)
VALUES (
    $1, $2,
    (SELECT id FROM shadow_erp.customers WHERE shadow_erp.customers.erp_id = sqlc.narg('customer_erp_id') AND shadow_erp.customers.realm_id = $2),
    $3, $4, $5, $6, $7, $8, 'erp_sync', NOW(), NOW()
)
ON CONFLICT (realm_id, erp_id) DO UPDATE SET
    customer_id  = EXCLUDED.customer_id,
    doc_number   = EXCLUDED.doc_number,
    total_amount = EXCLUDED.total_amount,
    balance      = EXCLUDED.balance,
    due_date     = EXCLUDED.due_date,
    txn_date     = EXCLUDED.txn_date,
    sync_token   = EXCLUDED.sync_token,
    event_source = 'erp_sync',
    updated_at   = NOW(),
    deleted_at   = NULL;

-- name: UpsertBill :exec
INSERT INTO shadow_erp.bills (
    erp_id, realm_id, vendor_id, doc_number, total_amount, balance,
    due_date, txn_date, sync_token, event_source, created_at, updated_at
)
VALUES (
    $1, $2,
    (SELECT id FROM shadow_erp.vendors WHERE shadow_erp.vendors.erp_id = sqlc.narg('vendor_erp_id') AND shadow_erp.vendors.realm_id = $2),
    $3, $4, $5, $6, $7, $8, 'erp_sync', NOW(), NOW()
)
ON CONFLICT (realm_id, erp_id) DO UPDATE SET
    vendor_id    = EXCLUDED.vendor_id,
    doc_number   = EXCLUDED.doc_number,
    total_amount = EXCLUDED.total_amount,
    balance      = EXCLUDED.balance,
    due_date     = EXCLUDED.due_date,
    txn_date     = EXCLUDED.txn_date,
    sync_token   = EXCLUDED.sync_token,
    event_source = 'erp_sync',
    updated_at   = NOW(),
    deleted_at   = NULL;

-- name: SoftDeleteAccount :exec
UPDATE shadow_erp.accounts
SET deleted_at = $1, updated_at = $1, event_source = 'erp_sync'
WHERE realm_id = $2 AND erp_id = $3;

-- name: SoftDeleteVendor :exec
UPDATE shadow_erp.vendors
SET deleted_at = $1, updated_at = $1, event_source = 'erp_sync'
WHERE realm_id = $2 AND erp_id = $3;

-- name: SoftDeleteCustomer :exec
UPDATE shadow_erp.customers
SET deleted_at = $1, updated_at = $1, event_source = 'erp_sync'
WHERE realm_id = $2 AND erp_id = $3;

-- name: SoftDeleteInvoice :exec
UPDATE shadow_erp.invoices
SET deleted_at = $1, updated_at = $1, event_source = 'erp_sync'
WHERE realm_id = $2 AND erp_id = $3;

-- name: SoftDeleteBill :exec
UPDATE shadow_erp.bills
SET deleted_at = $1, updated_at = $1, event_source = 'erp_sync'
WHERE realm_id = $2 AND erp_id = $3;

-- name: UpsertPurchase :exec
INSERT INTO shadow_erp.purchases (
    erp_id, realm_id, sync_token, txn_date, total_amount, payment_type,
    source_account_id, entity_id, lines,
    event_source, created_at, updated_at
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9,
    'erp_sync', NOW(), NOW()
)
ON CONFLICT (realm_id, erp_id) DO UPDATE SET
    sync_token        = EXCLUDED.sync_token,
    txn_date          = EXCLUDED.txn_date,
    total_amount      = EXCLUDED.total_amount,
    payment_type      = EXCLUDED.payment_type,
    source_account_id = EXCLUDED.source_account_id,
    entity_id         = EXCLUDED.entity_id,
    lines             = EXCLUDED.lines,
    event_source      = 'erp_sync',
    updated_at        = NOW(),
    deleted_at        = NULL;

-- name: UpsertDeposit :exec
INSERT INTO shadow_erp.deposits (
    erp_id, realm_id, sync_token, txn_date, total_amount, target_account_id, lines,
    event_source, created_at, updated_at
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    'erp_sync', NOW(), NOW()
)
ON CONFLICT (realm_id, erp_id) DO UPDATE SET
    sync_token        = EXCLUDED.sync_token,
    txn_date          = EXCLUDED.txn_date,
    total_amount      = EXCLUDED.total_amount,
    target_account_id = EXCLUDED.target_account_id,
    lines             = EXCLUDED.lines,
    event_source      = 'erp_sync',
    updated_at        = NOW(),
    deleted_at        = NULL;

-- name: SoftDeletePurchase :exec
UPDATE shadow_erp.purchases
SET deleted_at = $1, updated_at = $1, event_source = 'erp_sync'
WHERE realm_id = $2 AND erp_id = $3;

-- name: SoftDeleteDeposit :exec
UPDATE shadow_erp.deposits
SET deleted_at = $1, updated_at = $1, event_source = 'erp_sync'
WHERE realm_id = $2 AND erp_id = $3;

-- name: GetPurchaseByERPID :one
SELECT * FROM shadow_erp.purchases
WHERE realm_id = $1 AND erp_id = $2;

-- name: GetDepositByERPID :one
SELECT * FROM shadow_erp.deposits
WHERE realm_id = $1 AND erp_id = $2;

-- name: GetExpenseAccountsFromPurchases :many
SELECT 
    erp_id AS transaction_id,
    source_account_id AS paid_from_bank_account,
    -- This extracts the second AccountRef (The Expense Category)
    jsonb_array_elements(lines)->'AccountBasedExpenseLineDetail'->'AccountRef'->>'value' AS expense_account_id
FROM shadow_erp.purchases
WHERE realm_id = $1;

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

-- name: UpdateVendorSynonymsByERPID :exec
UPDATE shadow_erp.vendors
SET ai_synonyms = $3, updated_at = NOW()
WHERE realm_id = $1 AND erp_id = $2;

-- name: GetAllAccountsForRealms :many
SELECT * FROM shadow_erp.accounts
WHERE realm_id = ANY(@realm_ids::text[]) AND deleted_at IS NULL
ORDER BY name ASC;

-- name: GetAccountsByRealm :many
SELECT * FROM shadow_erp.accounts
WHERE realm_id = $1 AND deleted_at IS NULL
ORDER BY name ASC;

-- name: GetAllVendorsForRealms :many
SELECT * FROM shadow_erp.vendors
WHERE realm_id = ANY(@realm_ids::text[]) AND deleted_at IS NULL
ORDER BY display_name ASC;

-- name: GetVendorsByRealm :many
SELECT * FROM shadow_erp.vendors
WHERE realm_id = $1 AND deleted_at IS NULL
ORDER BY display_name ASC;

-- name: GetAllCustomersForRealms :many
SELECT * FROM shadow_erp.customers
WHERE realm_id = ANY(@realm_ids::text[]) AND deleted_at IS NULL
ORDER BY display_name ASC;

-- name: GetCustomersByRealm :many
SELECT * FROM shadow_erp.customers
WHERE realm_id = $1 AND deleted_at IS NULL
ORDER BY display_name ASC;

-- name: GetCustomerByName :one
SELECT * FROM shadow_erp.customers
WHERE realm_id = $1
  AND deleted_at IS NULL
  AND display_name ILIKE $2
LIMIT 1;

-- name: GetAmbiguousProposals :many
SELECT * FROM fignode.staging_transactions
WHERE realm_id = $1
  AND confidence_score < $2
  AND status = 'PENDING'
ORDER BY created_at DESC;

-- name: GetVendor :one
SELECT * FROM shadow_erp.vendors
WHERE realm_id = $1 AND id = $2;

-- name: GetVendorByERPID :one
SELECT * FROM shadow_erp.vendors
WHERE realm_id = $1 AND erp_id = $2;

-- name: GetAccountByERPID :one
SELECT * FROM shadow_erp.accounts
WHERE realm_id = $1 AND erp_id = $2;

-- name: GetCustomerByERPID :one
SELECT * FROM shadow_erp.customers
WHERE realm_id = $1 AND erp_id = $2;

-- name: GetInvoiceByERPID :one
SELECT * FROM shadow_erp.invoices
WHERE realm_id = $1 AND erp_id = $2;

-- name: GetBillByERPID :one
SELECT * FROM shadow_erp.bills
WHERE realm_id = $1 AND erp_id = $2;

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

-- =========================================================================
-- Transaction Proposal & Audit
-- =========================================================================

-- name: GetUnifiedTransactions :many
-- Retrieves a unified view of all transactions (Bills and Invoices) for a given realm,
-- including the vendor/customer names.
SELECT 
    'Bill' as source_type,
    b.id,
    b.erp_id,
    b.realm_id,
    b.vendor_id as entity_id,
    v.display_name as entity_name,
    b.doc_number,
    b.total_amount,
    b.balance,
    b.due_date,
    b.txn_date,
    b.sync_token,
    b.created_at,
    b.updated_at,
    b.deleted_at
FROM shadow_erp.bills b
LEFT JOIN shadow_erp.vendors v ON b.vendor_id = v.id
WHERE b.realm_id = $1

UNION ALL

SELECT 
    'Invoice' as source_type,
    i.id,
    i.erp_id,
    i.realm_id,
    i.customer_id as entity_id,
    c.display_name as entity_name,
    i.doc_number,
    i.total_amount,
    i.balance,
    i.due_date,
    i.txn_date,
    i.sync_token,
    i.created_at,
    i.updated_at,
    i.deleted_at
FROM shadow_erp.invoices i
LEFT JOIN shadow_erp.customers c ON i.customer_id = c.id
WHERE i.realm_id = $1

ORDER BY txn_date DESC, created_at DESC;

-- name: GetProposedTransactionByValues :one
SELECT * FROM fignode.staging_transactions
WHERE realm_id = $1
  AND predicted_vendor_id = $2
  AND raw_date = $3
  AND raw_amount = $4
LIMIT 1;

-- name: CreateProposedTransaction :one
INSERT INTO fignode.staging_transactions (
    realm_id, source_type, raw_amount, raw_date, raw_description,
    predicted_vendor_id, predicted_account_id, confidence_score,
    ai_reasoning, status, created_at, updated_at
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW()
)
RETURNING *;

-- name: UpdateProposedTransactionSyncStatus :exec
UPDATE fignode.staging_transactions
SET status = $2, erp_transaction_id = $3, error_message = $4,
    updated_at = NOW()
WHERE id = $1;

-- name: GetProposedTransactionByID :one
SELECT * FROM fignode.staging_transactions WHERE id = $1;

-- name: ApproveProposedTransaction :one
UPDATE fignode.staging_transactions
SET predicted_account_id = $2,
    predicted_vendor_id  = $3,
    status               = 'APPROVED',
    updated_at           = NOW()
WHERE id = $1
RETURNING *;

-- name: UpsertStagingTransaction :exec
INSERT INTO fignode.staging_transactions (
    realm_id,
    erp_transaction_id,
    source_type,
    raw_amount,
    raw_date,
    raw_description,
    predicted_vendor_id,
    predicted_account_id,
    status
)
VALUES (
    $1, $2, $3, $4, $5, $6,
    (SELECT id FROM shadow_erp.vendors WHERE shadow_erp.vendors.erp_id = $7 AND shadow_erp.vendors.realm_id = $1),
    (SELECT id FROM shadow_erp.accounts WHERE shadow_erp.accounts.erp_id = $8 AND shadow_erp.accounts.realm_id = $1),
    $9
)
ON CONFLICT (realm_id, erp_transaction_id) DO UPDATE SET
    source_type = EXCLUDED.source_type,
    raw_amount = EXCLUDED.raw_amount,
    raw_date = EXCLUDED.raw_date,
    raw_description = EXCLUDED.raw_description,
    predicted_vendor_id = EXCLUDED.predicted_vendor_id,
    predicted_account_id = EXCLUDED.predicted_account_id,
    status = EXCLUDED.status,
    updated_at = NOW();

-- =========================================================================
-- Company Info
-- =========================================================================

-- name: UpsertCompanyInfo :exec
INSERT INTO shadow_erp.company_info (
    realm_id, erp_id, sync_token, company_name, legal_name, domain, country,
    fiscal_year_start_month, company_start_date, supported_languages,
    company_addr, legal_addr, primary_phone, email, web_addr, name_values,
    erp_created_time, erp_updated_time, event_source, created_at, updated_at
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18,
    'erp_sync', NOW(), NOW()
)
ON CONFLICT (realm_id) DO UPDATE SET
    erp_id                  = EXCLUDED.erp_id,
    sync_token              = EXCLUDED.sync_token,
    company_name            = EXCLUDED.company_name,
    legal_name              = EXCLUDED.legal_name,
    domain                  = EXCLUDED.domain,
    country                 = EXCLUDED.country,
    fiscal_year_start_month = EXCLUDED.fiscal_year_start_month,
    company_start_date      = EXCLUDED.company_start_date,
    supported_languages     = EXCLUDED.supported_languages,
    company_addr            = EXCLUDED.company_addr,
    legal_addr              = EXCLUDED.legal_addr,
    primary_phone           = EXCLUDED.primary_phone,
    email                   = EXCLUDED.email,
    web_addr                = EXCLUDED.web_addr,
    name_values             = EXCLUDED.name_values,
    erp_created_time        = EXCLUDED.erp_created_time,
    erp_updated_time        = EXCLUDED.erp_updated_time,
    event_source            = 'erp_sync',
    updated_at              = NOW();

-- name: GetCompanyInfo :one
SELECT * FROM shadow_erp.company_info
WHERE realm_id = $1;

-- name: UpdateCompanyTaxonomy :exec
UPDATE shadow_erp.company_info
SET industry = $2, industry_icon = $3, business_model = $4, mindset_hint = $5
WHERE realm_id = $1;

-- name: UpdateVendorTaxonomy :exec
UPDATE shadow_erp.vendors
SET industry = $3, industry_icon = $4, vendor_description = $5, vendor_url = $6
WHERE realm_id = $1 AND erp_id = $2;

-- name: GetCustomerByID :one
SELECT * FROM shadow_erp.customers
WHERE id = $1;

-- name: UpdateCustomerTaxonomy :exec
UPDATE shadow_erp.customers
SET industry = $3, industry_icon = $4, customer_description = $5, customer_url = $6
WHERE realm_id = $1 AND erp_id = $2;

-- name: GetFilteredAccountsForAI :many
SELECT erp_id, name, account_sub_type 
FROM shadow_erp.accounts 
WHERE realm_id = $1 
  AND classification = $2 
  AND account_type = $3 
  AND active = true 
  AND deleted_at IS NULL;

-- =========================================================================
-- Rule engine Bootstrapper Queries
-- =========================================================================

-- name: GetHistoricalSplitters :many
-- Finds vendors where >= 50% of their historical transactions had multiple expense lines.
WITH VendorSplitStats AS (
    SELECT 
        entity_id,
        COUNT(*) AS total_txns,
        SUM(CASE WHEN jsonb_array_length(lines) > 1 THEN 1 ELSE 0 END) AS split_count
    FROM shadow_erp.purchases
    WHERE realm_id = $1 AND entity_id IS NOT NULL AND deleted_at IS NULL
    GROUP BY entity_id
)
SELECT entity_id, total_txns, split_count
FROM VendorSplitStats
WHERE split_count >= 2 AND (split_count::decimal / total_txns) >= 0.5;

-- name: GetHistoricalPurchaseConsensus :many
-- Finds the #1 most frequently used expense account for a (vendor, source_account) pair.
WITH ExtractedLines AS (
    SELECT 
        entity_id,
        source_account_id,
        jsonb_array_elements(lines)->'AccountBasedExpenseLineDetail'->'AccountRef'->>'value' AS target_account_id
    FROM shadow_erp.purchases
    WHERE realm_id = $1 AND entity_id IS NOT NULL AND deleted_at IS NULL
),
RankedMappings AS (
    SELECT 
        entity_id,
        source_account_id,
        target_account_id,
        COUNT(*) as usage_count,
        ROW_NUMBER() OVER(PARTITION BY entity_id, source_account_id ORDER BY COUNT(*) DESC) as rank
    FROM ExtractedLines
    WHERE target_account_id IS NOT NULL
    GROUP BY entity_id, source_account_id, target_account_id
)
SELECT entity_id, source_account_id, target_account_id, usage_count
FROM RankedMappings
WHERE rank = sqlc.arg('target_rank')::int 
  AND usage_count >= sqlc.arg('min_usage_count')::bigint;

-- name: GetHistoricalDepositSplitters :many
-- Flags customers where >= 50% of their historical deposits were split across multiple income accounts.
WITH RawLines AS (
    SELECT 
        erp_id AS deposit_id,
        jsonb_array_elements(lines) AS line
    FROM shadow_erp.deposits
    WHERE realm_id = $1 AND deleted_at IS NULL
),
DepositLines AS (
    SELECT 
        deposit_id,
        COALESCE(
            line->'DepositLineDetail'->'Entity'->'EntityRef'->>'value',
            line->'DepositLineDetail'->'Entity'->>'value'
        )::text AS customer_id,
        line->'DepositLineDetail'->'AccountRef'->>'value'::text AS income_account_id
    FROM RawLines
),
CustomerDepositCounts AS (
    SELECT 
        customer_id,
        COUNT(DISTINCT deposit_id) as total_txns,
        SUM(CASE WHEN lines_in_deposit > 1 THEN 1 ELSE 0 END) as split_count
    FROM (
        SELECT customer_id, deposit_id, COUNT(*) as lines_in_deposit
        FROM DepositLines
        WHERE customer_id IS NOT NULL AND income_account_id IS NOT NULL
        GROUP BY customer_id, deposit_id
    ) sub
    GROUP BY customer_id
)
SELECT customer_id, total_txns, split_count
FROM CustomerDepositCounts
WHERE split_count >= 2 AND (split_count::decimal / total_txns) >= 0.5;

-- name: GetHistoricalDepositConsensus :many
-- Finds the #1 most frequently used Income Account for a (customer, bank_account) pair.
WITH RawLines AS (
    SELECT 
        target_account_id AS bank_account_id,
        jsonb_array_elements(lines) AS line
    FROM shadow_erp.deposits
    WHERE realm_id = $1 AND deleted_at IS NULL
),
DepositLines AS (
    SELECT 
        bank_account_id,
        COALESCE(
            line->'DepositLineDetail'->'Entity'->'EntityRef'->>'value',
            line->'DepositLineDetail'->'Entity'->>'value'
        )::text AS customer_id,
        line->'DepositLineDetail'->'AccountRef'->>'value'::text AS income_account_id
    FROM RawLines
),
RankedMappings AS (
    SELECT 
        customer_id,
        bank_account_id,
        income_account_id,
        COUNT(*) as usage_count,
        ROW_NUMBER() OVER(PARTITION BY customer_id, bank_account_id ORDER BY COUNT(*) DESC) as rank
    FROM DepositLines
    WHERE customer_id IS NOT NULL AND income_account_id IS NOT NULL
    GROUP BY customer_id, bank_account_id, income_account_id
)
SELECT customer_id, bank_account_id, income_account_id, usage_count
FROM RankedMappings
WHERE rank = sqlc.arg('target_rank')::int 
  AND usage_count >= sqlc.arg('min_usage_count')::bigint;

-- =========================================================================
-- Rule Execution & Cleanup
-- =========================================================================

-- name: GetActiveRealms :many
SELECT DISTINCT realm_id FROM toro_core.erp_connections;

-- name: GetOrphanedPurchases :many
SELECT p.*, v.display_name AS vendor_name, a.name AS source_account_name
FROM shadow_erp.purchases p
LEFT JOIN shadow_erp.vendors v ON v.erp_id = p.entity_id AND v.realm_id = p.realm_id
LEFT JOIN shadow_erp.accounts a ON a.erp_id = p.source_account_id AND a.realm_id = p.realm_id
WHERE p.realm_id = $1 AND p.rule_id IS NULL AND p.deleted_at IS NULL;

-- name: GetOrphanedDeposits :many
SELECT d.*, c.display_name AS customer_name, a.name AS source_account_name
FROM shadow_erp.deposits d
LEFT JOIN LATERAL (
    SELECT COALESCE(
        l->'DepositLineDetail'->'Entity'->'EntityRef'->>'value',
        l->'DepositLineDetail'->'Entity'->>'value'
    ) AS customer_id
    FROM jsonb_array_elements(d.lines) l
    LIMIT 1
) AS loc ON true
LEFT JOIN shadow_erp.customers c ON c.realm_id = d.realm_id AND c.erp_id = loc.customer_id
LEFT JOIN shadow_erp.accounts a ON a.erp_id = d.target_account_id AND a.realm_id = d.realm_id
WHERE d.realm_id = $1 AND d.rule_id IS NULL AND d.deleted_at IS NULL;

-- name: UpdatePurchaseRuleID :exec
UPDATE shadow_erp.purchases
SET rule_id = $2, updated_at = NOW()
WHERE id = $1;

-- name: UpdateDepositRuleID :exec
UPDATE shadow_erp.deposits
SET rule_id = $2, updated_at = NOW()
WHERE id = $1;
