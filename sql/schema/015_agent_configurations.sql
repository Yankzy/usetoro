-- +goose Up
-- =========================================================================
-- SCHEMA: toro_core
-- TABLE: agent_configurations
-- =========================================================================

CREATE TABLE IF NOT EXISTS toro_core.agent_configurations (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name          VARCHAR(255) UNIQUE NOT NULL,
    description   TEXT,
    system_prompt TEXT NOT NULL,
    sdk_client    VARCHAR(100) NOT NULL,
    metadata      JSONB DEFAULT '{}'::jsonb NOT NULL,
    created_at    TIMESTAMPTZ DEFAULT NOW() NOT NULL,
    updated_at    TIMESTAMPTZ DEFAULT NOW() NOT NULL
);

CREATE INDEX idx_agent_configurations_name ON toro_core.agent_configurations(name);

-- +goose Down
DROP TABLE IF EXISTS toro_core.agent_configurations;
