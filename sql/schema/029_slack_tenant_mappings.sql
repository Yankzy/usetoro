-- +goose Up
CREATE TABLE toro_core.slack_tenant_mappings (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id           UUID NOT NULL REFERENCES toro_core.entities(id) ON DELETE CASCADE,
    slack_team_id       TEXT NOT NULL UNIQUE,
    slack_access_token  TEXT NOT NULL,
    slack_bot_user_id   TEXT NOT NULL,
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    updated_at          TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_slack_mapping_tenant ON toro_core.slack_tenant_mappings(tenant_id);

CREATE TRIGGER update_slack_mapping_updated_at
    BEFORE UPDATE ON toro_core.slack_tenant_mappings
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- +goose Down
DROP TRIGGER IF EXISTS update_slack_mapping_updated_at ON toro_core.slack_tenant_mappings;
DROP TABLE IF EXISTS toro_core.slack_tenant_mappings;
