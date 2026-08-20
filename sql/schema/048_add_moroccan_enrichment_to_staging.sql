-- +goose Up
-- =========================================================================
-- SCHEMA: fignode
-- MIGRATION: 048_add_moroccan_enrichment_to_staging.sql
-- DESCRIPTION: Add moroccan_enrichment JSONB column to fignode.staging_transactions
-- =========================================================================

ALTER TABLE fignode.staging_transactions
ADD COLUMN IF NOT EXISTS moroccan_enrichment JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS idx_staging_transactions_moroccan_enrichment_gin 
ON fignode.staging_transactions USING GIN (moroccan_enrichment);

-- +goose Down
DROP INDEX IF EXISTS fignode.idx_staging_transactions_moroccan_enrichment_gin;
ALTER TABLE fignode.staging_transactions DROP COLUMN IF EXISTS moroccan_enrichment;
