-- name: AddSessionEmailHold :exec
INSERT INTO fignode.ase_session_email_holds (session_id, transaction_id)
VALUES ($1, $2)
ON CONFLICT (session_id, transaction_id) DO NOTHING;

-- name: GetHeldTransactionsBySession :many
SELECT 
    h.transaction_id,
    t.raw_description,
    t.raw_amount,
    t.ase_execution_trace
FROM fignode.ase_session_email_holds h
JOIN fignode.staging_transactions t ON h.transaction_id = t.id
WHERE h.session_id = $1;
