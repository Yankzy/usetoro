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
