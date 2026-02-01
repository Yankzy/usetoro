-- name: GetWebhookSecret :one
SELECT webhook_secret 
FROM webhooks_providerconnection 
WHERE connection_id = $1 AND is_active = true;

-- name: UpsertWebhookSecret :exec
INSERT INTO webhooks_providerconnection (connection_id, webhook_secret, is_active)
VALUES ($1, $2, true)
ON CONFLICT (connection_id) 
DO UPDATE SET 
    webhook_secret = $2, 
    updated_at = NOW();
