-- +goose Up
-- =========================================================================
-- SCHEMA: shadow_erp
-- The External Mirror: volatile cache of ERP data (QBO, NetSuite, Sage, etc.)
-- All tables are keyed by realm_id (ERP company ID) and join to
-- toro_core.entities via toro_core.erp_connections.realm_id.
-- =========================================================================
CREATE SCHEMA IF NOT EXISTS shadow_erp;

-- =========================================================================
-- Chart of Accounts (The AI's Navigation System)
-- =========================================================================
CREATE TABLE IF NOT EXISTS shadow_erp.accounts (
    id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    erp_id               TEXT NOT NULL,              -- Original ERP-specific Account ID
    realm_id             TEXT NOT NULL,              -- Multi-tenant isolation
    name                 TEXT NOT NULL,
    account_type         TEXT NOT NULL,              -- 'Expense', 'Revenue', 'Asset', etc.
    account_sub_type     TEXT,                       -- 'OfficeGeneralExpenses', etc.
    classification       TEXT,                       -- 'BalanceSheet' or 'IncomeStatement'
    fully_qualified_name TEXT,
    active               BOOLEAN DEFAULT true,
    sync_token           TEXT NOT NULL,              -- For collision detection
    
    -- Dynamically added QBO fields (from 006)
    domain               TEXT,
    currency_ref_name    TEXT,
    currency_ref_value   TEXT,
    current_balance_with_sub_accounts DECIMAL(15,2),
    "sparse"             BOOLEAN,
    erp_created_time     TIMESTAMPTZ,
    erp_updated_time     TIMESTAMPTZ,
    current_balance      DECIMAL(15,2),
    sub_account          BOOLEAN,

    event_source         TEXT NOT NULL DEFAULT 'toro_internal', -- CDC Event Guard (from 010)

    created_at           TIMESTAMPTZ DEFAULT NOW(),
    updated_at           TIMESTAMPTZ DEFAULT NOW(),
    deleted_at           TIMESTAMPTZ,                -- Soft delete
    UNIQUE(realm_id, erp_id)
);

CREATE INDEX IF NOT EXISTS idx_accounts_realm        ON shadow_erp.accounts(realm_id);
CREATE INDEX IF NOT EXISTS idx_accounts_realm_active ON shadow_erp.accounts(realm_id, active) WHERE deleted_at IS NULL;

-- =========================================================================
-- Vendors (The Entity Resolution Cache)
-- =========================================================================
CREATE TABLE IF NOT EXISTS shadow_erp.vendors (
    id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    erp_id               TEXT NOT NULL,
    realm_id             TEXT NOT NULL,
    display_name         TEXT NOT NULL,
    sync_token           TEXT NOT NULL,
    last_known_account_id UUID,                     -- AI hint: usual expense account
    ai_synonyms          JSONB,                     -- ["Staples Inc", "STAPLS", "Staples #44"]
    event_source         TEXT NOT NULL DEFAULT 'toro_internal',
    created_at           TIMESTAMPTZ DEFAULT NOW(),
    updated_at           TIMESTAMPTZ DEFAULT NOW(),
    deleted_at           TIMESTAMPTZ,
    UNIQUE(realm_id, erp_id),
    FOREIGN KEY (last_known_account_id) REFERENCES shadow_erp.accounts(id)
);

CREATE INDEX IF NOT EXISTS idx_vendors_realm ON shadow_erp.vendors(realm_id);
CREATE INDEX IF NOT EXISTS idx_vendors_name  ON shadow_erp.vendors(realm_id, display_name);

-- =========================================================================
-- Customers (For AR Tracking)
-- =========================================================================
CREATE TABLE IF NOT EXISTS shadow_erp.customers (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    erp_id       TEXT NOT NULL,
    realm_id     TEXT NOT NULL,
    display_name TEXT NOT NULL,
    sync_token   TEXT NOT NULL,
    event_source TEXT NOT NULL DEFAULT 'toro_internal',
    created_at   TIMESTAMPTZ DEFAULT NOW(),
    updated_at   TIMESTAMPTZ DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ,
    UNIQUE(realm_id, erp_id)
);

CREATE INDEX IF NOT EXISTS idx_customers_realm ON shadow_erp.customers(realm_id);

-- =========================================================================
-- Invoices (AR Documents)
-- =========================================================================
CREATE TABLE IF NOT EXISTS shadow_erp.invoices (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    erp_id       TEXT NOT NULL,
    realm_id     TEXT NOT NULL,
    customer_id  UUID,
    doc_number   TEXT,
    total_amount DECIMAL(15,2),
    balance      DECIMAL(15,2),
    due_date     DATE,
    txn_date     DATE,
    sync_token   TEXT NOT NULL,
    event_source TEXT NOT NULL DEFAULT 'toro_internal',
    created_at   TIMESTAMPTZ DEFAULT NOW(),
    updated_at   TIMESTAMPTZ DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ,
    UNIQUE(realm_id, erp_id),
    FOREIGN KEY (customer_id) REFERENCES shadow_erp.customers(id)
);

CREATE INDEX IF NOT EXISTS idx_invoices_realm    ON shadow_erp.invoices(realm_id);
CREATE INDEX IF NOT EXISTS idx_invoices_customer ON shadow_erp.invoices(customer_id);

-- =========================================================================
-- Bills (AP Documents)
-- =========================================================================
CREATE TABLE IF NOT EXISTS shadow_erp.bills (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    erp_id       TEXT NOT NULL,
    realm_id     TEXT NOT NULL,
    vendor_id    UUID,
    doc_number   TEXT,
    total_amount DECIMAL(15,2),
    balance      DECIMAL(15,2),
    due_date     DATE,
    txn_date     DATE,
    sync_token   TEXT NOT NULL,
    event_source TEXT NOT NULL DEFAULT 'toro_internal',
    created_at   TIMESTAMPTZ DEFAULT NOW(),
    updated_at   TIMESTAMPTZ DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ,
    UNIQUE(realm_id, erp_id),
    FOREIGN KEY (vendor_id) REFERENCES shadow_erp.vendors(id)
);

CREATE INDEX IF NOT EXISTS idx_bills_realm  ON shadow_erp.bills(realm_id);
CREATE INDEX IF NOT EXISTS idx_bills_vendor ON shadow_erp.bills(vendor_id);

-- =========================================================================
-- REMOVED: Proposed Transactions (AI Work-in-Progress)
-- Now handled unified in the fignode.staging_transactions table.
-- =========================================================================

-- =========================================================================
-- Vector Sync State (Pinecone tracking per realm)
-- =========================================================================
CREATE TABLE IF NOT EXISTS shadow_erp.vector_sync_state (
    realm_id             TEXT PRIMARY KEY,
    last_coa_sync        TIMESTAMPTZ,
    last_vendor_sync     TIMESTAMPTZ,
    last_customer_sync   TIMESTAMPTZ,
    coa_vector_count     INT DEFAULT 0,
    vendor_vector_count  INT DEFAULT 0,
    customer_vector_count INT DEFAULT 0,
    event_source         TEXT NOT NULL DEFAULT 'toro_internal',
    created_at           TIMESTAMPTZ DEFAULT NOW(),
    updated_at           TIMESTAMPTZ DEFAULT NOW()
);

COMMENT ON TABLE  shadow_erp.vector_sync_state IS 'Tracks Pinecone vector database sync state per ERP realm';
COMMENT ON COLUMN shadow_erp.vector_sync_state.last_coa_sync IS 'Last Chart of Accounts sync to Pinecone';
COMMENT ON COLUMN shadow_erp.vector_sync_state.coa_vector_count IS 'Number of account vectors in Pinecone';

-- =========================================================================
-- AI Corrections (Learning from user feedback)
-- =========================================================================
CREATE TABLE IF NOT EXISTS shadow_erp.ai_corrections (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id        TEXT NOT NULL,
    user_id         UUID REFERENCES toro_core.users(id),   -- Who made the correction
    raw_input       TEXT NOT NULL,                         -- Original raw text (e.g., "STAPLS #452")
    ai_prediction   TEXT,                                  -- What the AI predicted
    user_correction TEXT NOT NULL,                         -- What the user corrected to
    correction_type TEXT NOT NULL,                         -- 'vendor', 'customer', 'account'
    confidence_score DECIMAL(3,2),                         -- AI's original confidence (0.00-1.00)
    event_source    TEXT NOT NULL DEFAULT 'toro_internal',
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ai_corrections_user    ON shadow_erp.ai_corrections(user_id);
CREATE INDEX IF NOT EXISTS idx_ai_corrections_realm   ON shadow_erp.ai_corrections(realm_id, correction_type);
CREATE INDEX IF NOT EXISTS idx_ai_corrections_created ON shadow_erp.ai_corrections(created_at DESC);

COMMENT ON TABLE  shadow_erp.ai_corrections IS 'Records when users correct AI predictions for learning and synonym updates';
COMMENT ON COLUMN shadow_erp.ai_corrections.raw_input IS 'The original text that AI tried to match';
COMMENT ON COLUMN shadow_erp.ai_corrections.user_correction IS 'The correct entity ID provided by user';

-- =========================================================================
-- Rule Engine (from 007 & 008)
-- =========================================================================

-- Rule Groups hold the hierarchical logic and metadata
CREATE TABLE IF NOT EXISTS shadow_erp.rule_groups (
    id SERIAL PRIMARY KEY,
    realm_id TEXT NOT NULL, -- To isolate rules per tenant/connection
    name VARCHAR(255) NOT NULL,
    logic VARCHAR(10) NOT NULL DEFAULT 'AND', -- 'AND' or 'OR'
    priority INT NOT NULL DEFAULT 0,
    keywords TEXT, -- Auto-generated for optimization
    active BOOLEAN NOT NULL DEFAULT true,
    
    -- Resolution Targets
    target_account_id UUID REFERENCES shadow_erp.accounts(id) ON DELETE SET NULL,
    target_vendor_id UUID REFERENCES shadow_erp.vendors(id) ON DELETE SET NULL,

    parent_id INT REFERENCES shadow_erp.rule_groups(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT timezone('utc', now()) NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT timezone('utc', now()) NOT NULL
);

-- Indexes for fast filtering
CREATE INDEX IF NOT EXISTS idx_rule_groups_realm ON shadow_erp.rule_groups(realm_id);
CREATE INDEX IF NOT EXISTS idx_rule_groups_parent ON shadow_erp.rule_groups(parent_id);
CREATE INDEX IF NOT EXISTS idx_rule_groups_active ON shadow_erp.rule_groups(active) WHERE active = true;

-- Rule Conditions define the specific matching criteria for a group
CREATE TABLE IF NOT EXISTS shadow_erp.rule_conditions (
    id SERIAL PRIMARY KEY,
    rule_group_id INT NOT NULL REFERENCES shadow_erp.rule_groups(id) ON DELETE CASCADE,
    field VARCHAR(50) NOT NULL,    -- 'description', 'vendor', etc.
    operator VARCHAR(50) NOT NULL, -- 'equals', 'contains', etc.
    value TEXT NOT NULL,           -- The target value to match against
    created_at TIMESTAMP WITH TIME ZONE DEFAULT timezone('utc', now()) NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT timezone('utc', now()) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_rule_conditions_group ON shadow_erp.rule_conditions(rule_group_id);

CREATE TABLE IF NOT EXISTS shadow_erp.rule_audit_logs (
    id SERIAL PRIMARY KEY,
    realm_id TEXT NOT NULL,
    transaction_id UUID NOT NULL,
    rule_group_id INT REFERENCES shadow_erp.rule_groups(id) ON DELETE SET NULL,
    matched BOOLEAN NOT NULL,
    match_info JSONB DEFAULT '{}'::jsonb NOT NULL,
    human_readable_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT timezone('utc', now()) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_rule_audit_logs_realm ON shadow_erp.rule_audit_logs(realm_id);
CREATE INDEX IF NOT EXISTS idx_rule_audit_logs_transaction ON shadow_erp.rule_audit_logs(transaction_id);
CREATE INDEX IF NOT EXISTS idx_rule_audit_logs_group ON shadow_erp.rule_audit_logs(rule_group_id);

COMMENT ON COLUMN shadow_erp.rule_audit_logs.match_info IS 'Verbose match explanation: JSON containing condition-level results (maps to MatchExplanation struct)';

-- =========================================================================
-- Company Info Cache (mirrors QBO CompanyInfo per realm) (from 009)
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

    event_source         TEXT NOT NULL DEFAULT 'toro_internal',
    erp_created_time     TIMESTAMPTZ,
    erp_updated_time     TIMESTAMPTZ,
    created_at           TIMESTAMPTZ DEFAULT NOW(),
    updated_at           TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_company_info_realm ON shadow_erp.company_info(realm_id);

COMMENT ON TABLE shadow_erp.company_info IS 'Mirror of QBO CompanyInfo; keyed by realm_id. One row per connected QBO company.';

-- =========================================================================
-- REMOVED: Clean-Up Mode: staging pipeline for messy/uncategorized transactions.
-- Now handled unified in the fignode.staging_sessions & fignode.staging_transactions tables.
-- =========================================================================

-- =========================================================================
-- Attachables (from 015)
-- =========================================================================
CREATE TABLE IF NOT EXISTS shadow_erp.attachables (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id VARCHAR(255) NOT NULL,
    erp_id VARCHAR(255) NOT NULL,
    file_name VARCHAR(255),
    content_type VARCHAR(255),
    size NUMERIC,
    note TEXT,
    attachable_refs JSONB,
    sync_token VARCHAR(255),
    erp_created_time TIMESTAMPTZ,
    erp_updated_time TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (realm_id, erp_id)
);

CREATE INDEX IF NOT EXISTS idx_attachables_realm_id ON shadow_erp.attachables(realm_id);

-- +goose Down
DROP TABLE IF EXISTS shadow_erp.attachables;
DROP TABLE IF EXISTS shadow_erp.bills;
DROP TABLE IF EXISTS shadow_erp.invoices;
DROP TABLE IF EXISTS shadow_erp.customers;
DROP TABLE IF EXISTS shadow_erp.vendors;
DROP TABLE IF EXISTS shadow_erp.accounts;
DROP SCHEMA IF EXISTS shadow_erp CASCADE;
