-- name: GetWebhookSecret :one
SELECT webhook_secret 
FROM webhookks_providerconnection 
WHERE connection_id = $1 AND is_active = true;
