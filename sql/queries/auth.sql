-- name: CreateEntity :one
INSERT INTO toro_core.entities (name, entity_type, plan_tier)
VALUES ($1, $2, $3) RETURNING id;

-- name: CreateUser :one
INSERT INTO toro_core.users (entity_id, email, password_hash, role)
VALUES ($1, $2, $3, $4) RETURNING id;

-- name: GetUserByEmail :one
SELECT * FROM toro_core.users WHERE email = $1;

-- name: GetUserByID :one
SELECT * FROM toro_core.users WHERE id = $1;

-- name: GetUsersByIDs :many
SELECT * FROM toro_core.users WHERE id = ANY($1::uuid[]);

-- name: CreateRefreshToken :exec
INSERT INTO toro_core.refresh_tokens (token_hash, user_id, expires_at, ip_address, user_agent)
VALUES ($1, $2, $3, $4, $5);

-- name: GetRefreshToken :one
SELECT * FROM toro_core.refresh_tokens WHERE token_hash = $1;

-- name: DeleteRefreshToken :exec
DELETE FROM toro_core.refresh_tokens WHERE token_hash = $1;

-- name: GetEntities :many
SELECT * FROM toro_core.entities
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountEntities :one
SELECT COUNT(*) FROM toro_core.entities;
