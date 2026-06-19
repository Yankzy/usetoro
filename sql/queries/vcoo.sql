-- name: GetUserVCOOData :one
SELECT id, entity_id, email, vcoo_active_blockers, vcoo_history, vcoo_state
FROM toro_core.users
WHERE id = $1;

-- name: GetUserVCOODataByEmail :one
SELECT id, entity_id, email, vcoo_active_blockers, vcoo_history, vcoo_state
FROM toro_core.users
WHERE email = $1;

-- name: UpdateUserVCOOData :exec
UPDATE toro_core.users
SET vcoo_active_blockers = $2,
    vcoo_history = $3,
    vcoo_state = $4,
    updated_at = NOW()
WHERE id = $1;

-- name: ListUsersWithVCOO :many
SELECT id, entity_id, email, vcoo_active_blockers, vcoo_history, vcoo_state
FROM toro_core.users
WHERE vcoo_state IS NOT NULL AND vcoo_state != '{}'::jsonb;
