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
    created_at           TIMESTAMPTZ DEFAULT NOW(),
    updated_at           TIMESTAMPTZ DEFAULT NOW(),
    deleted_at           TIMESTAMPTZ,               -- Soft delete
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
    created_at   TIMESTAMPTZ DEFAULT NOW(),
    updated_at   TIMESTAMPTZ DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ,
    UNIQUE(realm_id, erp_id),
    FOREIGN KEY (vendor_id) REFERENCES shadow_erp.vendors(id)
);

CREATE INDEX IF NOT EXISTS idx_bills_realm  ON shadow_erp.bills(realm_id);
CREATE INDEX IF NOT EXISTS idx_bills_vendor ON shadow_erp.bills(vendor_id);

-- =========================================================================
-- Proposed Transactions (AI Work-in-Progress)
-- =========================================================================
CREATE TABLE IF NOT EXISTS shadow_erp.proposed_transactions (
    id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id             TEXT NOT NULL,
    source_type          TEXT NOT NULL,             -- 'Receipt', 'BankFeed', 'Email'
    raw_amount           DECIMAL(15,2) NOT NULL,
    raw_date             DATE,
    raw_description      TEXT,

    -- AI Predictions
    predicted_vendor_id  UUID,
    predicted_account_id UUID,
    confidence_score     DECIMAL(3,2),              -- 0.00 to 1.00
    ai_reasoning         TEXT,                      -- e.g., "Matched 'Shell' via synonym"

    -- ERP Link (Once synced)
    erp_transaction_id   TEXT,
    sync_status          TEXT DEFAULT 'PENDING',    -- 'PENDING', 'SYNCED', 'ERROR', 'REJECTED'
    error_message        TEXT,

    created_at           TIMESTAMPTZ DEFAULT NOW(),
    updated_at           TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_proposed_realm   ON shadow_erp.proposed_transactions(realm_id);
CREATE INDEX IF NOT EXISTS idx_proposed_status  ON shadow_erp.proposed_transactions(realm_id, sync_status);

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
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ai_corrections_user    ON shadow_erp.ai_corrections(user_id);
CREATE INDEX IF NOT EXISTS idx_ai_corrections_realm   ON shadow_erp.ai_corrections(realm_id, correction_type);
CREATE INDEX IF NOT EXISTS idx_ai_corrections_created ON shadow_erp.ai_corrections(created_at DESC);

COMMENT ON TABLE  shadow_erp.ai_corrections IS 'Records when users correct AI predictions for learning and synonym updates';
COMMENT ON COLUMN shadow_erp.ai_corrections.raw_input IS 'The original text that AI tried to match';
COMMENT ON COLUMN shadow_erp.ai_corrections.user_correction IS 'The correct entity ID provided by user';

-- +goose Down
DROP TABLE IF EXISTS shadow_erp.ai_corrections;
DROP TABLE IF EXISTS shadow_erp.vector_sync_state;
DROP TABLE IF EXISTS shadow_erp.proposed_transactions;
DROP TABLE IF EXISTS shadow_erp.bills;
DROP TABLE IF EXISTS shadow_erp.invoices;
DROP TABLE IF EXISTS shadow_erp.customers;
DROP TABLE IF EXISTS shadow_erp.vendors;
DROP TABLE IF EXISTS shadow_erp.accounts;
DROP SCHEMA IF EXISTS shadow_erp;
