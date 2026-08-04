-- name: InsertClientRequestOutbox :exec
INSERT INTO fignode.client_request_outbox (
    transaction_id,
    session_id,
    client_id,
    request_type,
    context,
    dag_node_id,
    status
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
);

-- name: GetQueuedRequestsBySession :many
SELECT 
    o.id,
    o.transaction_id,
    o.session_id,
    o.client_id,
    o.request_type,
    o.context,
    o.dag_node_id,
    o.status,
    t.raw_description,
    t.raw_amount,
    t.raw_date
FROM fignode.client_request_outbox o
JOIN fignode.staging_transactions t ON o.transaction_id = t.id
WHERE o.session_id = $1 AND o.status = 'QUEUED';

-- name: MarkOutboxRequestsSent :exec
UPDATE fignode.client_request_outbox
SET status = 'SENT', updated_at = NOW()
WHERE session_id = $1 AND status = 'QUEUED';
