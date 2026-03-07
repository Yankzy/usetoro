-- +goose Up
-- =========================================================================
-- Company Info Cache (mirrors QBO CompanyInfo per realm)
-- =========================================================================
CREATE TABLE IF NOT EXISTS shadow_erp.company_info (
    id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id             TEXT NOT NULL UNIQUE,
    erp_id               TEXT NOT NULL,
    sync_token           TEXT NOT NULL,

    company_name         TEXT NOT NULL,
    legal_name           TEXT,
    domain               TEXT,
    country              TEXT,
    fiscal_year_start_month TEXT,
    company_start_date   DATE,
    supported_languages  TEXT,

    -- Address fields stored as JSONB for flexibility (PhysicalAddress)
    company_addr         JSONB,
    legal_addr           JSONB,

    primary_phone        TEXT,
    email                TEXT,
    web_addr             TEXT,

    -- Sparse preference bag (NameValue pairs from QBO)
    name_values          JSONB,

    erp_created_time     TIMESTAMPTZ,
    erp_updated_time     TIMESTAMPTZ,
    created_at           TIMESTAMPTZ DEFAULT NOW(),
    updated_at           TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_company_info_realm ON shadow_erp.company_info(realm_id);

COMMENT ON TABLE shadow_erp.company_info IS 'Mirror of QBO CompanyInfo; keyed by realm_id. One row per connected QBO company.';

-- +goose Down
DROP TABLE IF EXISTS shadow_erp.company_info;
