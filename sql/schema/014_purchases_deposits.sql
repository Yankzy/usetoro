-- +goose Up
-- The Local Purchase Table (Money Out)
CREATE TABLE IF NOT EXISTS shadow_erp.purchases (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    erp_id TEXT NOT NULL,                  -- QBO Purchase ID (e.g., "252")
    realm_id TEXT NOT NULL,                -- The CPA's Client ID
    sync_token TEXT NOT NULL,              -- Required for QBO updates/conflict resolution
    
    txn_date DATE NOT NULL,
    total_amount DECIMAL(15,2) NOT NULL,
    payment_type TEXT,                     -- 'Cash', 'Check', 'CreditCard'
    
    -- The TOP-LEVEL Account (Where the money came from)
    source_account_id TEXT NOT NULL,       -- e.g., "35" (Checking)
    
    -- The VENDOR (Who we paid)
    entity_id TEXT,                        -- e.g., Vendor ID. Nullable because of anonymous receipts.
    
    -- THE LINE ITEMS (What we bought - contains the second AccountRefs)
    lines JSONB NOT NULL,                  -- Holds the array of AccountBasedExpenseLineDetail
    rule_id INT REFERENCES shadow_erp.rule_groups(id),
    event_source TEXT NOT NULL DEFAULT 'toro_internal',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE(realm_id, erp_id)
);

CREATE TABLE IF NOT EXISTS shadow_erp.deposits (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    erp_id TEXT NOT NULL,                  -- QBO Deposit ID (e.g., "148")
    realm_id TEXT NOT NULL,                -- The CPA's Client ID
    sync_token TEXT NOT NULL,              -- For QBO collision detection
    
    -- Transaction Data
    txn_date DATE NOT NULL,
    total_amount DECIMAL(15,2) NOT NULL,
    target_account_id TEXT NOT NULL,       -- Mapped from DepositToAccountRef.value
    
    -- Line Items (JSONB handles both DepositLineDetail AND LinkedTxn arrays)
    lines JSONB NOT NULL,                  
    
    -- QBO System Metadata (Added from your JSON)
    domain TEXT,                           -- e.g., 'QBO'
    sparse BOOLEAN,                        -- True if QBO sent a partial update payload
    erp_created_time TIMESTAMPTZ,          -- Mapped from MetaData.CreateTime
    erp_updated_time TIMESTAMPTZ,          -- Mapped from MetaData.LastUpdatedTime
    
    -- Internal State
    rule_id INT REFERENCES shadow_erp.rule_groups(id),
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
