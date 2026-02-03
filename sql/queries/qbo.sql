-- name: UpsertQBOTokens :exec
INSERT INTO qbo_connections (realm_id, access_token, refresh_token, expires_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (realm_id) 
DO UPDATE SET 
    access_token = $2, 
    refresh_token = $3, 
    expires_at = $4,
    updated_at = NOW();

-- name: GetQBOTokens :one
SELECT access_token, refresh_token, expires_at
FROM qbo_connections
WHERE realm_id = $1;
