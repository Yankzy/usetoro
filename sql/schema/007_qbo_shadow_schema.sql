-- +goose Up
-- Create QBO namespace (schema) for Shadow DB
CREATE SCHEMA IF NOT EXISTS qbo;

-- Chart of Accounts (The AI's Navigation System)
CREATE TABLE IF NOT EXISTS qbo.accounts (
    id TEXT PRIMARY KEY,                    -- QBO Account ID
    realm_id TEXT NOT NULL,                 -- Multi-tenant isolation
    name TEXT NOT NULL,
    account_type TEXT NOT NULL,             -- 'Expense', 'Revenue', 'Asset', etc.
    account_sub_type TEXT,                  -- 'OfficeGeneralExpenses', etc.
    classification TEXT,                    -- 'BalanceSheet' or 'IncomeStatement'
    fully_qualified_name TEXT,
    active BOOLEAN DEFAULT true,
    sync_token TEXT NOT NULL,               -- For collision detection
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,                 -- Soft delete
    UNIQUE(realm_id, id)
);

CREATE INDEX IF NOT EXISTS idx_qbo_accounts_realm ON qbo.accounts(realm_id);
CREATE INDEX IF NOT EXISTS idx_qbo_accounts_active ON qbo.accounts(realm_id, active) WHERE deleted_at IS NULL;

-- Vendors (The Entity Resolution Cache)
CREATE TABLE IF NOT EXISTS qbo.vendors (
    id TEXT PRIMARY KEY,                    -- QBO Vendor ID
    realm_id TEXT NOT NULL,
    display_name TEXT NOT NULL,
    sync_token TEXT NOT NULL,
    last_known_account_id TEXT,             -- AI hint: usual expense account
    ai_synonyms JSONB,                      -- ["Staples Inc", "STAPLS", "Staples #44"]
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE(realm_id, id)
);

CREATE INDEX IF NOT EXISTS idx_qbo_vendors_realm ON qbo.vendors(realm_id);
CREATE INDEX IF NOT EXISTS idx_qbo_vendors_name ON qbo.vendors(realm_id, display_name);

-- Customers (For AR tracking)
CREATE TABLE IF NOT EXISTS qbo.customers (
    id TEXT PRIMARY KEY,                    -- QBO Customer ID
    realm_id TEXT NOT NULL,
    display_name TEXT NOT NULL,
    sync_token TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE(realm_id, id)
);

CREATE INDEX IF NOT EXISTS idx_qbo_customers_realm ON qbo.customers(realm_id);

-- Invoices (AR Documents)
CREATE TABLE IF NOT EXISTS qbo.invoices (
    id TEXT PRIMARY KEY,                    -- QBO Invoice ID
    realm_id TEXT NOT NULL,
    customer_id TEXT,
    doc_number TEXT,
    total_amount DECIMAL(15,2),
    balance DECIMAL(15,2),
    due_date DATE,
    txn_date DATE,
    sync_token TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE(realm_id, id),
    FOREIGN KEY (customer_id) REFERENCES qbo.customers(id)
);

CREATE INDEX IF NOT EXISTS idx_qbo_invoices_realm ON qbo.invoices(realm_id);
CREATE INDEX IF NOT EXISTS idx_qbo_invoices_customer ON qbo.invoices(customer_id);

-- Bills (AP Documents)
CREATE TABLE IF NOT EXISTS qbo.bills (
    id TEXT PRIMARY KEY,                    -- QBO Bill ID
    realm_id TEXT NOT NULL,
    vendor_id TEXT,
    doc_number TEXT,
    total_amount DECIMAL(15,2),
    balance DECIMAL(15,2),
    due_date DATE,
    txn_date DATE,
    sync_token TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    UNIQUE(realm_id, id),
    FOREIGN KEY (vendor_id) REFERENCES qbo.vendors(id)
);

CREATE INDEX IF NOT EXISTS idx_qbo_bills_realm ON qbo.bills(realm_id);
CREATE INDEX IF NOT EXISTS idx_qbo_bills_vendor ON qbo.bills(vendor_id);

-- Proposed Transactions (AI Work-in-Progress)
CREATE TABLE IF NOT EXISTS qbo.proposed_transactions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL,
    source_type TEXT NOT NULL,              -- 'Receipt', 'BankFeed', 'Email'
    raw_amount DECIMAL(15,2) NOT NULL,
    raw_date DATE,
    raw_description TEXT,
    
    -- AI Predictions
    predicted_vendor_id TEXT,
    predicted_account_id TEXT,
    confidence_score DECIMAL(3,2),          -- 0.00 to 1.00
    ai_reasoning TEXT,                      -- e.g., "Matched 'Shell' via synonym"
    
    -- QBO Link (Once synced)
    qbo_transaction_id TEXT,
    sync_status TEXT DEFAULT 'PENDING',     -- 'PENDING', 'SYNCED', 'ERROR', 'REJECTED'
    error_message TEXT,
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_qbo_proposed_realm ON qbo.proposed_transactions(realm_id);
CREATE INDEX IF NOT EXISTS idx_qbo_proposed_status ON qbo.proposed_transactions(realm_id, sync_status);

-- +goose Down
DROP TABLE IF EXISTS qbo.proposed_transactions;
DROP TABLE IF EXISTS qbo.bills;
DROP TABLE IF EXISTS qbo.invoices;
DROP TABLE IF EXISTS qbo.customers;
DROP TABLE IF EXISTS qbo.vendors;
DROP TABLE IF EXISTS qbo.accounts;
DROP SCHEMA IF EXISTS qbo;
