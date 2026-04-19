-- +goose Up
-- =========================================================================
-- SCHEMA: fignode
-- The High-Velocity Layer: AI categorization & Employee "Swipe" UI.
-- Optimized for Plaid bank feeds, CSV batch uploads, and mobile UX.
-- =========================================================================
CREATE SCHEMA IF NOT EXISTS fignode;

-- =========================================================================
-- 1. Employee Profiles (Gamification state, linked to toro_core.users)
-- =========================================================================
CREATE TABLE IF NOT EXISTS fignode.employee_profiles (
    user_id            UUID PRIMARY KEY REFERENCES toro_core.users(id) ON DELETE CASCADE,
    first_name         TEXT,
    last_name          TEXT,
    is_manager         BOOLEAN NOT NULL DEFAULT FALSE,
    ai_accuracy_score  NUMERIC(4,3) NOT NULL DEFAULT 1.000 CHECK (ai_accuracy_score BETWEEN 0 AND 1),
    streak             INT NOT NULL DEFAULT 0,
    streak_last_date   DATE,
    total_cleared      INT NOT NULL DEFAULT 0,
    today_cleared      INT NOT NULL DEFAULT 0,
    today_cleared_date DATE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =========================================================================
-- 2. Staging Sessions (For CSV Uploads / Catch-up Bookkeeping)
-- Used when a CPA uploads a bank statement manually. NULL for live Plaid feeds.
-- =========================================================================
CREATE TABLE IF NOT EXISTS fignode.staging_sessions (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id    TEXT,                -- QBO company ID (NULL for offline modes)
    created_by  UUID REFERENCES toro_core.users(id) ON DELETE SET NULL,
    file_name   TEXT,
    row_count   INT NOT NULL DEFAULT 0,
    status      TEXT NOT NULL DEFAULT 'PROCESSING',
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_fignode_sessions_realm  ON fignode.staging_sessions(realm_id) WHERE realm_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_fignode_sessions_status ON fignode.staging_sessions(realm_id, status) WHERE realm_id IS NOT NULL;

-- =========================================================================
-- 3. Staging Transactions (The Unified AI & "Tinder" Pipeline)
-- Handles both CSV rows and Plaid webhooks. Denormalized for fast mobile reads.
-- =========================================================================
CREATE TABLE IF NOT EXISTS fignode.staging_transactions (
    id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id           UUID REFERENCES fignode.staging_sessions(id) ON DELETE CASCADE, -- NULL if from Plaid
    realm_id             TEXT,      -- The QBO client

    -- A. Raw Input (Universal)
    source_type          TEXT NOT NULL DEFAULT 'BankFeed', -- 'CSV', 'BankFeed', 'Receipt'
    raw_description      TEXT,                             -- e.g., "AMZN Mktp US"
    raw_amount           TEXT NOT NULL,
    raw_date             DATE,

    -- B. Plaid / Open Banking Specifics (Rich Data)
    plaid_transaction_id TEXT UNIQUE,                      -- Prevents webhook duplicates
    plaid_account_id     TEXT,                             -- The specific bank account
    merchant_name        TEXT,                             -- Cleaned by Plaid (e.g., "Amazon")
    logo_url             TEXT,                             -- CRITICAL for mobile swipe UI
    plaid_category       TEXT,                             -- e.g., "Food and Drink"
    is_pending           BOOLEAN DEFAULT false,            -- True if not yet cleared by bank

    -- C. AI Predictions (Denormalized for instant mobile rendering)
    predicted_vendor_id  UUID REFERENCES shadow_erp.vendors(id) ON DELETE SET NULL,
    predicted_vendor_name TEXT,                            -- Cached to avoid joins on mobile feed
    predicted_customer_id  UUID REFERENCES shadow_erp.customers(id) ON DELETE SET NULL,
    predicted_customer_name TEXT,                            -- Cached to avoid joins on mobile feed
    predicted_account_id UUID REFERENCES shadow_erp.accounts(id) ON DELETE SET NULL,
    predicted_account_name TEXT,                           -- Cached to avoid joins on mobile feed
    confidence_score     DECIMAL(3,2),                     -- 0.00-1.00 AI certainty
    ai_reasoning         TEXT,                             -- "Matches past Starbucks purchases"

    -- D. Human "Swipe" Mechanics (The Tinder Action)
    human_action         TEXT,                             -- 'SWIPED_RIGHT', 'SWIPED_LEFT', 'SKIPPED', 'ASK_CLIENT'
    swiped_by            UUID REFERENCES toro_core.users(id) ON DELETE SET NULL,
    swiped_at            TIMESTAMPTZ,
    override_vendor_id   UUID REFERENCES shadow_erp.vendors(id) ON DELETE SET NULL,
    override_customer_id UUID REFERENCES shadow_erp.customers(id) ON DELETE SET NULL,
    override_account_id  UUID REFERENCES shadow_erp.accounts(id) ON DELETE SET NULL,

    -- E. Smart Discovery Flags
    duplicate_of         UUID REFERENCES fignode.staging_transactions(id) ON DELETE SET NULL,
    is_recurring         BOOLEAN NOT NULL DEFAULT false,
    split_suggestion     JSONB,                            -- Complex splits [{"account_id":"...", "amount":50}]

    -- F. Lifecycle State Machine
    -- States: PENDING_AI -> READY_FOR_REVIEW -> SWIPED_APPROVED -> POSTING_TO_ERP -> POSTED
    status               TEXT NOT NULL DEFAULT 'PENDING_AI',
    erp_transaction_id   TEXT,                             -- The QBO 'Purchase' or 'Deposit' ID
    error_message        TEXT,                             -- If QBO API rejects it

    created_at           TIMESTAMPTZ DEFAULT NOW(),
    updated_at           TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(realm_id, erp_transaction_id)
);

-- Indexes optimized for the Mobile "Swipe Feed" queries and background workers
CREATE INDEX IF NOT EXISTS idx_fignode_tx_status_realm ON fignode.staging_transactions(realm_id, status);
CREATE INDEX IF NOT EXISTS idx_fignode_tx_plaid_id     ON fignode.staging_transactions(plaid_transaction_id) WHERE plaid_transaction_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_fignode_tx_session      ON fignode.staging_transactions(session_id) WHERE session_id IS NOT NULL;

-- =========================================================================
-- 4. Leaderboard Snapshots (Materialized for Gamification)
-- =========================================================================
CREATE TABLE IF NOT EXISTS fignode.leaderboard_snapshots (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    period      TEXT NOT NULL CHECK (period IN ('daily','weekly','all-time')),
    computed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    entries     JSONB NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_fignode_lb_period_time ON fignode.leaderboard_snapshots(period, computed_at DESC);

-- =========================================================================
-- 5. Fignode Industries (For profile customization)
-- =========================================================================
CREATE TABLE IF NOT EXISTS fignode.fignode_industries (
    name VARCHAR PRIMARY KEY,
    icon_emoji VARCHAR NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO fignode.fignode_industries (name, icon_emoji) VALUES
('Food Delivery', '🍔'), ('Payment Processing', '💳'), ('D2C E-Commerce', '📦'),
('E-Commerce / Retail', '🛒'), ('Petroleum / Fuel', '⛽'), ('Construction & Trades', '🏗️'),
('Creative Software (SaaS)', '🎨'), ('Creative Agency', '💡'), ('Point of Sale', '🧾'),
('Medical Practice', '🏥'), ('Commercial Aviation', '✈️'), ('B2B Software (SaaS)', '💻'),
('Productivity Software (SaaS)', '☁️'), ('Office Retail', '📎'), ('Financial Software', '🏦'),
('Legal Services', '⚖️'), ('Digital Advertising', '📢'), ('Telecommunications', '📡'),
('Real Estate Agency', '🏠'), ('Co-Working & Office Space', '🏢'), ('Online Payments', '📱'),
('Payroll & Accounting SaaS', '💼'), ('Restaurant Group', '🍽️'), ('Fast Food / Restaurant', '🍗'),
('Big Box Retail', '🏪'), ('Property Management', '🏘️'), ('Banking', '🏧'),
('Property & Casualty Insurance', '🛡️'), ('Logistics & Freight', '🚚'), ('Plumbing & Trades', '🔧'),
('Retail', '🛒'), ('Food & Beverage', '☕')
ON CONFLICT (name) DO NOTHING;


-- +goose Down
DROP TABLE IF EXISTS fignode.fignode_industries;
DROP TABLE IF EXISTS fignode.leaderboard_snapshots;
DROP TABLE IF EXISTS fignode.staging_transactions;
DROP TABLE IF EXISTS fignode.staging_sessions;
DROP TABLE IF EXISTS fignode.employee_profiles;
DROP SCHEMA IF EXISTS fignode CASCADE;
