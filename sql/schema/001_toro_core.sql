-- +goose Up
-- Enable required extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- =========================================================================
-- SCHEMA: toro_core
-- The Nervous System: Hierarchy, Users, Memory, and Internal Ledger
-- =========================================================================
CREATE SCHEMA IF NOT EXISTS toro_core;

-- =========================================================================
-- 1. Entities (The Hierarchy: Apex CPA -> Sub CPA -> Client)
-- Replaces the old flat `tenants` table.
-- =========================================================================
CREATE TABLE toro_core.entities (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    parent_id     UUID REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    name          VARCHAR(255) NOT NULL,
    entity_type   VARCHAR(50) NOT NULL,    -- 'apex_cpa', 'sub_cpa', 'client'
    erp_provider  VARCHAR(50),             -- 'qbo', 'netsuite', 'sage', NULL
    erp_tenant_id VARCHAR(255),            -- The ID of this entity inside the ERP
    plan_tier     TEXT DEFAULT 'basic',    -- 'basic', 'pro', 'enterprise'
    status        TEXT DEFAULT 'active',   -- 'active', 'suspended', 'archived'
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    updated_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_entities_parent ON toro_core.entities(parent_id);
CREATE INDEX idx_entities_status ON toro_core.entities(status);
CREATE INDEX idx_entities_type   ON toro_core.entities(entity_type);

-- =========================================================================
-- 2. Updated_at Trigger Function (shared across toro_core tables)
-- =========================================================================
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION toro_core.update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE 'plpgsql';
-- +goose StatementEnd

CREATE TRIGGER update_entities_updated_at
    BEFORE UPDATE ON toro_core.entities
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- =========================================================================
-- 3. Users (The Humans, scoped to an Entity)
-- =========================================================================
CREATE TABLE toro_core.users (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_id     UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    email         TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,           -- Argon2id hash
    full_name     TEXT,
    role          TEXT DEFAULT 'member',   -- 'owner', 'admin', 'member'
    is_active     BOOLEAN DEFAULT TRUE,
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    updated_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_users_email     ON toro_core.users(email);
CREATE INDEX idx_users_entity_id ON toro_core.users(entity_id);

CREATE TRIGGER update_users_updated_at
    BEFORE UPDATE ON toro_core.users
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- =========================================================================
-- 4. Refresh Tokens (Long-lived sessions)
-- =========================================================================
CREATE TABLE toro_core.refresh_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES toro_core.users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    ip_address TEXT,
    user_agent TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- =========================================================================
-- 5. Webhook Provider Connections
-- =========================================================================
CREATE TABLE toro_core.webhooks_providerconnection (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    connection_id  TEXT NOT NULL,
    webhook_secret TEXT NOT NULL,
    is_active      BOOLEAN DEFAULT true,
    created_at     TIMESTAMPTZ DEFAULT NOW(),
    updated_at     TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(connection_id)
);

CREATE INDEX idx_provider_connection_id ON toro_core.webhooks_providerconnection(connection_id);

-- =========================================================================
-- 6. Agent Memory Rules (Scoped to QBO realm)
-- =========================================================================
CREATE TABLE toro_core.agent_memory_rules (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id     TEXT NOT NULL,
    entity_type  TEXT NOT NULL,   -- e.g., "vendor", "description"
    entity_value TEXT NOT NULL,   -- e.g., "Home Depot", "Uber"
    instruction  TEXT NOT NULL,   -- e.g., "Always categorize as 'Repairs'"
    source       TEXT DEFAULT 'user_correction', -- 'user_correction', 'admin_override'
    created_at   TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(realm_id, entity_type, entity_value)
);

CREATE INDEX idx_memory_realm_lookup ON toro_core.agent_memory_rules(realm_id, entity_value);

-- =========================================================================
-- 7. Internal Toro Transactions (NOT ERP data)
-- RLS enabled for strict isolation.
-- =========================================================================
CREATE TABLE toro_core.transactions (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_id     UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    external_id   TEXT NOT NULL,
    amount_micros BIGINT NOT NULL,
    description   TEXT,
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(entity_id, external_id)
);

ALTER TABLE toro_core.transactions ENABLE ROW LEVEL SECURITY;

CREATE POLICY entity_isolation_policy ON toro_core.transactions
    FOR ALL
    USING (entity_id = current_setting('app.current_entity')::UUID)
    WITH CHECK (entity_id = current_setting('app.current_entity')::UUID);

-- +goose Down
DROP TABLE IF EXISTS toro_core.transactions;
DROP TABLE IF EXISTS toro_core.agent_memory_rules;
DROP TABLE IF EXISTS toro_core.webhooks_providerconnection;
DROP TABLE IF EXISTS toro_core.refresh_tokens;
DROP TABLE IF EXISTS toro_core.users;
DROP TABLE IF EXISTS toro_core.entities;
DROP FUNCTION IF EXISTS toro_core.update_updated_at_column();
DROP SCHEMA IF EXISTS toro_core;
