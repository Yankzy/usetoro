-- name: CreateThreadMapping :exec
INSERT INTO toro_core.toro_threads_mappings (conversation_id, tenant_id, slack_channel_id, slack_parent_ts, email_latest_message_id)
VALUES ($1, $2, $3, $4, $5);

-- name: GetThreadMappingBySlackTS :one
SELECT * FROM toro_core.toro_threads_mappings
WHERE slack_channel_id = $1 AND slack_parent_ts = $2;

-- name: GetThreadMappingByEmailMessageID :one
SELECT * FROM toro_core.toro_threads_mappings
WHERE email_latest_message_id = $1;

-- name: UpdateThreadMappingEmailMessageID :exec
UPDATE toro_core.toro_threads_mappings
SET email_latest_message_id = $1, updated_at = NOW()
WHERE slack_channel_id = $2 AND slack_parent_ts = $3;
