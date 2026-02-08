-- name: CreateTenant :one
INSERT INTO tenants (name, plan_tier) VALUES ($1, $2) RETURNING id;

-- name: CreateUser :one
INSERT INTO users (tenant_id, email, password_hash, role) 
VALUES ($1, $2, $3, $4) RETURNING id;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: CreateRefreshToken :exec
INSERT INTO refresh_tokens (token_hash, user_id, expires_at, ip_address, user_agent)
VALUES ($1, $2, $3, $4, $5);

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;
