-- +goose Up
ALTER TABLE toro_core.users ADD COLUMN IF NOT EXISTS vcoo_active_blockers TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE toro_core.users ADD COLUMN IF NOT EXISTS vcoo_history JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE toro_core.users ADD COLUMN IF NOT EXISTS vcoo_state JSONB NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE toro_core.users DROP COLUMN IF EXISTS vcoo_active_blockers;
ALTER TABLE toro_core.users DROP COLUMN IF EXISTS vcoo_history;
ALTER TABLE toro_core.users DROP COLUMN IF EXISTS vcoo_state;
