-- +goose Up
ALTER TABLE fignode.staging_sessions ADD COLUMN outflow_is TEXT NOT NULL DEFAULT 'NEGATIVE';

-- +goose Down
ALTER TABLE fignode.staging_sessions DROP COLUMN outflow_is;
