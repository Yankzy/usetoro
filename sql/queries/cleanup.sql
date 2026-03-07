-- =========================================================================
-- Cleanup Mode Queries
-- =========================================================================

-- name: CreateCleanupSession :one
INSERT INTO shadow_erp.cleanup_sessions (realm_id, created_by, file_name, row_count, status)
VALUES (sqlc.narg('realm_id'), $1, $2, $3, 'PENDING')
RETURNING id, realm_id, created_by, file_name, row_count, status, created_at, updated_at;

-- name: GetCleanupSession :one
SELECT id, realm_id, created_by, file_name, row_count, status, created_at, updated_at
FROM shadow_erp.cleanup_sessions
WHERE id = $1;

-- name: ListCleanupSessions :many
-- Returns sessions for a realm (when realm_id is provided) OR sessions created by a user
-- (when realm_id is NULL). Exactly one of the two filters will be non-null per call.
SELECT id, realm_id, created_by, file_name, row_count, status, created_at, updated_at
FROM shadow_erp.cleanup_sessions
WHERE (sqlc.narg('realm_id')::TEXT IS NULL OR realm_id = sqlc.narg('realm_id')::TEXT)
  AND (sqlc.narg('created_by')::UUID IS NULL OR created_by = sqlc.narg('created_by')::UUID)
ORDER BY created_at DESC;

-- name: UpdateCleanupSessionStatus :exec
UPDATE shadow_erp.cleanup_sessions
SET status = $2, updated_at = NOW()
WHERE id = $1;

-- name: UpdateCleanupSessionRowCount :exec
UPDATE shadow_erp.cleanup_sessions
SET row_count = $2, updated_at = NOW()
WHERE id = $1;

-- name: InsertCleanupRow :one
INSERT INTO shadow_erp.cleanup_staging (
    session_id, realm_id, raw_description, raw_amount, raw_date, raw_vendor_name, status
)
VALUES ($1, sqlc.narg('realm_id'), $2, $3, $4, $5, 'PENDING')
RETURNING id;

-- name: GetPendingSessionRows :many
SELECT id, session_id, realm_id, raw_description, raw_amount, raw_date, raw_vendor_name,
       predicted_vendor_id, predicted_account_id, normalized_vendor,
       confidence_score, ai_reasoning, duplicate_of, is_recurring, split_suggestion,
       override_vendor_id, override_account_id, status, erp_transaction_id,
       created_at, updated_at
FROM shadow_erp.cleanup_staging
WHERE session_id = $1 AND status = 'PENDING'
ORDER BY raw_date ASC NULLS LAST, id ASC;

-- name: GetSessionRows :many
SELECT cs.id, cs.session_id, cs.realm_id, cs.raw_description, cs.raw_amount, cs.raw_date,
       cs.raw_vendor_name, cs.predicted_vendor_id, cs.predicted_account_id,
       cs.normalized_vendor, cs.confidence_score, cs.ai_reasoning,
       cs.duplicate_of, cs.is_recurring, cs.split_suggestion,
       cs.override_vendor_id, cs.override_account_id,
       cs.status, cs.erp_transaction_id, cs.created_at, cs.updated_at,
       v.display_name AS predicted_vendor_name,
       a.name         AS predicted_account_name,
       a.account_type AS predicted_account_type,
       ov.display_name AS override_vendor_name,
       oa.name         AS override_account_name
FROM shadow_erp.cleanup_staging cs
LEFT JOIN shadow_erp.vendors  v  ON v.id  = cs.predicted_vendor_id
LEFT JOIN shadow_erp.accounts a  ON a.id  = cs.predicted_account_id
LEFT JOIN shadow_erp.vendors  ov ON ov.id = cs.override_vendor_id
LEFT JOIN shadow_erp.accounts oa ON oa.id = cs.override_account_id
WHERE cs.session_id = $1
  AND (sqlc.narg('status')::TEXT IS NULL OR cs.status = sqlc.narg('status')::TEXT)
ORDER BY cs.raw_date ASC NULLS LAST, cs.id ASC;

-- name: GetCleanupRow :one
SELECT id, session_id, realm_id, raw_description, raw_amount, raw_date, raw_vendor_name,
       predicted_vendor_id, predicted_account_id, normalized_vendor,
       confidence_score, ai_reasoning, duplicate_of, is_recurring, split_suggestion,
       override_vendor_id, override_account_id, status, erp_transaction_id,
       created_at, updated_at
FROM shadow_erp.cleanup_staging
WHERE id = $1;

-- name: UpdateRowEnrichment :exec
UPDATE shadow_erp.cleanup_staging
SET
    predicted_vendor_id  = $2,
    predicted_account_id = $3,
    normalized_vendor    = $4,
    confidence_score     = $5,
    ai_reasoning         = $6,
    duplicate_of         = $7,
    is_recurring         = $8,
    split_suggestion     = $9,
    status               = 'ENRICHED',
    updated_at           = NOW()
WHERE id = $1;

-- name: ApproveCleanupRow :one
UPDATE shadow_erp.cleanup_staging
SET status = 'APPROVED', updated_at = NOW()
WHERE id = $1
RETURNING id, session_id, realm_id, raw_description, raw_amount, raw_date, raw_vendor_name,
          predicted_vendor_id, predicted_account_id, normalized_vendor,
          confidence_score, ai_reasoning, duplicate_of, is_recurring, split_suggestion,
          override_vendor_id, override_account_id, status, erp_transaction_id,
          created_at, updated_at;

-- name: RejectCleanupRow :exec
UPDATE shadow_erp.cleanup_staging
SET status = 'REJECTED', updated_at = NOW()
WHERE id = $1;

-- name: OverrideCleanupRow :one
UPDATE shadow_erp.cleanup_staging
SET
    override_vendor_id  = sqlc.narg('override_vendor_id'),
    override_account_id = sqlc.narg('override_account_id'),
    status              = 'APPROVED',
    updated_at          = NOW()
WHERE id = $1
RETURNING id, session_id, realm_id, raw_description, raw_amount, raw_date, raw_vendor_name,
          predicted_vendor_id, predicted_account_id, normalized_vendor,
          confidence_score, ai_reasoning, duplicate_of, is_recurring, split_suggestion,
          override_vendor_id, override_account_id, status, erp_transaction_id,
          created_at, updated_at;

-- name: BulkApproveByVendor :many
UPDATE shadow_erp.cleanup_staging
SET status = 'APPROVED', updated_at = NOW()
WHERE session_id = $1
  AND predicted_vendor_id = $2
  AND status = 'ENRICHED'
RETURNING id;

-- name: GetApprovedRows :many
SELECT id, session_id, realm_id, raw_description, raw_amount, raw_date, raw_vendor_name,
       predicted_vendor_id, predicted_account_id, normalized_vendor,
       confidence_score, ai_reasoning, duplicate_of, is_recurring, split_suggestion,
       override_vendor_id, override_account_id, status, erp_transaction_id,
       created_at, updated_at
FROM shadow_erp.cleanup_staging
WHERE session_id = $1 AND status = 'APPROVED'
ORDER BY raw_date ASC NULLS LAST, id ASC;

-- name: MarkRowPosted :exec
UPDATE shadow_erp.cleanup_staging
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
FROM shadow_erp.cleanup_staging
WHERE session_id = $1;

-- name: GetAiCorrectionByRawInput :one
SELECT user_correction, correction_type
FROM shadow_erp.ai_corrections
WHERE realm_id = $1
  AND raw_input  = $2
  AND correction_type = $3
ORDER BY created_at DESC
LIMIT 1;
