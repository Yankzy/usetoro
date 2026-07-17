-- +goose Up

CREATE TABLE IF NOT EXISTS marketing.lists (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    name VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS marketing.list_subscribers (
    list_id UUID NOT NULL REFERENCES marketing.lists(id) ON DELETE CASCADE,
    prospect_id UUID NOT NULL REFERENCES marketing.prospects(id) ON DELETE CASCADE,
    status VARCHAR(50) NOT NULL DEFAULT 'active', -- active, unsubscribed
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (list_id, prospect_id)
);

CREATE TABLE IF NOT EXISTS marketing.conversions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    prospect_id UUID REFERENCES marketing.prospects(id) ON DELETE SET NULL,
    customer_email VARCHAR(255) NOT NULL,
    conversion_value INTEGER NOT NULL, -- Stored in cents
    source VARCHAR(100),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS marketing.attributions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversion_id UUID NOT NULL REFERENCES marketing.conversions(id) ON DELETE CASCADE,
    email_log_id UUID NOT NULL REFERENCES marketing.email_logs(id) ON DELETE CASCADE,
    campaign_id UUID REFERENCES marketing.campaigns(id) ON DELETE SET NULL,
    list_id UUID REFERENCES marketing.lists(id) ON DELETE SET NULL,
    attributed_value INTEGER NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(conversion_id)
);

CREATE TABLE IF NOT EXISTS marketing.tenant_settings (
    tenant_id UUID PRIMARY KEY,
    attribution_window_days INTEGER NOT NULL DEFAULT 5,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

ALTER TABLE marketing.email_logs
    ADD COLUMN IF NOT EXISTS list_id UUID REFERENCES marketing.lists(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS user_agent TEXT,
    ADD COLUMN IF NOT EXISTS ip_address VARCHAR(255),
    ADD COLUMN IF NOT EXISTS is_human BOOLEAN DEFAULT true;

-- Indexes for performance on matchmaker queries
CREATE INDEX idx_conversions_customer_email ON marketing.conversions(customer_email);
CREATE INDEX idx_email_logs_is_human ON marketing.email_logs(is_human);

-- +goose Down

ALTER TABLE marketing.email_logs
    DROP COLUMN IF EXISTS is_human,
    DROP COLUMN IF EXISTS ip_address,
    DROP COLUMN IF EXISTS user_agent,
    DROP COLUMN IF EXISTS list_id;

DROP TABLE IF EXISTS marketing.tenant_settings;
DROP TABLE IF EXISTS marketing.attributions;
DROP TABLE IF EXISTS marketing.conversions;
DROP TABLE IF EXISTS marketing.list_subscribers;
DROP TABLE IF EXISTS marketing.lists;
