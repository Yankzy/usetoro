-- name: GetWallet :one
SELECT * FROM toro_core.wallet
WHERE entity_id = $1;

-- name: CreateWallet :one
INSERT INTO toro_core.wallet (entity_id, total_purchased_micrions, total_burned_micrions)
VALUES ($1, 0, 0)
ON CONFLICT (entity_id) DO NOTHING
RETURNING *;

-- name: LogPurchase :one
WITH updated_wallet AS (
    UPDATE toro_core.wallet
    SET total_purchased_micrions = total_purchased_micrions + $4
    WHERE entity_id = $1
    RETURNING id, entity_id, total_purchased_micrions, total_burned_micrions
)
INSERT INTO toro_core.wallet_transactions (
    wallet_id, 
    transaction_type, 
    stripe_session_id, 
    usd_amount, 
    micrion_amount
)
SELECT 
    id, 
    'purchase', 
    $2, 
    $3, 
    $4
FROM updated_wallet
ON CONFLICT (stripe_session_id) WHERE stripe_session_id IS NOT NULL
DO NOTHING
RETURNING *;

-- name: LogBulkBurn :one
WITH updated_wallet AS (
    UPDATE toro_core.wallet
    SET total_burned_micrions = total_burned_micrions + $3
    WHERE entity_id = $1
    RETURNING id, entity_id, total_purchased_micrions, total_burned_micrions
)
INSERT INTO toro_core.wallet_transactions (
    wallet_id, 
    transaction_type, 
    nats_revision, 
    micrion_amount
)
SELECT 
    id, 
    'burn', 
    $2, 
    $3
FROM updated_wallet
ON CONFLICT (wallet_id, nats_revision) WHERE nats_revision IS NOT NULL
DO NOTHING
RETURNING *;
