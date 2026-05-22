-- +goose Up
ALTER TABLE fignode.staging_transactions
ADD COLUMN macro_class TEXT,
ADD COLUMN account_type TEXT;

-- +goose Down
ALTER TABLE fignode.staging_transactions
DROP COLUMN macro_class,
DROP COLUMN account_type;
