-- +goose Up
ALTER TABLE fignode.staging_transactions DROP COLUMN IF EXISTS v2_status;
ALTER TABLE fignode.staging_transactions DROP COLUMN IF EXISTS v2_transfer_hold_reason;
ALTER TABLE fignode.staging_transactions DROP COLUMN IF EXISTS v2_erp_transaction_id;

-- Add execution trace column, defaulting to empty JSON array
ALTER TABLE fignode.staging_transactions ADD COLUMN ase_execution_trace JSONB NOT NULL DEFAULT '[]'::jsonb;

-- +goose Down
ALTER TABLE fignode.staging_transactions DROP COLUMN IF EXISTS ase_execution_trace;

ALTER TABLE fignode.staging_transactions ADD COLUMN v2_status TEXT;
ALTER TABLE fignode.staging_transactions ADD COLUMN v2_transfer_hold_reason TEXT;
ALTER TABLE fignode.staging_transactions ADD COLUMN v2_erp_transaction_id TEXT;
