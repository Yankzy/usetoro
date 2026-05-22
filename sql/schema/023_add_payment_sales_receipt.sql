-- +goose Up

-- Payments table
CREATE TABLE IF NOT EXISTS shadow_erp.payments (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    erp_id TEXT NOT NULL,
    realm_id TEXT NOT NULL,
    sync_token TEXT NOT NULL,
    txn_date DATE NOT NULL,
    total_amount DECIMAL(15,2) NOT NULL,
    unapplied_amount DECIMAL(15,2) DEFAULT 0,
    customer_id TEXT,
    deposit_to_account_id TEXT,
    lines JSONB NOT NULL,
    rule_id INT REFERENCES shadow_erp.rule_groups(id),
    event_source TEXT NOT NULL DEFAULT 'toro_internal',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE(realm_id, erp_id)
);

-- SalesReceipts table
CREATE TABLE IF NOT EXISTS shadow_erp.sales_receipts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    erp_id TEXT NOT NULL,
    realm_id TEXT NOT NULL,
    sync_token TEXT NOT NULL,
    txn_date DATE NOT NULL,
    total_amount DECIMAL(15,2) NOT NULL,
    customer_id TEXT,
    deposit_to_account_id TEXT,
    doc_number TEXT,
    lines JSONB NOT NULL,
    rule_id INT REFERENCES shadow_erp.rule_groups(id),
    event_source TEXT NOT NULL DEFAULT 'toro_internal',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE(realm_id, erp_id)
);

-- Webhook timestamp columns
ALTER TABLE toro_core.erp_connections ADD COLUMN IF NOT EXISTS last_webhook_payment TIMESTAMPTZ;
ALTER TABLE toro_core.erp_connections ADD COLUMN IF NOT EXISTS last_webhook_sales_receipt TIMESTAMPTZ;

-- +goose Down
DROP TABLE IF EXISTS shadow_erp.payments;
DROP TABLE IF EXISTS shadow_erp.sales_receipts;
ALTER TABLE toro_core.erp_connections DROP COLUMN IF EXISTS last_webhook_payment;
ALTER TABLE toro_core.erp_connections DROP COLUMN IF EXISTS last_webhook_sales_receipt;
