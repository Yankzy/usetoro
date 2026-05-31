-- name: UpsertSlackTenantMapping :one
INSERT INTO toro_core.slack_tenant_mappings (
    tenant_id,
    slack_team_id,
    slack_access_token,
    slack_bot_user_id
) VALUES (
    $1, $2, $3, $4
)
ON CONFLICT (slack_team_id) DO UPDATE SET
    tenant_id = EXCLUDED.tenant_id,
    slack_access_token = EXCLUDED.slack_access_token,
    slack_bot_user_id = EXCLUDED.slack_bot_user_id,
    updated_at = NOW()
RETURNING *;

-- name: GetSlackTenantMappingByTeamID :one
SELECT * FROM toro_core.slack_tenant_mappings
WHERE slack_team_id = $1 LIMIT 1;

-- name: GetSlackTenantMappingByTenantID :one
SELECT * FROM toro_core.slack_tenant_mappings
WHERE tenant_id = $1 LIMIT 1;

-- name: DeleteSlackTenantMappingByTeamID :exec
DELETE FROM toro_core.slack_tenant_mappings
WHERE slack_team_id = $1;
