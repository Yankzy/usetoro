-- name: LogStalledMessage :one
INSERT INTO toro_core.stalled_messages (
    agent_did, original_subject, payload, error_reason
) VALUES (
    $1, $2, $3, $4
) RETURNING *;

-- name: GetStalledMessagesByAgent :many
SELECT * FROM toro_core.stalled_messages
WHERE agent_did = $1
ORDER BY created_at ASC;

-- name: DeleteStalledMessage :exec
DELETE FROM toro_core.stalled_messages WHERE id = $1;
