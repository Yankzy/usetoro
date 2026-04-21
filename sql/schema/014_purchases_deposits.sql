-- +goose Up
-- The Local Purchase Table (Money Out)
CREATE TABLE IF NOT EXISTS shadow_erp.purchases (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    erp_id TEXT NOT NULL,         -- QBO Purchase ID
    realm_id TEXT NOT NULL,       -- The CPA's Client ID
    txn_date DATE NOT NULL,
    total_amount DECIMAL(15,2),
    payment_type TEXT,            -- 'Cash', 'Check', 'CreditCard'
    source_account_id TEXT,       -- The bank account that paid (QBO AccountRef)
    entity_id TEXT,               -- The Vendor ID (QBO EntityRef)
    lines JSONB NOT NULL,         -- The raw QBO Line[] array
    event_source TEXT NOT NULL DEFAULT 'toro_internal',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE(realm_id, erp_id)
);

-- The Local Deposit Table (Money In)
CREATE TABLE IF NOT EXISTS shadow_erp.deposits (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    erp_id TEXT NOT NULL,         -- QBO Deposit ID
    realm_id TEXT NOT NULL,
    txn_date DATE NOT NULL,
    total_amount DECIMAL(15,2),
    target_account_id TEXT,       -- The bank account receiving money (DepositToAccountRef)
    lines JSONB NOT NULL,         -- The raw QBO Line[] array (contains Customer ID & Income Account)
    event_source TEXT NOT NULL DEFAULT 'toro_internal',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE(realm_id, erp_id)
);

CREATE INDEX IF NOT EXISTS idx_purchases_realm ON shadow_erp.purchases(realm_id);
CREATE INDEX IF NOT EXISTS idx_deposits_realm ON shadow_erp.deposits(realm_id);

-- Add support for Deposit webhooks to the core connections table
ALTER TABLE toro_core.erp_connections ADD COLUMN IF NOT EXISTS last_webhook_deposit TIMESTAMPTZ;

-- +goose Down
DROP TABLE IF EXISTS shadow_erp.deposits;
DROP TABLE IF EXISTS shadow_erp.purchases;
