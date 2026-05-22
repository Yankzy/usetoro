-- +goose Up
-- Change raw_date from DATE to TEXT so raw CSV date strings are never lost on parse failure.
ALTER TABLE fignode.staging_transactions
  ALTER COLUMN raw_date TYPE TEXT USING raw_date::TEXT;

-- Add parsed_date for successfully parsed date values used in sorting, comparisons, and date operations.
ALTER TABLE fignode.staging_transactions
  ADD COLUMN parsed_date DATE;

-- +goose Down
ALTER TABLE fignode.staging_transactions
  DROP COLUMN IF EXISTS parsed_date;

ALTER TABLE fignode.staging_transactions
  ALTER COLUMN raw_date TYPE DATE USING raw_date::DATE;
