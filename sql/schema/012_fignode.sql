-- +goose Up
-- =========================================================================
-- SCHEMA: fignode
-- The High-Velocity Layer: Employee bookkeeping classification.
-- Focuses on streamlined categorization and internal gamification (leaderboards, streaks).
-- =========================================================================
CREATE SCHEMA IF NOT EXISTS fignode;

-- =========================================================================
-- Extend toro_core.users with user_type discriminator
-- =========================================================================
ALTER TABLE toro_core.users
    ADD COLUMN IF NOT EXISTS user_type TEXT NOT NULL DEFAULT 'standard';

-- =========================================================================
-- 1. Categories (Chart of Accounts — 20 fixed bookkeeping categories)
-- =========================================================================
CREATE TABLE fignode.categories (
    id         TEXT PRIMARY KEY,
    label      TEXT UNIQUE NOT NULL,
    sort_order SMALLINT NOT NULL
);

INSERT INTO fignode.categories (id, label, sort_order) VALUES
    ('MEALS',        'Meals & Entertainment',     0),
    ('TRAVEL',       'Travel & Transportation',   1),
    ('OFFICE',       'Office Supplies',           2),
    ('SOFTWARE',     'Software & Subscriptions',  3),
    ('UTILITIES',    'Utilities',                 4),
    ('RENT',         'Rent & Lease',              5),
    ('INSURANCE',    'Insurance',                 6),
    ('PROFESSIONAL', 'Professional Services',     7),
    ('ADVERTISING',  'Advertising & Marketing',   8),
    ('REPAIRS',      'Repairs & Maintenance',     9),
    ('PHONE',        'Phone & Internet',         10),
    ('PAYROLL',      'Payroll Expenses',          11),
    ('TAXES',        'Taxes & Licenses',          12),
    ('BANK_FEES',    'Bank Charges & Fees',       13),
    ('EDUCATION',    'Education & Training',      14),
    ('SUPPLIES',     'General Supplies',          15),
    ('AUTO',         'Auto & Fuel',              16),
    ('MEDICAL',      'Medical & Health',          17),
    ('CHARITY',      'Charitable Contributions',  18),
    ('MISC',         'Miscellaneous Expense',     19);

-- =========================================================================
-- 2. Employee Profiles (gamification state, linked to toro_core.users)
-- =========================================================================
CREATE TABLE fignode.employee_profiles (
    user_id            UUID PRIMARY KEY REFERENCES toro_core.users(id) ON DELETE CASCADE,
    first_name         TEXT,
    last_name          TEXT,
    is_manager         BOOLEAN NOT NULL DEFAULT FALSE,
    ai_accuracy_score  NUMERIC(4,3) NOT NULL DEFAULT 1.000
        CHECK (ai_accuracy_score BETWEEN 0 AND 1),
    streak             INT NOT NULL DEFAULT 0,
    streak_last_date   DATE,
    total_cleared      INT NOT NULL DEFAULT 0,
    today_cleared      INT NOT NULL DEFAULT 0,
    today_cleared_date DATE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER update_employee_profiles_updated_at
    BEFORE UPDATE ON fignode.employee_profiles
    FOR EACH ROW EXECUTE FUNCTION toro_core.update_updated_at_column();

-- =========================================================================
-- 3. Transactions (raw bank transactions surfaced to employees)
-- =========================================================================
CREATE TABLE fignode.transactions (
    id                   TEXT PRIMARY KEY,
    raw_description      TEXT NOT NULL,
    vendor               TEXT NOT NULL,
    industry             TEXT NOT NULL,
    industry_icon        TEXT NOT NULL,
    vendor_description   TEXT NOT NULL,
    vendor_url           TEXT,
    location             TEXT NOT NULL,
    is_recurring         BOOLEAN NOT NULL DEFAULT FALSE,
    client_industry      TEXT NOT NULL,
    client_industry_icon TEXT NOT NULL,
    business_model       TEXT NOT NULL
        CHECK (business_model IN (
            'Brick & Mortar','Remote First','VC Backed',
            'Hybrid','Field-Based','Professional','Service-Based'
        )),
    mindset_hint         TEXT NOT NULL,
    accent_color         TEXT NOT NULL,
    accent_bg            TEXT NOT NULL,
    amount               NUMERIC(12,4) NOT NULL,
    tx_date              DATE NOT NULL,
    account_type         TEXT NOT NULL,
    tx_timestamp         TIMESTAMPTZ NOT NULL,
    ai_suggestion        TEXT NOT NULL REFERENCES fignode.categories(label),
    ai_confidence        NUMERIC(4,3) NOT NULL
        CHECK (ai_confidence BETWEEN 0 AND 1),
    truth_category       TEXT REFERENCES fignode.categories(label),
    status               TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open','in_review','cleared','exported_to_qbo')),
    cleared_at           TIMESTAMPTZ,
    owner_user_id        UUID REFERENCES toro_core.users(id),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_fignode_tx_status    ON fignode.transactions(status);
CREATE INDEX idx_fignode_tx_open      ON fignode.transactions(status) WHERE status = 'open';
CREATE INDEX idx_fignode_tx_date      ON fignode.transactions(tx_date);

-- =========================================================================
-- 4. Classifications (every categorization submitted)
-- =========================================================================
CREATE TABLE fignode.classifications (
    id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    transaction_id       TEXT NOT NULL REFERENCES fignode.transactions(id),
    user_id              UUID NOT NULL REFERENCES toro_core.users(id),
    category             TEXT NOT NULL REFERENCES fignode.categories(label),
    action               TEXT NOT NULL CHECK (action IN ('APPROVE','RECLASSIFY')),
    approved_by          UUID REFERENCES toro_core.users(id),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (transaction_id, user_id)
);

CREATE INDEX idx_fignode_cls_tx   ON fignode.classifications(transaction_id);
CREATE INDEX idx_fignode_cls_user ON fignode.classifications(user_id);
CREATE INDEX idx_fignode_cls_date ON fignode.classifications(created_at);

-- =========================================================================
-- 5. Skips (skipped transactions)
-- =========================================================================
CREATE TABLE fignode.skips (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    transaction_id TEXT NOT NULL REFERENCES fignode.transactions(id),
    user_id        UUID NOT NULL REFERENCES toro_core.users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (transaction_id, user_id)
);

-- =========================================================================
-- 6. Batches (tracks which batch was served to which user)
-- =========================================================================
CREATE TABLE fignode.batches (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID NOT NULL REFERENCES toro_core.users(id),
    transaction_ids TEXT[] NOT NULL,
    served_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ
);

CREATE INDEX idx_fignode_batches_user ON fignode.batches(user_id);

-- =========================================================================
-- 7. Leaderboard Snapshots (materialized every 5 minutes)
-- =========================================================================
CREATE TABLE fignode.leaderboard_snapshots (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    period      TEXT NOT NULL CHECK (period IN ('daily','weekly','all-time')),
    computed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    entries     JSONB NOT NULL
);

CREATE INDEX idx_fignode_lb_period_time ON fignode.leaderboard_snapshots(period, computed_at DESC);

-- =========================================================================
-- 8. Badges (earned badges — referenced in leaderboard entries)
-- =========================================================================
CREATE TABLE fignode.badges (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID NOT NULL REFERENCES toro_core.users(id),
    badge_key   TEXT NOT NULL,
    badge_label TEXT NOT NULL,
    earned_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (user_id, badge_key)
);

CREATE INDEX idx_fignode_badges_user ON fignode.badges(user_id);

-- =========================================================================
-- Exclude fignode tables from CDC publication
-- =========================================================================
-- +goose StatementBegin
DO $$
DECLARE
    _tbl RECORD;
BEGIN
    FOR _tbl IN
        SELECT schemaname, tablename
        FROM pg_publication_tables
        WHERE pubname    = 'toro_ledger_pub'
          AND schemaname = 'fignode'
    LOOP
        EXECUTE format(
            'ALTER PUBLICATION toro_ledger_pub DROP TABLE %I.%I',
            _tbl.schemaname, _tbl.tablename
        );
    END LOOP;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS fignode.badges;
DROP TABLE IF EXISTS fignode.leaderboard_snapshots;
DROP TABLE IF EXISTS fignode.batches;
DROP TABLE IF EXISTS fignode.skips;
DROP TABLE IF EXISTS fignode.classifications;
DROP TABLE IF EXISTS fignode.transactions;
DROP TABLE IF EXISTS fignode.employee_profiles;
DROP TABLE IF EXISTS fignode.categories;
ALTER TABLE toro_core.users DROP COLUMN IF EXISTS user_type;
DROP SCHEMA IF EXISTS fignode;
