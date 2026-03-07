-- +goose Up
-- =========================================================================
-- SCHEMA: shadow_erp
-- Clean-Up Mode: staging pipeline for messy/uncategorized transactions.
-- Data flows: PENDING → ENRICHED → APPROVED/REJECTED → POSTED
-- =========================================================================

-- =========================================================================
-- Cleanup Sessions (one per CSV/XLSX upload)
-- =========================================================================
CREATE TABLE IF NOT EXISTS shadow_erp.cleanup_sessions (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id    TEXT,                -- NULL when ERP is not connected (Excel-only mode)
    created_by  UUID REFERENCES toro_core.users(id) ON DELETE SET NULL,
    file_name   TEXT,
    row_count   INT NOT NULL DEFAULT 0,
    -- PENDING → ENRICHING → ENRICHED → POSTED
    status      TEXT NOT NULL DEFAULT 'PENDING',
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cleanup_sessions_realm  ON shadow_erp.cleanup_sessions(realm_id) WHERE realm_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_cleanup_sessions_status ON shadow_erp.cleanup_sessions(realm_id, status) WHERE realm_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_cleanup_sessions_user   ON shadow_erp.cleanup_sessions(created_by) WHERE created_by IS NOT NULL;

-- =========================================================================
-- Cleanup Staging Rows (one per raw transaction)
-- =========================================================================
CREATE TABLE IF NOT EXISTS shadow_erp.cleanup_staging (
    id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id           UUID NOT NULL REFERENCES shadow_erp.cleanup_sessions(id) ON DELETE CASCADE,
    realm_id             TEXT,      -- NULL mirrors the session when ERP is not connected

    -- Raw input fields
    raw_description      TEXT,
    raw_amount           DECIMAL(15,2) NOT NULL,
    raw_date             DATE,
    raw_vendor_name      TEXT,

    -- AI predictions
    predicted_vendor_id  UUID REFERENCES shadow_erp.vendors(id) ON DELETE SET NULL,
    predicted_account_id UUID REFERENCES shadow_erp.accounts(id) ON DELETE SET NULL,
    normalized_vendor    TEXT,                  -- Canonical vendor name after entity resolution
    confidence_score     DECIMAL(3,2),          -- 0.00–1.00 combined AI confidence
    ai_reasoning         TEXT,                  -- Human-readable rationale

    -- Smart discovery flags
    duplicate_of         UUID REFERENCES shadow_erp.cleanup_staging(id) ON DELETE SET NULL,
    is_recurring         BOOLEAN NOT NULL DEFAULT false,
    split_suggestion     JSONB,                 -- [{"account_id":"...","amount":100.00}, ...]

    -- CPA overrides (set when CPA changes AI suggestion)
    override_vendor_id   UUID REFERENCES shadow_erp.vendors(id) ON DELETE SET NULL,
    override_account_id  UUID REFERENCES shadow_erp.accounts(id) ON DELETE SET NULL,

    -- Lifecycle: PENDING → ENRICHED → APPROVED/REJECTED → POSTED
    status               TEXT NOT NULL DEFAULT 'PENDING',
    erp_transaction_id   TEXT,                  -- Populated after ERP batch write

    created_at           TIMESTAMPTZ DEFAULT NOW(),
    updated_at           TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cleanup_staging_session        ON shadow_erp.cleanup_staging(session_id);
CREATE INDEX IF NOT EXISTS idx_cleanup_staging_realm_session  ON shadow_erp.cleanup_staging(realm_id, session_id) WHERE realm_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_cleanup_staging_status         ON shadow_erp.cleanup_staging(session_id, status);
CREATE INDEX IF NOT EXISTS idx_cleanup_staging_duplicate      ON shadow_erp.cleanup_staging(duplicate_of) WHERE duplicate_of IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_cleanup_staging_vendor         ON shadow_erp.cleanup_staging(session_id, predicted_vendor_id) WHERE predicted_vendor_id IS NOT NULL;

-- Explicitly exclude cleanup staging tables from CDC publication.
-- These are transient scratch-space tables — they change on every upload,
-- enrichment step, and CPA click, producing massive WAL noise with no
-- downstream consumer value. They also lack the event_source column that
-- the CDC guard pattern requires, so any ledger.* consumer receiving these
-- events would not be able to call IsInternal() safely.
--
-- The DO block is idempotent: it only calls ALTER PUBLICATION when the
-- tables are actually present in the publication (e.g. if 005 was re-run
-- after 011 had already created the tables).
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_publication_tables
        WHERE pubname    = 'toro_ledger_pub'
          AND schemaname = 'shadow_erp'
          AND tablename  IN ('cleanup_sessions', 'cleanup_staging')
    ) THEN
        ALTER PUBLICATION toro_ledger_pub DROP TABLE
            shadow_erp.cleanup_sessions,
            shadow_erp.cleanup_staging;
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS shadow_erp.cleanup_staging;
DROP TABLE IF EXISTS shadow_erp.cleanup_sessions;
