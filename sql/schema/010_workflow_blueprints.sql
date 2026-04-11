-- +goose NO TRANSACTION
-- +goose Up

-- =========================================================================
-- SCHEMA: toro_core.workflow_blueprints
-- Declarative workflow definitions (blueprints) stored in Postgres.
-- =========================================================================

CREATE TABLE IF NOT EXISTS toro_core.workflow_blueprints (
    name          TEXT PRIMARY KEY,
    trigger_topic TEXT NOT NULL,
    definition    JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_workflow_blueprints_trigger_topic
    ON toro_core.workflow_blueprints(trigger_topic);

-- Attach the standard toro_core updated_at trigger
DROP TRIGGER IF EXISTS update_workflow_blueprints_updated_at ON toro_core.workflow_blueprints;
CREATE TRIGGER update_workflow_blueprints_updated_at
    BEFORE UPDATE ON toro_core.workflow_blueprints
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- +goose Down
DROP TRIGGER IF EXISTS update_workflow_blueprints_updated_at ON toro_core.workflow_blueprints;
DROP TABLE IF EXISTS toro_core.workflow_blueprints;

