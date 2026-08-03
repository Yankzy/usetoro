-- +goose Up
-- =========================================================================
-- SCHEMA: shadow_erp (Pre-Sage Email MVP Extensions)
-- =========================================================================

-- 1. Client Dossiers Master (Maps accounting firm tenant & dossier_code e.g. rap_1042@a.usetoro.io)
CREATE TABLE IF NOT EXISTS shadow_erp.client_dossiers (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL UNIQUE, -- Multi-tenant ERP Company ID joining shadow_erp tables
    fiduciaire_id UUID NOT NULL, -- Accounting firm tenant ID
    dossier_code VARCHAR(50) NOT NULL, -- e.g. "1042", "atlas_sarl" (used in email alias rap_1042@a.usetoro.io)
    company_name VARCHAR(255) NOT NULL,
    ice_number VARCHAR(15), -- Identifiant Commun de l'Entreprise (15 digits)
    sage_template_profile_id UUID,
    event_source TEXT NOT NULL DEFAULT 'toro_internal',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    CONSTRAINT unique_dossier_per_fiduciaire UNIQUE (fiduciaire_id, dossier_code)
);

CREATE INDEX IF NOT EXISTS idx_shadow_erp_dossiers_lookup ON shadow_erp.client_dossiers (fiduciaire_id, dossier_code);
CREATE INDEX IF NOT EXISTS idx_shadow_erp_dossiers_realm  ON shadow_erp.client_dossiers (realm_id);

-- 2. Per-Client Sage Import Template Profiles (Custom Sage .PNM formats per dossier)
CREATE TABLE IF NOT EXISTS shadow_erp.sage_import_templates (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL, -- Keyed to ERP Company ID
    template_name VARCHAR(100) NOT NULL, -- e.g. "Sage 100 Coala Standard", "Custom PNM"
    delimiter VARCHAR(5) NOT NULL DEFAULT ';',
    date_format VARCHAR(20) NOT NULL DEFAULT '020106', -- DDMMYY
    column_mapping JSONB NOT NULL, 
    -- JSONB Structure: 
    -- { "columns": ["journal_code", "date", "general_account", "auxiliary_account", "piece_ref", "libelle", "debit", "credit"], "has_header": false }
    event_source TEXT NOT NULL DEFAULT 'toro_internal',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_shadow_erp_sage_templates_realm ON shadow_erp.sage_import_templates (realm_id);

-- 3. Stateful Reconciliation Tasks (Tracks stateful email threads & HITL)
CREATE TABLE IF NOT EXISTS shadow_erp.reconciliation_tasks (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    realm_id TEXT NOT NULL REFERENCES shadow_erp.client_dossiers(realm_id) ON DELETE CASCADE,
    period_label VARCHAR(50) NOT NULL, -- e.g. "2026-07"
    status VARCHAR(50) NOT NULL DEFAULT 'OPEN', -- 'OPEN', 'HOLD_MISSING_DOCS', 'HOLD_DISCREPANCY', 'CLOSED'
    email_thread_id VARCHAR(255), -- Postmark Message-ID / In-Reply-To header
    missing_docs_summary JSONB, -- List of missing BLs / Factures for interactive reply
    discrepancies JSONB, -- List of fee deltas (e.g. 15 MAD bank fee)
    event_source TEXT NOT NULL DEFAULT 'toro_internal',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_shadow_erp_rec_tasks_realm  ON shadow_erp.reconciliation_tasks (realm_id);
CREATE INDEX IF NOT EXISTS idx_shadow_erp_rec_tasks_status ON shadow_erp.reconciliation_tasks (realm_id, status);

-- +goose Down
DROP TABLE IF EXISTS shadow_erp.reconciliation_tasks;
DROP TABLE IF EXISTS shadow_erp.sage_import_templates;
DROP TABLE IF EXISTS shadow_erp.client_dossiers;
