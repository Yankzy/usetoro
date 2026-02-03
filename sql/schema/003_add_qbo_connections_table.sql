-- +goose Up
CREATE TABLE IF NOT EXISTS qbo_connections (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL,
    access_token TEXT NOT NULL,
    refresh_token TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(realm_id)
);

CREATE INDEX IF NOT EXISTS idx_qbo_realm_id ON qbo_connections(realm_id);
