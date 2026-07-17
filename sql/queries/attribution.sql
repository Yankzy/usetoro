-- name: CreateMarketingList :one
INSERT INTO marketing.lists (tenant_id, name)
VALUES ($1, $2)
RETURNING *;

-- name: GetProspectIDByEmail :one
SELECT id
FROM marketing.prospects
WHERE email = $1 AND tenant_id = $2
LIMIT 1;

-- name: AddListSubscriber :one
INSERT INTO marketing.list_subscribers (list_id, prospect_id, status)
VALUES ($1, $2, 'active')
ON CONFLICT (list_id, prospect_id) DO UPDATE SET status = 'active', created_at = CURRENT_TIMESTAMP
RETURNING *;

-- name: UnsubscribeList :exec
UPDATE marketing.list_subscribers
SET status = 'unsubscribed'
WHERE list_id = $1 AND prospect_id = $2;

-- name: GetTenantSettings :one
SELECT * FROM marketing.tenant_settings
WHERE tenant_id = $1;

-- name: UpsertTenantSettings :one
INSERT INTO marketing.tenant_settings (tenant_id, attribution_window_days)
VALUES ($1, $2)
ON CONFLICT (tenant_id) DO UPDATE SET
    attribution_window_days = EXCLUDED.attribution_window_days,
    updated_at = CURRENT_TIMESTAMP
RETURNING *;

-- name: CreateConversion :one
INSERT INTO marketing.conversions (tenant_id, prospect_id, customer_email, conversion_value, source)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CreateAttribution :one
INSERT INTO marketing.attributions (conversion_id, email_log_id, campaign_id, list_id, attributed_value)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: FindLastTouch :one
SELECT *
FROM marketing.email_logs
WHERE prospect_id = $1
  AND is_human = true
  AND event_type IN ('opened', 'clicked')
  AND created_at >= NOW() - INTERVAL '1 day' * sqlc.arg('window_days')::int
ORDER BY created_at DESC
LIMIT 1;

-- name: GetCampaignRevenueMetrics :many
SELECT
    campaign_id,
    COUNT(id) AS total_conversions,
    SUM(attributed_value)::bigint AS total_revenue
FROM marketing.attributions
WHERE campaign_id = $1
GROUP BY campaign_id;

-- name: GetListRevenueMetrics :many
SELECT
    list_id,
    COUNT(id) AS total_conversions,
    SUM(attributed_value)::bigint AS total_revenue
FROM marketing.attributions
WHERE list_id = $1
GROUP BY list_id;
