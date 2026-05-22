-- +goose Up
ALTER TABLE fignode.staging_transactions ADD COLUMN synced_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE fignode.staging_transactions DROP COLUMN IF EXISTS synced_at;
