-- =========================================================================
-- Cleanup Mode Queries (now stored in fignode schema)
-- =========================================================================

-- name: CreateCleanupSession :one
INSERT INTO fignode.staging_sessions (realm_id, created_by, file_name, row_count, status)
VALUES (sqlc.narg('realm_id'), $1, $2, $3, 'PENDING')
RETURNING id, realm_id, created_by, file_name, row_count, status, created_at, updated_at;

-- name: GetCleanupSession :one
SELECT id, realm_id, created_by, file_name, row_count, status, created_at, updated_at
FROM fignode.staging_sessions
WHERE id = $1;

-- name: ListCleanupSessions :many
-- Returns sessions for a realm (when realm_id is provided) OR sessions created by a user
-- (when realm_id is NULL). Exactly one of the two filters will be non-null per call.
SELECT id, realm_id, created_by, file_name, row_count, status, created_at, updated_at
FROM fignode.staging_sessions
WHERE (sqlc.narg('realm_id')::TEXT IS NULL OR realm_id = sqlc.narg('realm_id')::TEXT)
  AND (sqlc.narg('created_by')::UUID IS NULL OR created_by = sqlc.narg('created_by')::UUID)
ORDER BY created_at DESC;

-- name: UpdateCleanupSessionStatus :exec
UPDATE fignode.staging_sessions
SET status = $2, updated_at = NOW()
WHERE id = $1;

-- name: UpdateCleanupSessionRowCount :exec
UPDATE fignode.staging_sessions
SET row_count = $2, updated_at = NOW()
WHERE id = $1;

-- name: InsertCleanupRow :one
INSERT INTO fignode.staging_transactions (
    session_id, realm_id, source_type, raw_description, raw_amount, raw_date, status,
    predicted_vendor_id, predicted_vendor_name, predicted_customer_id, predicted_customer_name, predicted_account_id, predicted_account_name,
    confidence_score, ai_reasoning, duplicate_of, is_recurring, split_suggestion,
    human_action, swiped_by, swiped_at, override_vendor_id, override_customer_id, override_account_id,
    erp_transaction_id, error_message, plaid_transaction_id, plaid_account_id,
    merchant_name, logo_url, plaid_category, is_pending
)
VALUES (
    $1, sqlc.narg('realm_id'), $2, $3, $4, $5, COALESCE(sqlc.narg('status'), 'PENDING'),
    sqlc.narg('predicted_vendor_id'), sqlc.narg('predicted_vendor_name'), sqlc.narg('predicted_customer_id'), sqlc.narg('predicted_customer_name'), sqlc.narg('predicted_account_id'), sqlc.narg('predicted_account_name'),
    sqlc.narg('confidence_score'), sqlc.narg('ai_reasoning'), sqlc.narg('duplicate_of'), COALESCE(sqlc.narg('is_recurring'), FALSE), sqlc.narg('split_suggestion'),
    sqlc.narg('human_action'), sqlc.narg('swiped_by'), sqlc.narg('swiped_at'), sqlc.narg('override_vendor_id'), sqlc.narg('override_customer_id'), sqlc.narg('override_account_id'),
    sqlc.narg('erp_transaction_id'), sqlc.narg('error_message'), sqlc.narg('plaid_transaction_id'), sqlc.narg('plaid_account_id'),
    sqlc.narg('merchant_name'), sqlc.narg('logo_url'), sqlc.narg('plaid_category'), COALESCE(sqlc.narg('is_pending'), FALSE)
)
RETURNING id;

-- name: GetPendingSessionRows :many
SELECT cs.id, cs.session_id, cs.realm_id, cs.source_type, cs.raw_description, cs.raw_amount, cs.raw_date,
       cs.predicted_vendor_id, cs.predicted_customer_id, cs.predicted_account_id,
       cs.confidence_score, cs.ai_reasoning,
       cs.duplicate_of, cs.is_recurring, cs.split_suggestion,
       cs.override_vendor_id, cs.override_customer_id, cs.override_account_id,
       cs.merchant_name, cs.plaid_category,
       cs.status, cs.erp_transaction_id, cs.created_at, cs.updated_at,
       COALESCE(v.display_name, cs.predicted_vendor_name, '') AS predicted_vendor_name,
       COALESCE(c.display_name, cs.predicted_customer_name, '') AS predicted_customer_name,
       a.name         AS predicted_account_name,
       a.account_type AS predicted_account_type,
       ov.display_name AS override_vendor_name,
       oc.display_name AS override_customer_name,
       oa.name         AS override_account_name
FROM fignode.staging_transactions cs
LEFT JOIN shadow_erp.vendors  v  ON v.id  = cs.predicted_vendor_id
LEFT JOIN shadow_erp.customers c ON c.id  = cs.predicted_customer_id
LEFT JOIN shadow_erp.accounts a  ON a.id  = cs.predicted_account_id
LEFT JOIN shadow_erp.vendors  ov ON ov.id = cs.override_vendor_id
LEFT JOIN shadow_erp.customers oc ON oc.id = cs.override_customer_id
LEFT JOIN shadow_erp.accounts oa ON oa.id = cs.override_account_id
WHERE cs.session_id = $1 AND cs.status = 'PENDING'
ORDER BY cs.raw_date ASC NULLS LAST, cs.id ASC;

-- name: GetPendingRealmRows :many
SELECT cs.id, cs.session_id, cs.realm_id, cs.source_type, cs.raw_description, cs.raw_amount, cs.raw_date,
       cs.predicted_vendor_id, cs.predicted_customer_id, cs.predicted_account_id,
       cs.confidence_score, cs.ai_reasoning,
       cs.duplicate_of, cs.is_recurring, cs.split_suggestion,
       cs.override_vendor_id, cs.override_customer_id, cs.override_account_id,
       cs.merchant_name, cs.plaid_category,
       cs.status, cs.erp_transaction_id, cs.created_at, cs.updated_at,
       COALESCE(v.display_name, cs.predicted_vendor_name, '') AS predicted_vendor_name,
       COALESCE(c.display_name, cs.predicted_customer_name, '') AS predicted_customer_name,
       a.name         AS predicted_account_name,
       a.account_type AS predicted_account_type,
       ov.display_name AS override_vendor_name,
       oc.display_name AS override_customer_name,
       oa.name         AS override_account_name
FROM fignode.staging_transactions cs
LEFT JOIN shadow_erp.vendors  v  ON v.id  = cs.predicted_vendor_id
LEFT JOIN shadow_erp.customers c ON c.id  = cs.predicted_customer_id
LEFT JOIN shadow_erp.accounts a  ON a.id  = cs.predicted_account_id
LEFT JOIN shadow_erp.vendors  ov ON ov.id = cs.override_vendor_id
LEFT JOIN shadow_erp.customers oc ON oc.id = cs.override_customer_id
LEFT JOIN shadow_erp.accounts oa ON oa.id = cs.override_account_id
WHERE cs.realm_id = $1 AND cs.status = 'PENDING'
ORDER BY cs.raw_date ASC NULLS LAST
LIMIT $2;

-- name: GetSessionRows :many
SELECT cs.id, cs.session_id, cs.realm_id, cs.source_type, cs.raw_description, cs.raw_amount, cs.raw_date,
       cs.predicted_vendor_id, cs.predicted_customer_id, cs.predicted_account_id,
       cs.confidence_score, cs.ai_reasoning,
       cs.duplicate_of, cs.is_recurring, cs.split_suggestion,
       cs.override_vendor_id, cs.override_customer_id, cs.override_account_id,
       cs.merchant_name, cs.plaid_category,
       cs.status, cs.erp_transaction_id, cs.created_at, cs.updated_at,
       COALESCE(v.display_name, cs.predicted_vendor_name, '') AS predicted_vendor_name,
       COALESCE(c.display_name, cs.predicted_customer_name, '') AS predicted_customer_name,
       a.name         AS predicted_account_name,
       a.account_type AS predicted_account_type,
       ov.display_name AS override_vendor_name,
       oc.display_name AS override_customer_name,
       oa.name         AS override_account_name
FROM fignode.staging_transactions cs
LEFT JOIN shadow_erp.vendors  v  ON v.id  = cs.predicted_vendor_id
LEFT JOIN shadow_erp.customers c ON c.id  = cs.predicted_customer_id
LEFT JOIN shadow_erp.accounts a  ON a.id  = cs.predicted_account_id
LEFT JOIN shadow_erp.vendors  ov ON ov.id = cs.override_vendor_id
LEFT JOIN shadow_erp.customers oc ON oc.id = cs.override_customer_id
LEFT JOIN shadow_erp.accounts oa ON oa.id = cs.override_account_id
WHERE cs.session_id = $1
  AND (sqlc.narg('status')::TEXT IS NULL OR cs.status = sqlc.narg('status')::TEXT)
ORDER BY cs.raw_date ASC NULLS LAST, cs.id ASC;

-- name: GetCleanupRow :one
SELECT id, session_id, realm_id, source_type, raw_description, raw_amount, raw_date,
       predicted_vendor_id, predicted_customer_id, predicted_account_id,
       confidence_score, ai_reasoning, duplicate_of, is_recurring, split_suggestion,
       override_vendor_id, override_customer_id, override_account_id, 
       merchant_name, plaid_category,
       status, erp_transaction_id,
       created_at, updated_at
FROM fignode.staging_transactions
WHERE id = $1;

-- name: UpdateRowEnrichment :exec
UPDATE fignode.staging_transactions
SET
    predicted_vendor_id     = $2,
    predicted_vendor_name   = $3,
    predicted_customer_id   = $4,
    predicted_customer_name = $5,
    predicted_account_id    = $6,
    predicted_account_name  = $7,
    confidence_score        = $8,
    ai_reasoning            = $9,
    duplicate_of            = $10,
    is_recurring            = $11,
    split_suggestion        = $12,
    merchant_name           = $13,
    plaid_category          = $14,
    status                  = 'ENRICHED',
    updated_at              = NOW()
WHERE id = $1;

-- name: ApproveCleanupRow :one
UPDATE fignode.staging_transactions
SET status = 'APPROVED', updated_at = NOW()
WHERE id = $1
RETURNING id, session_id, realm_id, source_type, raw_description, raw_amount, raw_date,
          predicted_vendor_id, predicted_customer_id, predicted_account_id,
          confidence_score, ai_reasoning, duplicate_of, is_recurring, split_suggestion,
          override_vendor_id, override_customer_id, override_account_id, status, erp_transaction_id,
          created_at, updated_at;

-- name: RejectCleanupRow :exec
UPDATE fignode.staging_transactions
SET status = 'REJECTED', updated_at = NOW()
WHERE id = $1;

-- name: OverrideCleanupRow :one
UPDATE fignode.staging_transactions
SET
    override_vendor_id   = sqlc.narg('override_vendor_id'),
    override_customer_id = sqlc.narg('override_customer_id'),
    override_account_id  = sqlc.narg('override_account_id'),
    status               = 'APPROVED',
    updated_at           = NOW()
WHERE id = $1
RETURNING id, session_id, realm_id, source_type, raw_description, raw_amount, raw_date,
          predicted_vendor_id, predicted_customer_id, predicted_account_id,
          confidence_score, ai_reasoning, duplicate_of, is_recurring, split_suggestion,
          override_vendor_id, override_customer_id, override_account_id, status, erp_transaction_id,
          created_at, updated_at;

-- name: BulkApproveByVendor :many
UPDATE fignode.staging_transactions
SET status = 'APPROVED', updated_at = NOW()
WHERE session_id = $1
  AND predicted_vendor_id = $2
  AND status = 'ENRICHED'
RETURNING id;

-- name: GetApprovedRows :many
SELECT id, session_id, realm_id, source_type, raw_description, raw_amount, raw_date,
       predicted_vendor_id, predicted_customer_id, predicted_account_id,
       confidence_score, ai_reasoning, duplicate_of, is_recurring, split_suggestion,
       override_vendor_id, override_customer_id, override_account_id, status, erp_transaction_id,
       created_at, updated_at
FROM fignode.staging_transactions
WHERE session_id = $1 AND status = 'APPROVED'
ORDER BY raw_date ASC NULLS LAST, id ASC;

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
