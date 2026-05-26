-- +goose Up
ALTER TABLE fignode.staging_transactions ADD COLUMN v2_status TEXT;
ALTER TABLE fignode.staging_transactions ADD COLUMN v2_transfer_hold_reason TEXT;
ALTER TABLE fignode.staging_transactions ADD COLUMN v2_erp_transaction_id TEXT;

-- +goose Down
ALTER TABLE fignode.staging_transactions DROP COLUMN IF EXISTS v2_erp_transaction_id;
ALTER TABLE fignode.staging_transactions DROP COLUMN IF EXISTS v2_transfer_hold_reason;
ALTER TABLE fignode.staging_transactions DROP COLUMN IF EXISTS v2_status;
