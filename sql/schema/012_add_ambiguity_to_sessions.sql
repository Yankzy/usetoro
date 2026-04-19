-- +goose Up
ALTER TABLE fignode.staging_sessions ADD COLUMN IF NOT EXISTS is_ambiguous BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE fignode.staging_sessions ADD COLUMN IF NOT EXISTS ambiguity_reason TEXT;

-- +goose Down
ALTER TABLE fignode.staging_sessions DROP COLUMN IF EXISTS is_ambiguous;
ALTER TABLE fignode.staging_sessions DROP COLUMN IF EXISTS ambiguity_reason;
