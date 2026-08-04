-- name: GetCompletedPcmSessions :many
SELECT s.id, s.realm_id, s.file_name, s.row_count, u.email as user_email, s.bank_account_id
FROM fignode.staging_sessions s
LEFT JOIN toro_core.users u ON u.id = s.created_by
WHERE s.status IN ('PENDING', 'ENRICHED')
  AND s.kind = 'CSV'
  AND s.row_count > 0
  AND (
      SELECT COUNT(*)
      FROM fignode.staging_transactions t
      WHERE t.session_id = s.id AND t.status = 'CLASSIFIED'
  ) = s.row_count;

-- name: MarkPcmSessionExported :exec
UPDATE fignode.staging_sessions
SET status = 'EXPORTED', updated_at = NOW()
WHERE id = $1;

-- name: GetPcmSessionTransactions :many
SELECT 
    t.id, 
    t.parsed_date,
    t.raw_date,
    t.raw_description,
    t.raw_amount,
    t.cash_direction,
    t.ase_execution_trace
FROM fignode.staging_transactions t
WHERE t.session_id = $1
ORDER BY t.row_index ASC;
