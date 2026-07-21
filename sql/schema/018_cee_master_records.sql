-- +goose Up
-- =========================================================================
-- SCHEMA: fignode
-- TABLES: master_merchants, master_patterns
-- =========================================================================

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE IF NOT EXISTS fignode.master_merchants (
    id                    BIGSERIAL PRIMARY KEY,
    normalized_name       VARCHAR(255) UNIQUE NOT NULL,
    primary_domain        VARCHAR(255) NOT NULL DEFAULT '',
    logo_url              TEXT NOT NULL DEFAULT '',
    mcc                   INT NOT NULL DEFAULT 0,
    naics                 VARCHAR(10) NOT NULL DEFAULT '',
    default_macro_class   VARCHAR(50) NOT NULL DEFAULT 'EXPENSE',
    default_qbo_category  VARCHAR(100) NOT NULL DEFAULT '',
    irs_receipt_threshold NUMERIC(10, 2) NOT NULL DEFAULT 75.00
);

CREATE TABLE IF NOT EXISTS fignode.master_patterns (
    id                 BIGSERIAL PRIMARY KEY,
    cleaned_stem       VARCHAR(255) UNIQUE NOT NULL,
    master_merchant_id BIGINT NOT NULL REFERENCES fignode.master_merchants(id) ON DELETE CASCADE,
    is_intermediary    BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE INDEX IF NOT EXISTS idx_master_patterns_cleaned_stem_trgm ON fignode.master_patterns USING GIN (cleaned_stem gin_trgm_ops);

-- +goose Down
DROP INDEX IF EXISTS fignode.idx_master_patterns_cleaned_stem_trgm;
DROP TABLE IF EXISTS fignode.master_patterns;
DROP TABLE IF EXISTS fignode.master_merchants;
