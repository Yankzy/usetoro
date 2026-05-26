-- name: SaveInboundConversation :exec
INSERT INTO toro_core.conversations (
    entity_id,
    source,
    external_id,
    from_handle,
    to_handle,
    reply_to,
    in_reply_to,
    subject,
    body_text,
    body_html,
    stripped_text,
    metadata
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
ON CONFLICT (external_id) DO NOTHING;

-- name: GetEntityIDByEmail :one
SELECT entity_id FROM toro_core.users WHERE email = $1 LIMIT 1;

-- name: GetRecentConversations :many
SELECT * FROM toro_core.conversations
WHERE (from_handle = $1 AND to_handle = $2)
   OR (from_handle = $2 AND to_handle = $1)
ORDER BY created_at DESC
LIMIT $3;

-- name: InsertConversationSession :one
INSERT INTO toro_core.conversation_sessions (
    entity_id, source, participant_handle, toro_handle, subject, system_prompt, context_json, status
) VALUES ($1, $2, $3, $4, $5, $6, $7, 'active')
RETURNING *;

-- name: GetConversationSession :one
SELECT * FROM toro_core.conversation_sessions WHERE id = $1;

-- name: UpdateConversationSession :exec
UPDATE toro_core.conversation_sessions
SET
    status = CASE WHEN sqlc.narg('status')::text IS NOT NULL THEN sqlc.narg('status')::text ELSE status END,
    system_prompt = COALESCE(sqlc.narg('system_prompt'), system_prompt),
    context_json = COALESCE(sqlc.narg('context_json'), context_json),
    last_activity_at = NOW()
WHERE id = $1;

-- name: GetSessionConversations :many
SELECT * FROM toro_core.conversations
WHERE session_id = $1
ORDER BY created_at ASC;

-- name: GetActiveSessions :many
SELECT * FROM toro_core.conversation_sessions
WHERE entity_id = $1 AND status IN ('active', 'awaiting_reply')
ORDER BY last_activity_at DESC;

-- name: GetActiveSessionByParticipant :one
SELECT * FROM toro_core.conversation_sessions
WHERE entity_id = $1 AND participant_handle = $2 AND status IN ('active', 'awaiting_reply')
ORDER BY last_activity_at DESC
LIMIT 1;

-- name: SaveConversationSessionMessage :exec
INSERT INTO toro_core.conversations (
    entity_id, source, external_id, from_handle, to_handle, reply_to, in_reply_to, subject, body_text, body_html, stripped_text, metadata, session_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
)
ON CONFLICT (external_id) DO NOTHING;

-- name: GetAwaitingReplySessions :many
SELECT * FROM toro_core.conversation_sessions
WHERE status = 'awaiting_reply'
ORDER BY last_activity_at ASC;
