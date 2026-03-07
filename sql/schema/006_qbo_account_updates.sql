-- +goose Up
ALTER TABLE shadow_erp.accounts
ADD COLUMN domain TEXT,
ADD COLUMN currency_ref_name TEXT,
ADD COLUMN currency_ref_value TEXT,
ADD COLUMN current_balance_with_sub_accounts DECIMAL(15,2),
ADD COLUMN "sparse" BOOLEAN,
ADD COLUMN erp_created_time TIMESTAMPTZ,
ADD COLUMN erp_updated_time TIMESTAMPTZ,
ADD COLUMN current_balance DECIMAL(15,2),
ADD COLUMN sub_account BOOLEAN;

-- +goose Down
ALTER TABLE shadow_erp.accounts
DROP COLUMN domain,
DROP COLUMN currency_ref_name,
DROP COLUMN currency_ref_value,
DROP COLUMN current_balance_with_sub_accounts,
DROP COLUMN "sparse",
DROP COLUMN erp_created_time,
DROP COLUMN erp_updated_time,
DROP COLUMN current_balance,
DROP COLUMN sub_account;
