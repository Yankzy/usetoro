-- +goose Up
-- Backfill parsed_date for rows where raw_date exists but parsed_date was NULL
-- due to invisible Unicode characters in the CSV date strings.
-- Only attempts to cast well-formed ISO dates; rows with exotic formats will
-- be fixed on next re-ingestion by the updated parseCSVDate sanitizer.
UPDATE fignode.staging_transactions
SET parsed_date = TRIM(raw_date)::DATE
WHERE parsed_date IS NULL
  AND raw_date IS NOT NULL
  AND TRIM(raw_date) ~ '^\d{4}-\d{2}-\d{2}$';

-- +goose Down
-- No-op: we don't want to re-NULL valid parsed dates.
