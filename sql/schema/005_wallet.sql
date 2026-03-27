-- +goose NO TRANSACTION
-- +goose Up

-- =========================================================================
-- SCHEMA: toro_core.wallet
-- The Fiat Ledger & Burn Rollup for the Micrion Execution Layer.
-- =========================================================================

-- 1. The Wallet Master Record
CREATE TABLE toro_core.wallet (
    id                       UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_id                UUID NOT NULL UNIQUE REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    total_purchased_micrions BIGINT NOT NULL DEFAULT 0,
    total_burned_micrions    BIGINT NOT NULL DEFAULT 0,
    created_at               TIMESTAMPTZ DEFAULT NOW(),
    updated_at               TIMESTAMPTZ DEFAULT NOW()
);

CREATE TRIGGER update_wallet_updated_at
    BEFORE UPDATE ON toro_core.wallet
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- 2. The Transactions Ledger (Immutable Audit Log)
-- `stripe_session_id` tracks Fiat On-Ramps.
-- `nats_revision` tracks high-speed burn rollups (The Two Generals idempontency lock).
CREATE TABLE toro_core.wallet_transactions (
    id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    wallet_id         UUID NOT NULL REFERENCES toro_core.wallet(id) ON DELETE CASCADE,
    transaction_type  VARCHAR(50) NOT NULL, -- 'purchase' or 'burn'
    micrion_amount    BIGINT NOT NULL,      -- Positive for purchase, positive for burn (contextualized by type)
    usd_amount        BIGINT,               -- Only populated on 'purchase' (in cents)
    stripe_session_id TEXT,                 -- Only populated on 'purchase'
    nats_revision     BIGINT,               -- Only populated on 'burn' (The CAS idempotency lock)
    created_at        TIMESTAMPTZ DEFAULT NOW()
);

-- Indexes for fast querying
CREATE INDEX idx_wallet_transactions_wallet ON toro_core.wallet_transactions(wallet_id);

-- Idempotency Constraints!
-- Ensure we never double-charge a Stripe Session
CREATE UNIQUE INDEX idx_wallet_txn_stripe_unique 
    ON toro_core.wallet_transactions(stripe_session_id) 
    WHERE stripe_session_id IS NOT NULL;

-- Ensure we never double-charge a NATS execution rollup! (The cross-database atomic safety lock)
CREATE UNIQUE INDEX idx_wallet_txn_nats_unique 
    ON toro_core.wallet_transactions(wallet_id, nats_revision) 
    WHERE nats_revision IS NOT NULL;


-- +goose Down
DROP TABLE IF EXISTS toro_core.wallet_transactions;
DROP TABLE IF EXISTS toro_core.wallet;
