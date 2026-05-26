-- =========================================================================
-- Cleanup Mode Queries (now stored in fignode schema)
-- =========================================================================

-- name: CreateCleanupSession :one
INSERT INTO fignode.staging_sessions (realm_id, bank_account_id, kind, created_by, file_name, row_count, status, outflow_is)
VALUES (sqlc.narg('realm_id'), sqlc.narg('bank_account_id'), 'CSV', $1, $2, $3, 'PENDING', $4)
RETURNING id, realm_id, bank_account_id, kind, created_by, file_name, row_count, status, outflow_is, is_ambiguous, ambiguity_reason, created_at, updated_at;

-- name: GetCleanupSession :one
SELECT id, realm_id, bank_account_id, kind, created_by, file_name, row_count, status, outflow_is, is_ambiguous, ambiguity_reason, created_at, updated_at
FROM fignode.staging_sessions
WHERE id = $1;

-- name: ListCleanupSessions :many
-- Returns CSV sessions for a realm (when realm_id is provided) OR CSV sessions created by a user
-- (when realm_id is NULL). Excludes SYSTEM/PLAID sessions which are not user-facing.
SELECT id, realm_id, bank_account_id, kind, created_by, file_name, row_count, status, outflow_is, is_ambiguous, ambiguity_reason, created_at, updated_at
FROM fignode.staging_sessions
WHERE kind = 'CSV'
  AND (sqlc.narg('realm_id')::TEXT IS NULL OR realm_id = sqlc.narg('realm_id')::TEXT)
  AND (sqlc.narg('created_by')::UUID IS NULL OR created_by = sqlc.narg('created_by')::UUID)
ORDER BY created_at DESC;

-- name: GetOrCreateSystemSession :one
-- Returns the SYSTEM session for a given realm, creating it if it does not exist.
-- Used by non-CSV transaction stagers (rule engine, Plaid webhooks) to satisfy the
-- session_id linkage now that realm_id has been removed from staging_transactions.
INSERT INTO fignode.staging_sessions (realm_id, kind, status)
VALUES ($1, 'SYSTEM', 'ACTIVE')
ON CONFLICT (realm_id) WHERE kind = 'SYSTEM' AND realm_id IS NOT NULL
DO UPDATE SET updated_at = NOW()
RETURNING id;

-- name: UpdateCleanupSessionStatus :exec
UPDATE fignode.staging_sessions
SET status = $2, updated_at = NOW()
WHERE id = $1;

-- name: MarkCleanupSessionAmbiguous :exec
UPDATE fignode.staging_sessions
SET is_ambiguous = $2, ambiguity_reason = $3, updated_at = NOW()
WHERE id = $1;

-- name: UpdateCleanupSessionRowCount :exec
UPDATE fignode.staging_sessions
SET row_count = $2, updated_at = NOW()
WHERE id = $1;

-- name: UpdateCleanupSessionBankAccount :exec
UPDATE fignode.staging_sessions
SET bank_account_id = $2, updated_at = NOW()
WHERE id = $1;

-- name: InsertCleanupRow :one
INSERT INTO fignode.staging_transactions (
    session_id, row_index, source_type, raw_description, raw_amount, raw_date, parsed_date, status,
    predicted_vendor_id, predicted_vendor_name, predicted_customer_id, predicted_customer_name, predicted_account_id, predicted_account_name,
    confidence_score, ai_reasoning, duplicate_of, is_recurring, split_suggestion,
    human_action, swiped_by, swiped_at, override_vendor_id, override_customer_id, override_account_id,
    erp_transaction_id, error_message, transaction_id, pending_transaction_id,
    merchant_name, logo_url, category, is_pending
)
VALUES (
    $1, sqlc.narg('row_index'), $2, $3, $4, $5, sqlc.narg('parsed_date'), COALESCE(sqlc.narg('status'), 'PENDING'),
    sqlc.narg('predicted_vendor_id'), sqlc.narg('predicted_vendor_name'), sqlc.narg('predicted_customer_id'), sqlc.narg('predicted_customer_name'), sqlc.narg('predicted_account_id'), sqlc.narg('predicted_account_name'),
    sqlc.narg('confidence_score'), sqlc.narg('ai_reasoning'), sqlc.narg('duplicate_of'), COALESCE(sqlc.narg('is_recurring'), FALSE), sqlc.narg('split_suggestion'),
    sqlc.narg('human_action'), sqlc.narg('swiped_by'), sqlc.narg('swiped_at'), sqlc.narg('override_vendor_id'), sqlc.narg('override_customer_id'), sqlc.narg('override_account_id'),
    sqlc.narg('erp_transaction_id'), sqlc.narg('error_message'), sqlc.narg('transaction_id'), sqlc.narg('pending_transaction_id'),
    sqlc.narg('merchant_name'), sqlc.narg('logo_url'), sqlc.narg('category'), COALESCE(sqlc.narg('is_pending'), FALSE)
)
RETURNING id;

-- name: GetPendingSessionRows :many
SELECT cs.id, cs.session_id, ss.realm_id, ss.bank_account_id, ss.outflow_is, cs.source_type, cs.raw_description, cs.raw_amount, cs.raw_date, cs.parsed_date,
       cs.predicted_vendor_id, cs.predicted_customer_id, cs.predicted_account_id,
       cs.confidence_score, cs.ai_reasoning,
       cs.duplicate_of, cs.is_recurring, cs.split_suggestion,
       cs.override_vendor_id, cs.override_customer_id, cs.override_account_id,
       cs.merchant_name, cs.category,
       cs.status, cs.erp_transaction_id, cs.created_at, cs.updated_at,
       COALESCE(v.display_name, cs.predicted_vendor_name, '') AS predicted_vendor_name,
       COALESCE(c.display_name, cs.predicted_customer_name, '') AS predicted_customer_name,
       a.name         AS predicted_account_name,
       a.account_type AS predicted_account_type,
       ov.display_name AS override_vendor_name,
       oc.display_name AS override_customer_name,
       oa.name         AS override_account_name
FROM fignode.staging_transactions cs
LEFT JOIN fignode.staging_sessions ss ON ss.id = cs.session_id
LEFT JOIN shadow_erp.vendors  v  ON v.id  = cs.predicted_vendor_id
LEFT JOIN shadow_erp.customers c ON c.id  = cs.predicted_customer_id
LEFT JOIN shadow_erp.accounts a  ON a.id  = cs.predicted_account_id
LEFT JOIN shadow_erp.vendors  ov ON ov.id = cs.override_vendor_id
LEFT JOIN shadow_erp.customers oc ON oc.id = cs.override_customer_id
LEFT JOIN shadow_erp.accounts oa ON oa.id = cs.override_account_id
WHERE cs.session_id = $1 AND cs.status = 'PENDING'
ORDER BY cs.parsed_date ASC NULLS LAST, cs.id ASC;

-- name: GetPendingRealmRows :many
SELECT cs.id, cs.session_id, ss.realm_id, ss.bank_account_id, ss.outflow_is, cs.source_type, cs.raw_description, cs.raw_amount, cs.raw_date, cs.parsed_date,
       cs.predicted_vendor_id, cs.predicted_customer_id, cs.predicted_account_id,
       cs.confidence_score, cs.ai_reasoning,
       cs.duplicate_of, cs.is_recurring, cs.split_suggestion,
       cs.override_vendor_id, cs.override_customer_id, cs.override_account_id,
       cs.merchant_name, cs.category,
       cs.status, cs.erp_transaction_id, cs.created_at, cs.updated_at,
       COALESCE(v.display_name, cs.predicted_vendor_name, '') AS predicted_vendor_name,
       COALESCE(c.display_name, cs.predicted_customer_name, '') AS predicted_customer_name,
       a.name         AS predicted_account_name,
       a.account_type AS predicted_account_type,
       ov.display_name AS override_vendor_name,
       oc.display_name AS override_customer_name,
       oa.name         AS override_account_name
FROM fignode.staging_transactions cs
JOIN fignode.staging_sessions ss ON ss.id = cs.session_id
LEFT JOIN shadow_erp.vendors  v  ON v.id  = cs.predicted_vendor_id
LEFT JOIN shadow_erp.customers c ON c.id  = cs.predicted_customer_id
LEFT JOIN shadow_erp.accounts a  ON a.id  = cs.predicted_account_id
LEFT JOIN shadow_erp.vendors  ov ON ov.id = cs.override_vendor_id
LEFT JOIN shadow_erp.customers oc ON oc.id = cs.override_customer_id
LEFT JOIN shadow_erp.accounts oa ON oa.id = cs.override_account_id
WHERE ss.realm_id = $1 AND cs.status = 'PENDING'
ORDER BY cs.parsed_date ASC NULLS LAST
LIMIT $2;

-- name: GetSessionRows :many
SELECT cs.id, cs.session_id, ss.realm_id, ss.bank_account_id, ss.outflow_is, cs.source_type, cs.raw_description, cs.raw_amount, cs.raw_date, cs.parsed_date,
       cs.predicted_vendor_id, cs.predicted_customer_id, cs.predicted_account_id,
       cs.confidence_score, cs.ai_reasoning,
       cs.duplicate_of, cs.is_recurring, cs.split_suggestion,
       cs.override_vendor_id, cs.override_customer_id, cs.override_account_id,
       cs.merchant_name, cs.category,
       cs.status, cs.erp_transaction_id, cs.created_at, cs.updated_at,
       COALESCE(v.display_name, cs.predicted_vendor_name, '') AS predicted_vendor_name,
       COALESCE(c.display_name, cs.predicted_customer_name, '') AS predicted_customer_name,
       a.name         AS predicted_account_name,
       a.account_type AS predicted_account_type,
       ov.display_name AS override_vendor_name,
       oc.display_name AS override_customer_name,
       oa.name         AS override_account_name
FROM fignode.staging_transactions cs
LEFT JOIN fignode.staging_sessions ss ON ss.id = cs.session_id
LEFT JOIN shadow_erp.vendors  v  ON v.id  = cs.predicted_vendor_id
LEFT JOIN shadow_erp.customers c ON c.id  = cs.predicted_customer_id
LEFT JOIN shadow_erp.accounts a  ON a.id  = cs.predicted_account_id
LEFT JOIN shadow_erp.vendors  ov ON ov.id = cs.override_vendor_id
LEFT JOIN shadow_erp.customers oc ON oc.id = cs.override_customer_id
LEFT JOIN shadow_erp.accounts oa ON oa.id = cs.override_account_id
WHERE cs.session_id = $1
  AND (sqlc.narg('status')::TEXT IS NULL OR cs.status = sqlc.narg('status')::TEXT)
ORDER BY cs.parsed_date ASC NULLS LAST, cs.id ASC;

-- name: GetSessionRowsPaginated :many
SELECT cs.id, cs.session_id, ss.realm_id, ss.bank_account_id, ss.outflow_is, cs.source_type, cs.raw_description, cs.raw_amount, cs.raw_date, cs.parsed_date,
       cs.predicted_vendor_id, cs.predicted_customer_id, cs.predicted_account_id,
       cs.confidence_score, cs.ai_reasoning,
       cs.duplicate_of, cs.is_recurring, cs.split_suggestion,
       cs.override_vendor_id, cs.override_customer_id, cs.override_account_id,
       cs.merchant_name, cs.category,
       cs.status, cs.erp_transaction_id, cs.created_at, cs.updated_at,
       COALESCE(v.display_name, cs.predicted_vendor_name, '') AS predicted_vendor_name,
       COALESCE(c.display_name, cs.predicted_customer_name, '') AS predicted_customer_name,
       a.name         AS predicted_account_name,
       a.account_type AS predicted_account_type,
       ov.display_name AS override_vendor_name,
       oc.display_name AS override_customer_name,
       oa.name         AS override_account_name
FROM fignode.staging_transactions cs
LEFT JOIN fignode.staging_sessions ss ON ss.id = cs.session_id
LEFT JOIN shadow_erp.vendors  v  ON v.id  = cs.predicted_vendor_id
LEFT JOIN shadow_erp.customers c ON c.id  = cs.predicted_customer_id
LEFT JOIN shadow_erp.accounts a  ON a.id  = cs.predicted_account_id
LEFT JOIN shadow_erp.vendors  ov ON ov.id = cs.override_vendor_id
LEFT JOIN shadow_erp.customers oc ON oc.id = cs.override_customer_id
LEFT JOIN shadow_erp.accounts oa ON oa.id = cs.override_account_id
WHERE cs.session_id = $1
  AND (sqlc.narg('status')::TEXT IS NULL OR cs.status = sqlc.narg('status')::TEXT)
ORDER BY cs.parsed_date ASC NULLS LAST, cs.id ASC
LIMIT $2 OFFSET $3;

-- name: CountSessionRows :one
SELECT COUNT(*)
FROM fignode.staging_transactions cs
WHERE cs.session_id = $1
  AND (sqlc.narg('status')::TEXT IS NULL OR cs.status = sqlc.narg('status')::TEXT);

-- name: GetCleanupRow :one
SELECT cs.id, cs.session_id, ss.realm_id, ss.bank_account_id, ss.outflow_is, cs.source_type, cs.raw_description, cs.raw_amount, cs.raw_date, cs.parsed_date,
       cs.predicted_vendor_id, cs.predicted_customer_id, cs.predicted_account_id,
       cs.confidence_score, cs.ai_reasoning, cs.duplicate_of, cs.is_recurring, cs.split_suggestion,
       cs.override_vendor_id, cs.override_customer_id, cs.override_account_id,
       cs.merchant_name, cs.category,
       cs.status, cs.erp_transaction_id,
       cs.created_at, cs.updated_at
FROM fignode.staging_transactions cs
LEFT JOIN fignode.staging_sessions ss ON ss.id = cs.session_id
WHERE cs.id = $1;

-- name: UpdateRowEnrichment :exec
UPDATE fignode.staging_transactions
SET
    predicted_vendor_id     = sqlc.narg('predicted_vendor_id'),
    predicted_vendor_name   = COALESCE(NULLIF(sqlc.narg('predicted_vendor_name'), ''), predicted_vendor_name),
    predicted_customer_id   = sqlc.narg('predicted_customer_id'),
    predicted_customer_name = COALESCE(NULLIF(sqlc.narg('predicted_customer_name'), ''), predicted_customer_name),
    predicted_account_id    = sqlc.narg('predicted_account_id'),
    predicted_account_name  = COALESCE(NULLIF(sqlc.narg('predicted_account_name'), ''), predicted_account_name),
    confidence_score        = sqlc.narg('confidence_score'),
    ai_reasoning            = sqlc.narg('ai_reasoning'),
    duplicate_of            = sqlc.narg('duplicate_of'),
    is_recurring            = sqlc.narg('is_recurring'),
    split_suggestion        = sqlc.narg('split_suggestion'),
    merchant_name           = COALESCE(NULLIF(sqlc.narg('merchant_name'), ''), merchant_name),
    category                = COALESCE(NULLIF(sqlc.narg('category'), ''), category),
    status                  = 'ENRICHED',
    updated_at              = NOW()
WHERE id = sqlc.narg('id');

-- name: ApproveCleanupRow :one
WITH updated AS (
    UPDATE fignode.staging_transactions
    SET status = 'APPROVED', updated_at = NOW()
    WHERE fignode.staging_transactions.id = $1
    RETURNING *
)
SELECT updated.id, updated.session_id, ss.realm_id, ss.bank_account_id, ss.outflow_is, updated.source_type, updated.raw_description, updated.raw_amount, updated.raw_date, updated.parsed_date,
        updated.predicted_vendor_id, updated.predicted_customer_id, updated.predicted_account_id,
        updated.confidence_score, updated.ai_reasoning, updated.duplicate_of, updated.is_recurring, updated.split_suggestion,
        updated.override_vendor_id, updated.override_customer_id, updated.override_account_id, updated.status, updated.erp_transaction_id,
        updated.created_at, updated.updated_at
FROM updated
LEFT JOIN fignode.staging_sessions ss ON ss.id = updated.session_id;

-- name: RejectCleanupRow :exec
UPDATE fignode.staging_transactions
SET status = 'REJECTED', updated_at = NOW()
WHERE id = $1;

-- name: OverrideCleanupRow :one
WITH updated AS (
    UPDATE fignode.staging_transactions
    SET
        override_vendor_id   = sqlc.narg('override_vendor_id'),
        override_customer_id = sqlc.narg('override_customer_id'),
        override_account_id  = sqlc.narg('override_account_id'),
        status               = 'APPROVED',
        updated_at           = NOW()
    WHERE fignode.staging_transactions.id = $1
    RETURNING *
)
SELECT updated.id, updated.session_id, ss.realm_id, ss.bank_account_id, ss.outflow_is, updated.source_type, updated.raw_description, updated.raw_amount, updated.raw_date, updated.parsed_date,
        updated.predicted_vendor_id, updated.predicted_customer_id, updated.predicted_account_id,
        updated.confidence_score, updated.ai_reasoning, updated.duplicate_of, updated.is_recurring, updated.split_suggestion,
        updated.override_vendor_id, updated.override_customer_id, updated.override_account_id, updated.status, updated.erp_transaction_id,
        updated.created_at, updated.updated_at
FROM updated
LEFT JOIN fignode.staging_sessions ss ON ss.id = updated.session_id;

-- name: BulkApproveByVendor :many
UPDATE fignode.staging_transactions
SET status = 'APPROVED', updated_at = NOW()
WHERE session_id = $1
  AND predicted_vendor_id = $2
  AND status = 'ENRICHED'
RETURNING id;

-- name: GetApprovedRows :many
SELECT cs.id, cs.session_id, ss.realm_id, ss.bank_account_id, ss.outflow_is, cs.source_type, cs.raw_description, cs.raw_amount, cs.raw_date, cs.parsed_date,
       cs.predicted_vendor_id, cs.predicted_customer_id, cs.predicted_account_id,
       cs.confidence_score, cs.ai_reasoning, cs.duplicate_of, cs.is_recurring, cs.split_suggestion,
       cs.override_vendor_id, cs.override_customer_id, cs.override_account_id, cs.status, cs.erp_transaction_id,
       cs.created_at, cs.updated_at
FROM fignode.staging_transactions cs
LEFT JOIN fignode.staging_sessions ss ON ss.id = cs.session_id
WHERE cs.session_id = $1 AND cs.status = 'APPROVED'
ORDER BY cs.parsed_date ASC NULLS LAST, cs.id ASC;

-- name: MarkRowPosted :exec
UPDATE fignode.staging_transactions
SET status = 'POSTED', erp_transaction_id = $2, updated_at = NOW()
WHERE id = $1;

-- name: GetSessionSummary :one
SELECT
    COUNT(*)                                                               AS total_rows,
    COUNT(*) FILTER (WHERE status = 'APPROVED')                           AS approved_rows,
    COUNT(*) FILTER (WHERE status = 'REJECTED')                           AS rejected_rows,
    COUNT(*) FILTER (WHERE status = 'POSTED')                             AS posted_rows,
    COUNT(*) FILTER (WHERE duplicate_of IS NOT NULL)                      AS duplicate_rows,
    COUNT(*) FILTER (WHERE is_recurring)                                  AS recurring_rows,
    COUNT(*) FILTER (WHERE override_vendor_id IS NOT NULL
                        OR override_account_id IS NOT NULL)               AS overridden_rows,
    COALESCE(AVG(confidence_score) FILTER (WHERE confidence_score IS NOT NULL), 0) AS avg_confidence
FROM fignode.staging_transactions
WHERE session_id = $1;

-- name: GetAiCorrectionByRawInput :one
SELECT user_correction, correction_type
FROM shadow_erp.ai_corrections
WHERE realm_id = $1
  AND raw_input  = $2
  AND correction_type = $3
ORDER BY created_at DESC
LIMIT 1;

-- name: GetRealmIDFromEntity :one
SELECT erp_tenant_id 
FROM toro_core.entities 
WHERE id = $1 AND erp_provider = 'qbo';

-- name: GetRealmIDFromSession :one
SELECT realm_id 
FROM fignode.staging_sessions 
WHERE id = $1;