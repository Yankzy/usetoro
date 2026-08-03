-- +goose Up
-- =========================================================================
-- SCHEMA: fignode (Bank Reconciliation / 4-Way Matcher)
-- =========================================================================

-- Master Vendors
-- This table is used to find vendor names that might have variants from the fignode.vendor_aliases table.
CREATE TABLE fignode.canonical_vendors (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_id UUID NOT NULL,
    realm_id TEXT, -- QBO Company ID
    display_name VARCHAR(255) NOT NULL,
    ice_number VARCHAR(15), 
    default_account VARCHAR(20) NOT NULL, 
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Variant Lookup Table
-- This all the variants of a vendor name.
CREATE TABLE fignode.vendor_aliases (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_id UUID NOT NULL,
    realm_id TEXT,
    raw_variant VARCHAR(255) NOT NULL,
    canonical_vendor_id UUID NOT NULL REFERENCES fignode.canonical_vendors(id) ON DELETE CASCADE,
    source VARCHAR(50) NOT NULL, 
    confidence_score NUMERIC(3,2) DEFAULT 1.00,
    CONSTRAINT unique_variant_per_tenant UNIQUE (entity_id, raw_variant)
);

CREATE INDEX idx_fignode_vendor_aliases_lookup ON fignode.vendor_aliases (entity_id, raw_variant);

-- State Tracking for 4-Way Match
CREATE TABLE fignode.order_reconciliations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    entity_id UUID NOT NULL,
    realm_id TEXT,
    bank_transaction_id UUID,
    facture_id UUID,
    bl_id UUID,
    bc_id UUID,
    status TEXT NOT NULL DEFAULT 'PENDING',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS fignode.order_reconciliations;
DROP TABLE IF EXISTS fignode.vendor_aliases;
DROP TABLE IF EXISTS fignode.canonical_vendors;
