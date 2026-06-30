
-- name: UpdateSessionContextJSON :exec
UPDATE toro_core.conversation_sessions
SET context_json = $2
WHERE id = $1;
