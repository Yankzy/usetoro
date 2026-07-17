-- name: GetProspectByID :one
SELECT id, email, first_name, last_name, metadata, campaign_id, current_step_id, status, tenant_id
FROM marketing.prospects
WHERE id = $1;

-- name: GetCampaignStep :one
SELECT id, campaign_id, step_number, subject_template, body_template, delay_duration
FROM marketing.campaign_steps
WHERE campaign_id = $1 AND step_number = $2;

-- name: GetNextAvailableEmailAccount :one
SELECT id, email, encrypted_password, daily_send_count, daily_send_limit, status, tenant_id
FROM marketing.email_accounts
WHERE status = 'active' AND daily_send_count < daily_send_limit AND tenant_id = $1
ORDER BY daily_send_count ASC
LIMIT 1;

-- name: GetEmailAccountByID :one
SELECT id, email, encrypted_password, daily_send_count, daily_send_limit, status, tenant_id
FROM marketing.email_accounts
WHERE id = $1;

-- name: IncrementEmailAccountSendCount :exec
UPDATE marketing.email_accounts
SET daily_send_count = daily_send_count + 1, updated_at = NOW()
WHERE id = $1;

-- name: LogEmailEvent :exec
INSERT INTO marketing.email_logs (
    prospect_id, campaign_id, list_id, nats_msg_id, event_type, metadata, user_agent, ip_address, is_human
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
);

-- name: GetEmailAccountByEmail :one
SELECT id, email, encrypted_password, daily_send_count, daily_send_limit, status, tenant_id
FROM marketing.email_accounts
WHERE email = $1;

-- name: GetAllActiveEmailAccounts :many
SELECT id, email, encrypted_password, daily_send_count, daily_send_limit, status, tenant_id
FROM marketing.email_accounts
WHERE status = 'active';

-- name: UpdateEmailAccountStatus :exec
UPDATE marketing.email_accounts
SET status = $1, updated_at = NOW()
WHERE id = $2;

-- name: UpdateProspectStatus :exec
UPDATE marketing.prospects
SET status = $1, updated_at = NOW()
WHERE id = $2;

-- name: GetAllWarmingEmailAccounts :many
SELECT id, email, encrypted_password, daily_send_count, daily_send_limit, status, tenant_id, warmup_phase, domain_created_at, bounce_count, spam_complaints
FROM marketing.email_accounts
WHERE status IN ('warming', 'active');

-- name: UpdateEmailAccountWarmup :exec
UPDATE marketing.email_accounts
SET daily_send_limit = $1, warmup_phase = $2, bounce_count = $3, spam_complaints = $4, status = $5, updated_at = NOW()
WHERE id = $6;
