-- name: CreateTeamInvite :one
INSERT INTO toro_core.team_invites (
    token, email, tenant_id, firm_name
) VALUES (
    $1, $2, $3, $4
)
RETURNING *;

-- name: GetTeamInviteByToken :one
SELECT * FROM toro_core.team_invites
WHERE token = $1;

-- name: DeleteExpiredTeamInvites :exec
DELETE FROM toro_core.team_invites
WHERE created_at < NOW() - INTERVAL '7 days';

-- name: DeleteAndReturnTeamInvite :one
DELETE FROM toro_core.team_invites
WHERE token = $1 AND created_at >= NOW() - INTERVAL '7 days'
RETURNING *;
