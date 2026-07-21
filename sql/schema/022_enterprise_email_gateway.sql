-- +goose Up
CREATE TABLE IF NOT EXISTS toro_core.enterprise_domains (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_id UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    domain_name TEXT NOT NULL UNIQUE,
    dkim_private_key_encrypted TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_enterprise_domains_entity_id ON toro_core.enterprise_domains(entity_id);
CREATE INDEX idx_enterprise_domains_status ON toro_core.enterprise_domains(status);

CREATE TRIGGER update_enterprise_domains_updated_at
    BEFORE UPDATE ON toro_core.enterprise_domains
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

CREATE TABLE IF NOT EXISTS toro_core.enterprise_agent_aliases (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    domain_id UUID NOT NULL REFERENCES toro_core.enterprise_domains(id) ON DELETE CASCADE,
    agent_alias TEXT NOT NULL,
    target_did TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(domain_id, agent_alias)
);

CREATE INDEX idx_enterprise_agent_aliases_domain_id ON toro_core.enterprise_agent_aliases(domain_id);

CREATE TRIGGER update_enterprise_agent_aliases_updated_at
    BEFORE UPDATE ON toro_core.enterprise_agent_aliases
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- +goose Down
DROP TRIGGER IF EXISTS update_enterprise_agent_aliases_updated_at ON toro_core.enterprise_agent_aliases;
DROP TABLE IF EXISTS toro_core.enterprise_agent_aliases;

DROP TRIGGER IF EXISTS update_enterprise_domains_updated_at ON toro_core.enterprise_domains;
DROP TABLE IF EXISTS toro_core.enterprise_domains;
