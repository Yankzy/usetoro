-- +goose Up
CREATE TABLE IF NOT EXISTS webhookks_providerconnection (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    connection_id TEXT NOT NULL,
    webhook_secret TEXT NOT NULL,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(connection_id)
);

CREATE INDEX IF NOT EXISTS idx_provider_connection_id ON webhookks_providerconnection(connection_id);
