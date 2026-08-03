-- +goose Up
-- =========================================================================
-- SEED DATA for Email Testing
-- =========================================================================

-- Ensure we have the uuid extension if not already (should be in 001, but just in case)
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- 1. Insert a Sage Import Template for the test realm
INSERT INTO shadow_erp.sage_import_templates (id, realm_id, template_name, delimiter, date_format, column_mapping)
VALUES (
    '11111111-1111-1111-1111-111111111111',
    'test-realm-1',
    'Test Sage Template',
    ';',
    '020106',
    '{"columns": ["date", "journal_code", "general_account", "libelle", "debit", "credit"], "has_header": true}'
) ON CONFLICT DO NOTHING;

-- 2. Insert a Client Dossier mapped to this template
INSERT INTO shadow_erp.client_dossiers (id, realm_id, fiduciaire_id, dossier_code, company_name, ice_number, sage_template_profile_id)
VALUES (
    uuid_generate_v4(),
    'test-realm-1',
    uuid_generate_v4(),
    'test100',
    'SARL MAROC EXEMPLE',
    '123456789012345',
    '11111111-1111-1111-1111-111111111111'
) ON CONFLICT (realm_id) DO NOTHING;

-- 3. Insert a Canonical Vendor matching the Facture.pdf
INSERT INTO fignode.canonical_vendors (id, entity_id, realm_id, display_name, ice_number, default_account)
VALUES (
    uuid_generate_v4(),
    uuid_generate_v4(),
    'test-realm-1',
    'STE INFORMATIQUE SARL',
    '001122334455667',
    '4411000' -- Standard Moroccan chart of account for local vendors
);

-- +goose Down
DELETE FROM fignode.canonical_vendors WHERE realm_id = 'test-realm-1';
DELETE FROM shadow_erp.client_dossiers WHERE realm_id = 'test-realm-1';
DELETE FROM shadow_erp.sage_import_templates WHERE realm_id = 'test-realm-1';
