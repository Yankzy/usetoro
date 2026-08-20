-- +goose Up

-- =========================================================================
-- Fix F-02: enterprise_facts.uri UNIQUE constraint must be per-realm
-- The global UNIQUE(uri) allows cross-tenant data collision.
-- Two tenants with a vendor "Acme Corp" would collide on the same URI.
-- =========================================================================

-- Drop the existing global unique constraint on uri
ALTER TABLE toro_core.enterprise_facts DROP CONSTRAINT IF EXISTS enterprise_facts_uri_key;

-- Create per-realm unique constraint
ALTER TABLE toro_core.enterprise_facts
    ADD CONSTRAINT enterprise_facts_realm_uri_key UNIQUE (session_id, uri);

-- =========================================================================
-- Fix F-13: Add updated_at columns to enterprise_facts and enterprise_relationships
-- These tables only had created_at, making it impossible to track mutations.
-- =========================================================================

ALTER TABLE toro_core.enterprise_facts
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

CREATE TRIGGER update_enterprise_facts_updated_at
    BEFORE UPDATE ON toro_core.enterprise_facts
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

ALTER TABLE toro_core.enterprise_relationships
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

CREATE TRIGGER update_enterprise_relationships_updated_at
    BEFORE UPDATE ON toro_core.enterprise_relationships
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- +goose Down
DROP TRIGGER IF EXISTS update_enterprise_relationships_updated_at ON toro_core.enterprise_relationships;
ALTER TABLE toro_core.enterprise_relationships DROP COLUMN IF EXISTS updated_at;

DROP TRIGGER IF EXISTS update_enterprise_facts_updated_at ON toro_core.enterprise_facts;
ALTER TABLE toro_core.enterprise_facts DROP COLUMN IF EXISTS updated_at;

ALTER TABLE toro_core.enterprise_facts DROP CONSTRAINT IF EXISTS enterprise_facts_realm_uri_key;
ALTER TABLE toro_core.enterprise_facts
    ADD CONSTRAINT enterprise_facts_uri_key UNIQUE (uri);
