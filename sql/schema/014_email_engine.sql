-- +goose Up

CREATE SCHEMA IF NOT EXISTS marketing;

CREATE TABLE IF NOT EXISTS marketing.email_accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) NOT NULL UNIQUE,
    encrypted_password TEXT NOT NULL,
    daily_send_count INTEGER NOT NULL DEFAULT 0,
    daily_send_limit INTEGER NOT NULL DEFAULT 25,
    status VARCHAR(50) NOT NULL DEFAULT 'active', -- active, warming, cooling, burned
    tenant_id UUID NOT NULL,
    warmup_phase INTEGER NOT NULL DEFAULT 1,
    domain_created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    bounce_count INTEGER NOT NULL DEFAULT 0,
    spam_complaints INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS marketing.campaigns (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    tenant_id UUID NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'draft',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS marketing.campaign_steps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id UUID NOT NULL REFERENCES marketing.campaigns(id) ON DELETE CASCADE,
    step_number INTEGER NOT NULL,
    subject_template TEXT NOT NULL,
    body_template TEXT NOT NULL,
    delay_duration INTERVAL NOT NULL DEFAULT '0 days',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (campaign_id, step_number)
);

CREATE TABLE IF NOT EXISTS marketing.prospects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) NOT NULL,
    first_name VARCHAR(100),
    last_name VARCHAR(100),
    metadata JSONB DEFAULT '{}',
    campaign_id UUID REFERENCES marketing.campaigns(id) ON DELETE SET NULL,
    current_step_id UUID REFERENCES marketing.campaign_steps(id) ON DELETE SET NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'active', -- active, paused, opted_out, bounced
    tenant_id UUID NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, email)
);

CREATE TABLE IF NOT EXISTS marketing.email_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    prospect_id UUID NOT NULL REFERENCES marketing.prospects(id) ON DELETE CASCADE,
    campaign_id UUID NOT NULL REFERENCES marketing.campaigns(id) ON DELETE CASCADE,
    nats_msg_id VARCHAR(255),
    event_type VARCHAR(50) NOT NULL, -- sent, opened, clicked, replied, bounced
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_email_accounts_status ON marketing.email_accounts(status);
CREATE INDEX idx_prospects_status ON marketing.prospects(status);
CREATE INDEX idx_prospects_campaign_id ON marketing.prospects(campaign_id);
CREATE INDEX idx_email_logs_prospect_id ON marketing.email_logs(prospect_id);
CREATE INDEX idx_email_logs_nats_msg_id ON marketing.email_logs(nats_msg_id);

-- +goose Down

DROP TABLE IF EXISTS marketing.email_logs;
DROP TABLE IF EXISTS marketing.prospects;
DROP TABLE IF EXISTS marketing.campaign_steps;
DROP TABLE IF EXISTS marketing.campaigns;
DROP TABLE IF EXISTS marketing.email_accounts;
